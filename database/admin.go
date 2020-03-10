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

package database

import (
	"context"
	"fmt"
	"regexp"
	"sync"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"github.com/prometheus/common/model"

	admin "cloud.google.com/go/spanner/admin/database/apiv1"
	dpb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
)

var (
	dbRegExp = regexp.MustCompile("^(.*)/databases/(.*)$")
)

const (
	// Table Organization of Truestreet.
	createIndexTable = `CREATE TABLE index (
		lid INT64,
		tsid INT64,
	) PRIMARY KEY (lid, tsid)`
	createLabelsTable = `CREATE TABLE labels (
		lid INT64,
		name STRING(MAX),
		value STRING(MAX),
	) PRIMARY KEY (name, value)`
	createSamplesTable = `CREATE TABLE samples (
		tsid INT64,
		timestamp INT64,
		value FLOAT64
	) PRIMARY KEY (tsid, timestamp DESC)`
	// Create index for speeding up common lookup.
	createTSIDIndex = `CREATE INDEX tsid_index ON index(tsid)`
	createLIDIndex  = `CREATE INDEX lid_index ON labels(lid)`
)

// Config gets passed to functions that start TrueStreet's connection with Spanner.
type Config struct {
	Database string
	Workers  int
	Metrics  *DBMetrics
}

// parseDatabase takes in a path and parses it into its parent and database name.
func parseDatabase(db string) (parent string, database string, err error) {
	matches := dbRegExp.FindStringSubmatch(db)
	if matches == nil || len(matches) != 3 {
		return "", "", fmt.Errorf("invalid database id")
	}
	return matches[1], matches[2], nil
}

// NewDatabase creates a new database on Cloud Spanner at the provided string.
func NewDatabase(ctx context.Context, config *Config) (*database, error) {
	parent, databaseName, err := parseDatabase(config.Database)
	if err != nil {
		return nil, err
	}
	adminClient, err := admin.NewDatabaseAdminClient(ctx)
	if err != nil {
		return nil, err
	}
	defer adminClient.Close()
	op, err := adminClient.CreateDatabase(ctx, &dpb.CreateDatabaseRequest{
		Parent:          parent,
		CreateStatement: fmt.Sprintf("CREATE DATABASE `%s`", databaseName),
		ExtraStatements: []string{createIndexTable, createLabelsTable, createSamplesTable, createTSIDIndex, createLIDIndex},
	})
	if err != nil {
		return nil, err
	}
	if _, err := op.Wait(ctx); err != nil {
		return nil, err
	}
	return LoadDatabase(ctx, config)
}

// Exists returns true if the database given by the string exists on Cloud Spanner.
func Exists(ctx context.Context, db string) (bool, error) {
	parent, _, err := parseDatabase(db)
	if err != nil {
		return false, err
	}
	adminClient, err := admin.NewDatabaseAdminClient(ctx)
	if err != nil {
		return false, err
	}
	defer adminClient.Close()
	iter := adminClient.ListDatabases(ctx, &dpb.ListDatabasesRequest{Parent: parent})

	for database, err := iter.Next(); err != iterator.Done; {
		if err != nil {
			return false, err
		}
		if database.Name == db {
			return true, nil
		}
		database, err = iter.Next()
	}
	return false, nil
}

// LoadDatabase sets up clients with already existing database in Cloud Spanner.
func LoadDatabase(ctx context.Context, config *Config) (*database, error) {
	// Channel buffer to allow for queue of write requests
	const chanBufSize = 200
	dataClient, err := spanner.NewClient(ctx, config.Database)
	if err != nil {
		return nil, err
	}
	d := &database{
		dataClient: dataClient,
		metrics:    config.Metrics,
		// Give metricsChan somewhat of a buffer to process new metrics.
		metricsChan: make(chan []model.Metric, chanBufSize),
		samplesChan: make(chan []*Sample, chanBufSize),
		sem:         make(chan struct{}, config.Workers),
		tsCache:     sync.Map{},
		labelCache:  make(map[model.Fingerprint]bool),
	}
	go samplesMaster(ctx, d)
	go metricsMaster(ctx, d)
	return d, nil
}

// Close closes the database clients.
func (d *database) Close() {
	d.dataClient.Close()
}
