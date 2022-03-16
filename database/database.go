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

// Package database implements functions to store information in Cloud Spanner databases.
package database

import (
	"bytes"
	"context"
	"sync"
	"text/template"
	"time"

	"fmt"
	"log"
	"regexp"
	"sort"

	"cloud.google.com/go/spanner"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	prompb "github.com/prometheus/prometheus/prompb"
)

const (
	abortedBackoff = time.Second
	batchSize      = 1000
)

// DBMetrics stores prometheus metrics to record data about the database.
// SpannerDuration and SpannerError metrics should be stored as "write" and "read" in the maps.
type DBMetrics struct {
	SpannerDuration map[string]prometheus.Histogram
	SpannerErrors   map[string]prometheus.Counter
	Samples         map[string]prometheus.Summary
}

// Database interface for the external functions of truestreet.
type Database interface {
	Write(ctx context.Context, ts *TimeSeries) error
	Read(ctx context.Context, queries []*prompb.Query) (*prompb.ReadResponse, error)
	Close()
}

// database for interacting with Cloud Spanner's data client.
type database struct {
	dataClient  spannerClient
	metrics     *DBMetrics
	metricsChan chan []model.Metric
	samplesChan chan []*Sample
	sem         chan struct{}
	tsCache     sync.Map
	labelCache  map[model.Fingerprint]bool
}

// spannerClient is an interface that describes the functions that the database functions rely on to
// operate. This interface gets fulfilled by the Cloud Spanner package, but also can be used for testing.
type spannerClient interface {
	ReadOnlyTransaction() *spanner.ReadOnlyTransaction
	Apply(ctx context.Context, ms []*spanner.Mutation, opts ...spanner.ApplyOption) (time.Time, error)
	Close()
}

// Sample struct stores a single sample with the TSID identifier rather than the metric map.
type Sample struct {
	TSID      model.Fingerprint
	Timestamp model.Time
	Value     model.SampleValue
}

// TimeSeries struct for storing protobuf timeSeries data using prometheus model structs.
type TimeSeries struct {
	Metrics []model.Metric
	Samples []*Sample
}

// indexRow is a struct representation of a Spanner row in the index table.
type indexRow struct {
	LID  int64 `spanner:lid`
	TSID int64 `spanner:tsid`
}

// labelRow is a struct representation of a Spanner row in the labels table.
type labelRow struct {
	LID   int64  `spanner:lid`
	Name  string `spanner:name`
	Value string `spanner:value`
}

// generateTimeSeriesExists uses templates to create SQL Query for identifying existing timeseries.
func generateTimeSeriesExists(metrics []model.Metric) *spanner.Statement {
	fps := make([]int64, 0, len(metrics))
	for _, m := range metrics {
		fps = append(fps, int64(m.Fingerprint()))
	}
	return &spanner.Statement{
		SQL:    "SELECT DISTINCT tsid FROM index WHERE tsid IN UNNEST(@array)",
		Params: map[string]interface{}{"array": fps},
	}
}

// timeSeriesExists takes a timeSeries and checks if its data exists on Cloud Spanner.
func (d *database) timeSeriesExists(ctx context.Context, metrics []model.Metric) (map[int64]bool, error) {
	stmt := generateTimeSeriesExists(metrics)
	txn := d.dataClient.ReadOnlyTransaction()
	defer txn.Close()
	// Return the IDs of all timeseries that were found.
	foundSet := make(map[int64]bool)
	if err := txn.Query(ctx, *stmt).Do(func(row *spanner.Row) error {
		var tsid int64
		if err := row.Columns(&tsid); err != nil {
			return err
		}
		foundSet[tsid] = true
		return nil
	}); err != nil {
		return nil, err
	}
	return foundSet, nil
}

// addTimeSeries takes a slice of metrics and returns a slice of rows to add to index and a map of rows to add to labels.
func addTimeSeries(metrics []model.Metric, lCache map[model.Fingerprint]bool) ([]*indexRow, []*labelRow) {
	var indexRows []*indexRow
	// A map of new labels because many missing metrics might a lot of the same labels. So this eliminates the
	// possibility of adding the same labels to the labels table in a single write.
	labelRowsMap := make(map[int64]*labelRow)
	for _, m := range metrics {
		tsid := m.Fingerprint()
		for name, value := range m {
			lid := model.LabelSet{name: value}.Fingerprint()
			if _, ok := lCache[lid]; !ok {
				labelRowsMap[int64(lid)] = &labelRow{LID: int64(lid), Name: string(name), Value: string(value)}
			}
			indexRows = append(indexRows, &indexRow{LID: int64(lid), TSID: int64(tsid)})
		}
	}
	labelRows := make([]*labelRow, 0, len(labelRowsMap))
	for _, row := range labelRowsMap {
		labelRows = append(labelRows, row)
	}
	// Sort the returned rows for two reasons. First for more efficient transactions as less splits occur if written
	// rows' primary keys have locality. Second, for labelRows it makes testing output easier.
	sort.Slice(labelRows, func(i, j int) bool { return labelRows[i].LID < indexRows[j].LID })
	sort.Slice(indexRows, func(i, j int) bool { return indexRows[i].LID < indexRows[j].LID })
	return indexRows, labelRows
}

// applyMutations takes a context, a list of mutations, and a prometheus histogram (as a timer) and applies
// the mutations to the connected Spanner database.
func (d *database) applyMutations(ctx context.Context, mutations []*spanner.Mutation, promTimer prometheus.Histogram) error {
	timer := prometheus.NewTimer(promTimer)
	defer timer.ObserveDuration()
	_, err := d.dataClient.Apply(ctx, mutations)
	if err != nil {
		d.metrics.SpannerErrors["write"].Inc()
		return err
	}
	return nil
}

// updateIndexLabels takes in future rows of the index and labels table and adds them to Cloud Spanner.
func (d *database) updateIndexLabels(ctx context.Context, indexRows []*indexRow, labelRows []*labelRow) error {
	mutations := make([]*spanner.Mutation, 0, len(indexRows)+len(labelRows))
	for _, row := range indexRows {
		mutations = append(mutations, spanner.InsertOrUpdate("index", indexCols, []interface{}{row.LID, row.TSID}))
	}
	for _, row := range labelRows {
		mutations = append(mutations,
			spanner.InsertOrUpdate("labels", labelsCols, []interface{}{row.LID, row.Name, row.Value}))
	}
	for start := 0; start < len(mutations); start += batchSize {
		end := start + batchSize
		if end > len(mutations) {
			end = len(mutations)
		}
		if err := d.applyMutations(ctx, mutations[start:end], d.metrics.SpannerDuration["timeseries"]); err != nil {
			// applyMutations already records the error in its prometheus metric so we only need to log.
			log.Printf("applyMutations error for samples : %v", err)
		}
	}
	return nil
}

// metricsMaster is the organizer of the metricsWorker routines that perform writing of metrics to Cloud Spanner.
func metricsMaster(ctx context.Context, d *database) {
	const accumulate = 500
	for {
		var metricsAcc []model.Metric

	collection:
		for len(metricsAcc) < accumulate {
			select {
			case metrics := <-d.metricsChan:
				metricsAcc = append(metricsAcc, metrics...)
			default:
				break collection
			}
		}
		if len(metricsAcc) == 0 {
			metricsAcc = append(metricsAcc, <-d.metricsChan...)
		}
		// Sychronizing with the caches right now makes running multiple metrics workers actually
		// less effective. Only deal with a single worker.
		metricsWorker(ctx, d, metricsAcc)
	}
}

// metricsWorker takes in accumulated metrics and writes them to Cloud Spanner. Sorts them for a more efficient series of
// transactions if the number of samples is sufficiently large enough.
func metricsWorker(ctx context.Context, d *database, metricsAcc []model.Metric) {
	foundSet, err := d.timeSeriesExists(ctx, metricsAcc)
	if err != nil {
		log.Printf("timeSeriesExists error : %v", err)
		return
	}
	// Skip any work if all of the metrics already exist.
	if len(foundSet) == len(metricsAcc) {
		return
	}
	var missingMetrics []model.Metric
	for _, m := range metricsAcc {
		if _, has := foundSet[int64(m.Fingerprint())]; !has {
			missingMetrics = append(missingMetrics, m)
		}
	}
	indexRows, labelRows := addTimeSeries(missingMetrics, d.labelCache)
	if err := d.updateIndexLabels(ctx, indexRows, labelRows); err != nil {
		log.Printf("updateIndexLabels error : %v", err)
		return
	}
	for _, m := range metricsAcc {
		d.tsCache.Store(m.Fingerprint(), true)
		for name, value := range m {
			d.labelCache[model.LabelSet{name: value}.Fingerprint()] = true
		}
	}
}

// samplesMaster is the organizer of the samplesWorker routines that perform writing of samples to Cloud Spanner.
func samplesMaster(ctx context.Context, d *database) {
	const accumulate = 8000
	for {
		var samplesAcc []*Sample
	collection:
		for len(samplesAcc) < accumulate {
			select {
			case samples := <-d.samplesChan:
				samplesAcc = append(samplesAcc, samples...)
			default:
				break collection
			}
		}
		if len(samplesAcc) == 0 {
			samplesAcc = append(samplesAcc, <-d.samplesChan...)
		}
		d.sem <- struct{}{}
		go func(task []*Sample) {
			samplesWorker(ctx, d, task)
			<-d.sem
		}(samplesAcc)
	}
}

// samplesWorker takes in accumulated samples and writes them to Cloud Spanner. Sorts them for a more efficient series of
// transactions if the number of samples is sufficiently large enough.
func samplesWorker(ctx context.Context, d *database, samplesAcc []*Sample) {
	// By accumulating a large batch and organizing it into several smaller batches,
	// the small batches can be grouped up by primary key to have less splits over a transaction.
	sort.Slice(samplesAcc, func(i, j int) bool { return samplesAcc[i].TSID < samplesAcc[j].TSID })
	mutations := make([]*spanner.Mutation, 0, len(samplesAcc))
	for _, s := range samplesAcc {
		mutations = append(mutations, spanner.Insert("samples", []string{"timestamp", "value", "tsid"}, []interface{}{int64(s.Timestamp), float64(s.Value), int64(s.TSID)}))
	}
	for start := 0; start < len(mutations); start += batchSize {
		end := start + batchSize
		if end > len(mutations) {
			end = len(mutations)
		}
		if err := d.applyMutations(ctx, mutations[start:end], d.metrics.SpannerDuration["write"]); err != nil {
			log.Printf("applyMutations error for samples : %v", err)
		}
	}
	d.metrics.Samples["write"].Observe(float64(len(samplesAcc)))
}

// Write takes in samples and turns them into a mutation and
// sends them to Spanner through the dataClient to apply.
func (d *database) Write(ctx context.Context, ts *TimeSeries) error {
	if len(ts.Samples) == 0 {
		return nil
	}
	// Send all uncached metrics through channel until either channel blocks or all
	// metrics have been sent. Any unadded metrics can be added during next scrape.
	var metrics []model.Metric
	for _, m := range ts.Metrics {
		if _, has := d.tsCache.Load(m.Fingerprint()); !has {
			metrics = append(metrics, m)
		}
	}
	if len(metrics) > 0 {
		select {
		case d.metricsChan <- metrics:
		default:
		}
	}
	d.samplesChan <- ts.Samples
	return nil
}

// rowToSample converts a Cloud Spanner row to a sample and adds it to the appropriate timeseries.
func rowToSample(row *spanner.Row) (*prompb.Sample, model.Metric, error) {
	var names, labels []string
	var timestamp int64
	var value float64
	// Extract sample data from row.
	if err := row.Columns(&names, &labels, &timestamp, &value); err != nil {
		return nil, model.Metric{}, err
	}
	// Generate metric to get fingerprint.
	metric := model.Metric{}
	for i := 0; i < len(names); i++ {
		metric[model.LabelName(names[i])] = model.LabelValue(labels[i])
	}
	return &prompb.Sample{Timestamp: timestamp, Value: value}, metric, nil
}

// orderTimeSeries gives returned timeseries an ordering.
func orderTimeSeries(tsMap map[model.Fingerprint]*prompb.TimeSeries) []*prompb.TimeSeries {
	fps := make([]model.Fingerprint, 0, len(tsMap))
	for fp := range tsMap {
		fps = append(fps, fp)
	}
	sort.Slice(fps, func(i, j int) bool { return fps[i] < fps[j] })
	// Convert timeseries map to a list.
	tsList := make([]*prompb.TimeSeries, 0, len(tsMap))
	for _, fp := range fps {
		tsList = append(tsList, tsMap[fp])
	}
	return tsList
}

// rowsToTimeseries converts all the collected rows into a slice of the respective collection
// of timeseries that the rows belong to.
func rowsToTimeseries(iter *spanner.RowIterator) ([]*prompb.TimeSeries, error) {
	if iter == nil {
		return nil, nil
	}
	// Add row by row all the samples to a map of timeseries.
	tsMap := make(map[model.Fingerprint]*prompb.TimeSeries)
	if err := iter.Do(func(row *spanner.Row) error {
		sample, metric, err := rowToSample(row)
		if err != nil {
			return err
		}
		fp := metric.Fingerprint()
		if _, ok := tsMap[fp]; !ok {
			var labels []*prompb.Label
			for k, v := range metric {
				labels = append(labels, &prompb.Label{Name: string(k), Value: string(v)})
			}
			tsMap[fp] = &prompb.TimeSeries{Labels: labels}
		}
		tsMap[fp].Samples = append(tsMap[fp].Samples, *sample)
		return nil
	}); err != nil {
		return nil, err
	}
	return orderTimeSeries(tsMap), nil
}

// generateQuery takes a query created by prometheus and converts it to a query
// for Cloud Spanner to retrieve the desired rows of data.
func generateQuery(q *prompb.Query) (*spanner.Statement, error) {
	if q.EndTimestampMs < q.StartTimestampMs {
		return nil, fmt.Errorf("query end time %d is before start time %d", q.EndTimestampMs, q.StartTimestampMs)
	}
	type match struct {
		Index       int
		Type, Value string
	}
	var matchers, neqMatchers []*match
	params := make(map[string]interface{})
	for i, m := range q.Matchers {
		params[fmt.Sprintf("name%d", i)] = m.Name
		params[fmt.Sprintf("value%d", i)] = m.Value
		switch m.Type {
		case prompb.LabelMatcher_EQ:
			if m.Value == "" {
				neqMatchers = append(neqMatchers, &match{Index: i, Type: "NEQ", Value: m.Value})
				continue
			}
			matchers = append(matchers, &match{Index: i, Type: "EQ", Value: m.Value})
		case prompb.LabelMatcher_NEQ:
			if m.Value == "" {
				matchers = append(matchers, &match{Index: i, Type: "EQ", Value: m.Value})
				continue
			}
			neqMatchers = append(neqMatchers, &match{Index: i, Type: "NEQ", Value: m.Value})
		case prompb.LabelMatcher_RE:
			matchesEmpty, err := regexp.MatchString(fmt.Sprintf("(%s)", m.Value), "")
			if err != nil {
				return nil, err
			}
			if matchesEmpty {
				neqMatchers = append(neqMatchers, &match{Index: i, Type: "NEQ", Value: ""})
				continue
			}
			matchers = append(matchers, &match{Index: i, Type: "RE", Value: m.Value})
		case prompb.LabelMatcher_NRE:
			matchesEmpty, err := regexp.MatchString(fmt.Sprintf("(%s)", m.Value), "")
			if err != nil {
				return nil, err
			}
			if matchesEmpty {
				matchers = append(matchers, &match{Index: i, Type: "EQ", Value: ""})
				continue
			}
			neqMatchers = append(neqMatchers, &match{Index: i, Type: "NRE", Value: m.Value})
		default:
			return nil, fmt.Errorf("unrecognized matcher '%s'", m.Type)
		}
	}
	params["startMs"] = q.StartTimestampMs
	params["endMs"] = q.EndTimestampMs
	// StepMs can be an unset "0" in the case of requested very short time intervals.
	var msPerStep = q.Hints.StepMs
	if msPerStep == 0 || msPerStep > q.EndTimestampMs-q.StartTimestampMs {
		msPerStep = 1
	}
	// SampleCount is the maximum amount of samples subsampled from samples.
	params["sampleCount"] = (q.EndTimestampMs - q.StartTimestampMs) / msPerStep
	buf := new(bytes.Buffer)
	inputMatchers := struct {
		Matchers    []*match
		NeqMatchers []*match
	}{
		Matchers:    matchers,
		NeqMatchers: neqMatchers,
	}
	if err := query.Execute(buf, inputMatchers); err != nil {
		return nil, err
	}
	return &spanner.Statement{SQL: buf.String(), Params: params}, nil
}

// Read takes in queries and requests data from Cloud Spanner. Then packages it
// all into a read response for prometheus to be sent back.
func (d *database) Read(ctx context.Context, queries []*prompb.Query) (*prompb.ReadResponse, error) {
	resp := &prompb.ReadResponse{Results: []*prompb.QueryResult{}}
	if len(queries) == 0 {
		return resp, nil
	}
	for _, q := range queries {
		t := d.dataClient.ReadOnlyTransaction()
		defer t.Close()
		stmt, err := generateQuery(q)
		if err != nil {
			log.Printf("generateQuery failed: %v", err)
			return nil, err
		}
		// Send query to Cloud Spanner and process the receieved data.
		timer := prometheus.NewTimer(d.metrics.SpannerDuration["read"])
		iter := t.Query(ctx, *stmt)
		timer.ObserveDuration()
		tsList, err := rowsToTimeseries(iter)
		iter.Stop()
		if err != nil {
			d.metrics.SpannerErrors["read"].Inc()
			log.Printf("rowsToTimeseries failed: %v", err)
			return nil, err
		}
		for _, ts := range tsList {
			d.metrics.Samples["read"].Observe(float64(len(ts.Samples)))
			// Prometheus wants its samples to be sorted with it receives them.
			sort.Slice(ts.Samples, func(i, j int) bool { return ts.Samples[i].Timestamp < ts.Samples[j].Timestamp })
		}
		resp.Results = append(resp.Results, &prompb.QueryResult{Timeseries: tsList})
	}
	return resp, nil
}

var (
	query = template.Must(template.New("query").Parse(`
	{{- define "subQuery" -}}
	SELECT tsid FROM index JOIN (
		SELECT lid FROM labels WHERE
		(name=@name{{.Index}}{{if and (eq .Type "EQ" "NEQ") (.Value)}} AND value=@value{{.Index}}{{else if eq .Type "RE" "NRE"}} AND REGEXP_CONTAINS(value, r"^(@value{{.Index}})$"){{end}})
	) USING (lid)
	{{- end -}}
	{{- define "tsidQuery" -}}
	({{range $index, $element := .Matchers}}
		{{if $index}} INTERSECT ALL {{end}} {{template "subQuery" $element -}}
	{{end}})
	{{if .NeqMatchers}} EXCEPT ALL
		({{range $index, $element := .NeqMatchers}}
			{{if $index}} INTERSECT ALL {{end}}{{template "subQuery" $element -}}
		{{end}})
	{{end}}
	{{- end -}}
	SELECT * FROM (
		SELECT l_names, l_values, timestamp, value FROM (
			SELECT tsid, ARRAY_AGG(name) AS l_names, ARRAY_AGG(value) AS l_values FROM (
				SELECT tsid, lid FROM (
					{{- template "tsidQuery" . -}}
				) JOIN index USING (tsid)
			) JOIN labels USING (lid) GROUP BY tsid
		) JOIN samples USING (tsid) WHERE timestamp <= @endMs AND timestamp >= @startMs
	) TABLESAMPLE RESERVOIR (@sampleCount ROWS)`))
	indexCols  = []string{"lid", "tsid"}
	labelsCols = []string{"lid", "name", "value"}
)
