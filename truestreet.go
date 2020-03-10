// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main responsible for launching service for write
// requests to remote storage from the Prometheus monitoring system.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/prompb"
	"github.com/golang/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/google/truestreet/database"
)

// Command line flags and database.
var (
	port      = flag.String("port", "1760", "Port for serving truestreet")
	project   = flag.String("project", "axum-dev", "Name of GCP project")
	instance  = flag.String("instance", "test-instance", "Name of Cloud Spanner instance")
	db        = flag.String("db", "truestreet-db", "Name of database inside Cloud Spanner instance")
	createNew = flag.Bool("new", true, "Create a new database if one is not found")
	workers   = flag.Int("workers", 16, "Number of concurrent routines to deploy to write samples")

	help     = flag.Bool("help", false, "Prints help text.")
	dbClient database.Database
)

// Prometheus Metrics.
var (
	// Remote-Write.
	writeDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "spanner_adapter_write_latency_seconds",
		Help: "How long it took to respond to write requests.",
	})
	writeTimeSeries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "spanner_adapter_write_timeseries",
		Help: "How many timeseries are written.",
	})
	writeSamples = prometheus.NewSummary(prometheus.SummaryOpts{
		Name: "spanner_adapter_write_timeseries_samples",
		Help: "How many samples each written timeseries has.",
	})
	writeSpannerDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "spanner_adapter_write_spanner_latency_seconds",
		Help: "Latency for inserts to Cloud Spanner.",
	})
	writeTimeSeriesDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "spanner_adapter_write_timeseries_latency_seconds",
		Help: "How long it took to check/write timeseries with Spanner.",
	})
	writeSpannerErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "spanner_adapter_write_spanner_failed_total",
		Help: "How many inserts to Spanner failed.",
	})
	readDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "spanner_adapter_read_latency_seconds",
		Help: "How long it took to respond to read requests.",
	})
	readSamples = prometheus.NewSummary(prometheus.SummaryOpts{
		Name: "spanner_adapter_read_timeseries_samples",
		Help: "How many samples each returned timeseries has.",
	})
	readSpannerDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "spanner_adapter_read_spanner_latency_seconds",
		Help: "Latency for queries from Spanner.",
	})
	readErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "spanner_adapter_read_failed_total",
		Help: "How many requests to read from TrueStreet failed.",
	})
	readSpannerErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "spanner_adapter_read_spanner_failed_total",
		Help: "How many queries from Spanner failed.",
	})
	readQueries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "spanner_adapter_query_total",
		Help: "How many queries has prometheus sent.",
	})
)

// initMetrics registers all of the truestreet metrics to prometheus to be scraped.
func initMetrics() {
	prometheus.MustRegister(writeDuration)
	prometheus.MustRegister(writeTimeSeries)
	prometheus.MustRegister(writeSamples)
	prometheus.MustRegister(writeSpannerDuration)
	prometheus.MustRegister(writeTimeSeriesDuration)
	prometheus.MustRegister(writeSpannerErrors)
	prometheus.MustRegister(readDuration)
	prometheus.MustRegister(readSamples)
	prometheus.MustRegister(readSpannerDuration)
	prometheus.MustRegister(readSpannerErrors)
	prometheus.MustRegister(readErrors)
	prometheus.MustRegister(readQueries)
}

// protoToTimeseries takes in a WriteRequest and converts it to a more usable list of timeseries
// built off of the prometheus model package.
func protoToTimeseries(req *prompb.WriteRequest) (*database.TimeSeries, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	var metrics []model.Metric
	var samples []*database.Sample
	for _, ts := range req.Timeseries {
		metric := make(model.Metric, len(ts.Labels))
		for _, l := range ts.Labels {
			metric[model.LabelName(l.Name)] = model.LabelValue(l.Value)
		}
		metrics = append(metrics, metric)
		for _, s := range ts.Samples {
			samples = append(samples, &database.Sample{
				Value:     model.SampleValue(s.Value),
				Timestamp: model.Time(s.Timestamp),
				TSID:      metric.Fingerprint(),
			})
		}
	}
	writeTimeSeries.Add(float64(len(req.Timeseries)))
	return &database.TimeSeries{Metrics: metrics, Samples: samples}, nil
}

// writeHandler takes in write requests from prometheus and sends them to
// be written to Cloud Spanner.
func writeHandler(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(writeDuration)
	defer timer.ObserveDuration()

	if r.Body == nil {
		http.Error(w, "body is nil", http.StatusBadRequest)
		return
	}
	compressed, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("read error: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	reqBuf, err := snappy.Decode(nil, compressed)
	if err != nil {
		log.Printf("snappy decoding error: %v\n", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req prompb.WriteRequest
	if err := proto.Unmarshal(reqBuf, &req); err != nil {
		log.Printf("Unmarshal error: %v\n", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	timeseries, err := protoToTimeseries(&req)
	if err != nil {
		log.Printf("protoToTimeseries error: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := dbClient.Write(r.Context(), timeseries); err != nil {
		log.Printf("Failed to write : %v\n", err)
		if spanner.ErrCode(err) == codes.AlreadyExists {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		// Prometheus will retry on 500 error codes
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// readHandler takes in read requests from prometheus that contain a list of
// queries for Cloud Spanner and then writes back any data found through these queries.
func readHandler(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(readDuration)
	defer timer.ObserveDuration()
	if r.Body == nil {
		log.Printf("read request body is nil\n")
		http.Error(w, "body is nil", http.StatusBadRequest)
		return
	}
	compressed, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("read error : %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	reqBuf, err := snappy.Decode(nil, compressed)
	if err != nil {
		log.Printf("snappy decoding error : %v\n", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req prompb.ReadRequest
	if err := proto.Unmarshal(reqBuf, &req); err != nil {
		log.Printf("unmarshal error : %v\n", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	readQueries.Add(float64(len(req.Queries)))
	resp, err := dbClient.Read(r.Context(), req.Queries)
	if err != nil {
		readErrors.Inc()
		log.Printf("dataClient read error : %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := proto.Marshal(resp)
	if err != nil {
		log.Printf("marshal error : %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Content-Encoding", "snappy")

	compressed = snappy.Encode(nil, data)
	if _, err := w.Write(compressed); err != nil {
		log.Printf("error writing response : %v\n", err)
	}
}

// setupClients creates a database client to Cloud Spanner by first attempting to load
// the specified database. If this returns an error for whatever reason or
// if the database does not exist then it will attempt to create a new database.
func setupClients(ctx context.Context) error {
	dbName := fmt.Sprintf("projects/%s/instances/%s/databases/%s", *project, *instance, *db)
	exists, err := database.Exists(ctx, dbName)
	if err != nil {
		return err
	}
	// Metrics that belong to inner database actions.
	config := &database.Config{
		Database: dbName,
		Workers:  *workers,
		Metrics: &database.DBMetrics{
			SpannerDuration: map[string]prometheus.Histogram{
				"write":      writeSpannerDuration,
				"read":       readSpannerDuration,
				"timeseries": writeTimeSeriesDuration,
			},
			SpannerErrors: map[string]prometheus.Counter{
				"write": writeSpannerErrors,
				"read":  readSpannerErrors,
			},
			Samples: map[string]prometheus.Summary{
				"write": writeSamples,
				"read":  readSamples,
			},
		},
	}
	// Load or create database depending on whether it exists.
	if exists {
		log.Println("loading existing database")
		dbClient, err = database.LoadDatabase(ctx, config)
	} else if *createNew {
		log.Println("creating new database")
		dbClient, err = database.NewDatabase(ctx, config)
	} else {
		return errors.New("provided database not found")
	}
	if err != nil {
		return err
	}
	log.Printf("connected to database at [%s]\n", dbName)
	return nil
}

// main gets configuration information, sets up writers and launches TrueStreet.
func main() {
	flag.Parse()
	if *help {
		flag.PrintDefaults()
		log.Fatalln("exiting")
	}
	initMetrics()
	// Connect to Cloud Spanner and start serving at endpoint.
	ctx := context.Background()
	if err := setupClients(ctx); err != nil {
		log.Fatalf("failed to set up Cloud Spanner clients : %v", err)
	}
	// Load in handlers and start up server.
	http.HandleFunc("/write", writeHandler)
	http.HandleFunc("/read", readHandler)
	http.Handle("/metrics", promhttp.Handler())
	addr := fmt.Sprintf(":%s", *port)
	log.Printf("starting up TrueStreet server at %s...\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("failed to start up TrueStreet server : %v", err)
	}
	// Clean up.
	dbClient.Close()
	log.Fatalln("closing TrueStreet")
}
