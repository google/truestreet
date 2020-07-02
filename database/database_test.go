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

// Package database implements functions to store information in Cloud Spanner databases
package database

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/kylelemons/godebug/pretty"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/prompb"
)

type testSpannerClient struct {
	mutations []*spanner.Mutation
}

func (c *testSpannerClient) Apply(ctx context.Context, ms []*spanner.Mutation, opts ...spanner.ApplyOption) (time.Time, error) {
	c.mutations = append(c.mutations, ms...)
	return time.Now(), nil
}

func (c *testSpannerClient) ReadOnlyTransaction() *spanner.ReadOnlyTransaction {
	return &spanner.ReadOnlyTransaction{}
}

func (c *testSpannerClient) Close() {}

func createFakeDBSpanner() (*database, *testSpannerClient) {
	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{})
	counter := prometheus.NewCounter(prometheus.CounterOpts{})
	summary := prometheus.NewSummary(prometheus.SummaryOpts{})
	s := &testSpannerClient{}
	d := &database{
		dataClient: s,
		metrics: &DBMetrics{
			SpannerDuration: map[string]prometheus.Histogram{
				"timeseries": histogram,
				"write":      histogram,
			},
			SpannerErrors: map[string]prometheus.Counter{
				"write": counter,
			},
			Samples: map[string]prometheus.Summary{
				"write": summary,
			},
		},
	}
	return d, s
}

func TestParseDatabase(t *testing.T) {
	tests := []struct {
		input        string
		wantParent   string
		wantDatabase string
	}{
		{
			input:        "projects/axum-dev/instances/test-instance/databases/example-db",
			wantParent:   "projects/axum-dev/instances/test-instance",
			wantDatabase: "example-db",
		},
	}
	for _, tc := range tests {
		parent, database, err := parseDatabase(tc.input)
		if tc.wantParent != parent || tc.wantDatabase != database || err != nil {
			t.Errorf("parseDatabase(%s) = (%s,%s,%v), want (%s,%s,%v)",
				tc.input, parent, database, err, tc.wantParent, tc.wantDatabase, nil)
		}
	}
}

func TestParseDatabaseErr(t *testing.T) {
	tests := []struct {
		input        string
		wantParent   string
		wantDatabase string
		wantErr      error
	}{
		{
			input:        "IHopeThisFails",
			wantParent:   "",
			wantDatabase: "",
			wantErr:      fmt.Errorf("invalid database id"),
		},
		{
			input:        "",
			wantParent:   "",
			wantDatabase: "",
			wantErr:      fmt.Errorf("invalid database id"),
		},
	}
	for _, tc := range tests {
		parent, database, err := parseDatabase(tc.input)
		if err == nil {
			t.Errorf("parseDatabase(%s) = (%s,%s,%v), want (%s,%s,%v)",
				tc.input, parent, database, err, tc.wantParent, tc.wantDatabase, tc.wantErr)
		}
	}

}

func TestWrite(t *testing.T) {
	metric0 := model.Metric{model.LabelName("__name__"): model.LabelValue("events_total")}
	metric1 := model.Metric{model.LabelName("__name__"): model.LabelValue("fake_name")}
	d := &database{
		metricsChan: make(chan []model.Metric, 10),
		samplesChan: make(chan []*Sample, 10),
		tsCache:     sync.Map{},
	}
	tests := []struct {
		name    string
		input   *TimeSeries
		metrics []model.Metric
		samples []*Sample
	}{
		{
			name:  "Empty",
			input: &TimeSeries{},
		},
		{
			name: "Basic",
			input: &TimeSeries{
				Metrics: []model.Metric{metric0, metric1},
				Samples: []*Sample{
					{
						Value:     4.0,
						Timestamp: 1234,
						TSID:      metric0.Fingerprint(),
					},
					{
						Value:     42.0,
						Timestamp: 12334,
						TSID:      metric0.Fingerprint(),
					},
					{
						Value:     123.0,
						Timestamp: 1234,
						TSID:      metric1.Fingerprint(),
					},
				},
			},
			metrics: []model.Metric{metric0, metric1},
			samples: []*Sample{
				{
					Value:     4.0,
					Timestamp: 1234,
					TSID:      metric0.Fingerprint(),
				},
				{
					Value:     42.0,
					Timestamp: 12334,
					TSID:      metric0.Fingerprint(),
				},
				{
					Value:     123.0,
					Timestamp: 1234,
					TSID:      metric1.Fingerprint(),
				},
			},
		},
	}
	for _, tc := range tests {
		err := d.Write(context.Background(), tc.input)
		if err != nil {
			t.Errorf("test %s returned error when it should not have: %v", tc.name, err)
		}
		var gotMetrics []model.Metric
		var gotSamples []*Sample
	collect:
		for {
			select {
			case m := <-d.metricsChan:
				gotMetrics = append(gotMetrics, m...)
			case s := <-d.samplesChan:
				gotSamples = append(gotSamples, s...)
			default:
				break collect
			}
		}
		if diff := pretty.Compare(gotMetrics, tc.metrics); diff != "" {
			t.Errorf("test %s metrics failed. Incorrect metrics. diff: (-got +want)\n%s", tc.name, diff)
		}
		if diff := pretty.Compare(gotSamples, tc.samples); diff != "" {
			t.Errorf("test %s metrics failed. Incorrect samples. diff: (-got +want)\n%s", tc.name, diff)
		}
	}
}

func TestRowToSample(t *testing.T) {
	columnNames := []string{"l_names", "l_values", "timestamp", "value"}
	row1, err := spanner.NewRow(columnNames, []interface{}{[]string{"__name__"}, []string{"events_total"}, 42, 10.0})
	if err != nil {
		t.Fatal(err)
	}
	row2, err := spanner.NewRow(columnNames, []interface{}{[]string{"__name__"}, []string{"fake_name"}, 10, 11.1})
	if err != nil {
		t.Fatal(err)
	}
	row3, err := spanner.NewRow(columnNames, []interface{}{[]string{"__name__"}, []string{"fake_name"}, 1562113285506, 123.1})
	if err != nil {
		t.Fatal(err)
	}
	badRow4, err := spanner.NewRow([]string{"bad"}, []interface{}{42})
	if err != nil {
		t.Fatal(err)
	}

	ts1Metric := model.Metric{model.LabelName("__name__"): model.LabelValue("events_total")}
	ts2Metric := model.Metric{model.LabelName("__name__"): model.LabelValue("fake_name")}

	tests := []struct {
		input  *spanner.Row
		sample *prompb.Sample
		metric model.Metric
		fail   bool
	}{
		{
			input:  row1,
			sample: &prompb.Sample{Timestamp: 42, Value: 10.0},
			metric: ts1Metric,
		},
		{
			input:  row2,
			sample: &prompb.Sample{Timestamp: 10, Value: 11.1},
			metric: ts2Metric,
		},
		{
			input:  row3,
			sample: &prompb.Sample{Timestamp: 1562113285506, Value: 123.1},
			metric: ts2Metric,
		},
		{
			input:  badRow4,
			sample: nil,
			metric: model.Metric{},
			fail:   true,
		},
	}
	for i, tc := range tests {
		sample, metric, err := rowToSample(tc.input)
		if err != nil && !tc.fail {
			t.Errorf("rowToSample(*row %d*) returns error : %v when we expected none", i, err)
		}
		if err == nil && tc.fail {
			t.Errorf("rowToSample(*row %d*) does not return error when it should", i)
		}
		sampleDiff := pretty.Compare(tc.sample, sample)
		metricDiff := pretty.Compare(tc.metric, metric)
		if sampleDiff != "" || metricDiff != "" {
			// Cloud Spanner rows when printed are a bunch of hex
			t.Errorf("rowToSample(*row %d*) = %v,%v, want %v,%v", i, sample, metric, tc.sample, tc.metric)
		}
	}

}

func TestOrderTimeSeries(t *testing.T) {
	ts0 := &prompb.TimeSeries{
		Labels: []*prompb.Label{
			{Name: "__name__", Value: "events_total"},
		},
	}
	ts1 := &prompb.TimeSeries{
		Labels: []*prompb.Label{
			{Name: "__name__", Value: "fake_name"},
		},
	}

	tests := []struct {
		name  string
		input map[model.Fingerprint]*prompb.TimeSeries
		want  []*prompb.TimeSeries
	}{
		{
			name: "basic",
			input: map[model.Fingerprint]*prompb.TimeSeries{
				model.Metric{"__name__": "events_total"}.Fingerprint(): ts0,
				model.Metric{"__name__": "fake_name"}.Fingerprint():    ts1,
			},
			want: []*prompb.TimeSeries{ts1, ts0},
		},
	}
	for _, tc := range tests {
		got := orderTimeSeries(tc.input)
		if diff := pretty.Compare(got, tc.want); diff != "" {
			t.Errorf("test %s failed. orderTimeSeries(%v) = \n%v\n, wanted \n%v", tc.name, tc.input, got, tc.want)
		}
	}
}

func TestGenerateTimeSeriesExists(t *testing.T) {
	metric0 := model.Metric{model.LabelName("__name__"): model.LabelValue("events_total")}
	metric1 := model.Metric{model.LabelName("__name__"): model.LabelValue("fake_name")}
	tests := []struct {
		name   string
		input  []model.Metric
		want   string
		params map[string]interface{}
	}{
		{
			name:   "Empty",
			want:   "SELECT DISTINCT tsid FROM index WHERE tsid IN UNNEST(@array)",
			params: map[string]interface{}{"array": []int64{}},
		},
		{
			name: "Singleton",
			input: []model.Metric{
				metric0,
			},
			want:   "SELECT DISTINCT tsid FROM index WHERE tsid IN UNNEST(@array)",
			params: map[string]interface{}{"array": []int64{int64(metric0.Fingerprint())}},
		},
		{
			name: "Pair",
			input: []model.Metric{
				metric0,
				metric1,
			},
			want:   "SELECT DISTINCT tsid FROM index WHERE tsid IN UNNEST(@array)",
			params: map[string]interface{}{"array": []int64{int64(metric0.Fingerprint()), int64(metric1.Fingerprint())}},
		},
	}
	clean := strings.NewReplacer("\n", "", "\t", "", " ", "")
	for _, tc := range tests {
		got := generateTimeSeriesExists(tc.input)
		cleanGot := clean.Replace(got.SQL)
		cleanWant := clean.Replace(tc.want)
		if cleanGot != cleanWant {
			t.Errorf("Test %s failed. generateTimeSeriesExists(%v)=\n%s\n want \n%s", tc.name, tc.input, cleanGot, cleanWant)
		}
		if diff := pretty.Compare(got.Params, tc.params); diff != "" {
			t.Errorf("Test %s failed. generateTimeSeriesExists(%v) gives params %v, want %v", tc.name, tc.input, got.Params, tc.params)
		}
	}
}

func TestAddTimeSeries(t *testing.T) {
	metric0 := model.Metric{model.LabelName("__name__"): model.LabelValue("events_total")}
	tests := []struct {
		name      string
		input     []model.Metric
		lCache    map[model.Fingerprint]bool
		indexRows []*indexRow
		labelRows []*labelRow
	}{
		{
			name:   "empty",
			lCache: map[model.Fingerprint]bool{},
		},
		{
			name:   "singleton",
			input:  []model.Metric{metric0},
			lCache: map[model.Fingerprint]bool{},
			indexRows: []*indexRow{
				{TSID: int64(metric0.Fingerprint()), LID: int64(metric0.Fingerprint())},
			},
			labelRows: []*labelRow{
				{
					LID:   int64(metric0.Fingerprint()),
					Name:  "__name__",
					Value: "events_total",
				},
			},
		},
	}
	for _, tc := range tests {
		gotIndex, gotLabels := addTimeSeries(tc.input, tc.lCache)
		if diff := pretty.Compare(gotIndex, tc.indexRows); diff != "" {
			t.Errorf("Test %s failed. Got indexRows %v want %v", tc.name, gotIndex, tc.indexRows)
		}
		if diff := pretty.Compare(gotLabels, tc.labelRows); diff != "" {
			t.Errorf("Test %s failed. Got labelRows %v want %v", tc.name, gotLabels, tc.labelRows)
		}
	}
}

func TestApplyMutations(t *testing.T) {
	tests := []struct {
		name  string
		input []*spanner.Mutation
		want  []*spanner.Mutation
	}{
		{
			name: "Basic",
			input: []*spanner.Mutation{
				spanner.Insert("index", indexCols, []interface{}{42, 50}),
				spanner.Insert("index", indexCols, []interface{}{100, 200}),
			},
			want: []*spanner.Mutation{
				spanner.Insert("index", indexCols, []interface{}{42, 50}),
				spanner.Insert("index", indexCols, []interface{}{100, 200}),
			},
		},
	}
	for _, tc := range tests {
		d, s := createFakeDBSpanner()
		if err := d.applyMutations(context.Background(), tc.input, d.metrics.SpannerDuration["timeseries"]); err != nil {
			t.Errorf("test %s failed when it wasn't supposed to : applyMutations(%v) gives error %v", tc.name, tc.input, err)
		}
		if !cmp.Equal(tc.want, s.mutations, cmp.AllowUnexported(spanner.Mutation{})) {
			t.Errorf("test %s failed. d.applyMutations(%v) wrote %v, wanted %v", tc.name, tc.input, s.mutations, tc.want)
		}

	}
}

func TestUpdateIndexLabels(t *testing.T) {
	tests := []struct {
		name      string
		indexRows []*indexRow
		labelRows []*labelRow
		want      []*spanner.Mutation
	}{
		{
			name: "Basic",
			indexRows: []*indexRow{
				{LID: 40, TSID: 500},
				{LID: 42, TSID: 500},
				{LID: 50, TSID: 400},
			},
			labelRows: []*labelRow{
				{LID: 40, Name: "Hello", Value: "Good"},
				{LID: 42, Name: "test", Value: "value"},
				{LID: 50, Name: "Dairy", Value: "Cheese"},
			},
			want: []*spanner.Mutation{
				spanner.InsertOrUpdate("index", indexCols, []interface{}{int64(40), int64(500)}),
				spanner.InsertOrUpdate("index", indexCols, []interface{}{int64(42), int64(500)}),
				spanner.InsertOrUpdate("index", indexCols, []interface{}{int64(50), int64(400)}),
				spanner.InsertOrUpdate("labels", labelsCols, []interface{}{int64(40), "Hello", "Good"}),
				spanner.InsertOrUpdate("labels", labelsCols, []interface{}{int64(42), "test", "value"}),
				spanner.InsertOrUpdate("labels", labelsCols, []interface{}{int64(50), "Dairy", "Cheese"}),
			},
		},
	}
	for _, tc := range tests {
		d, s := createFakeDBSpanner()
		err := d.updateIndexLabels(context.Background(), tc.indexRows, tc.labelRows)
		if err != nil {
			t.Errorf("test %s failed when it wasn't supposed to : %v", tc.name, err)
		}
		if len(tc.want) != len(s.mutations) {
			t.Errorf("test %s failed. metricsWorker wrote %d samples, when it should have written %d", tc.name, len(s.mutations), len(tc.want))
			continue
		}
		for i := range tc.want {
			if !cmp.Equal(tc.want[i], s.mutations[i], cmp.AllowUnexported(spanner.Mutation{})) {
				t.Errorf("test %s failed. updateIndexLabels(%v, %v) wrote \n%v\n when it should have written \n%v", tc.name, tc.indexRows, tc.labelRows, s.mutations[i], tc.want[i])
			}
		}
	}
}

func TestMetricsWorker(t *testing.T) {
	metric0 := model.Metric{model.LabelName("__name__"): model.LabelValue("events_total")}
	metric1 := model.Metric{model.LabelName("__name__"): model.LabelValue("fake_name")}
	tests := []struct {
		name    string
		metrics []model.Metric
		want    []*spanner.Mutation
	}{
		{
			name:    "basic",
			metrics: []model.Metric{metric0, metric1},
			want: []*spanner.Mutation{
				spanner.Insert("index", indexCols, []interface{}{int64(metric0.Fingerprint()), int64(metric0.Fingerprint())}),
				spanner.Insert("index", indexCols, []interface{}{int64(metric1.Fingerprint()), int64(metric1.Fingerprint())}),
				spanner.Insert("labels", labelsCols, []interface{}{int64(metric0.Fingerprint()), "__name__", "events_total"}),
				spanner.Insert("labels", labelsCols, []interface{}{int64(metric1.Fingerprint()), "__name__", "fake_name"}),
			},
		},
	}
	for _, tc := range tests {
		continue
		d, s := createFakeDBSpanner()
		metricsWorker(context.Background(), d, tc.metrics)
		if len(tc.want) != len(s.mutations) {
			t.Errorf("test %s failed. metricsWorker wrote %d samples, when it should have written %d", tc.name, len(s.mutations), len(tc.want))
			continue
		}
		for i := range tc.want {
			if !cmp.Equal(tc.want[i], s.mutations[i], cmp.AllowUnexported(spanner.Mutation{})) {
				t.Errorf("test %s failed. metricsWorker wrote \n%v\n when it should have written \n%v", tc.name, s.mutations[i], tc.want[i])
			}
		}
	}
}

func TestSamplesWorker(t *testing.T) {
	samples := []*Sample{
		{
			Value:     4.0,
			Timestamp: 1234,
			TSID:      1000,
		},
		{
			Value:     42.0,
			Timestamp: 12334,
			TSID:      123,
		},
		{
			Value:     123.0,
			Timestamp: 1234,
			TSID:      500,
		},
	}
	tests := []struct {
		name    string
		samples []*Sample
		want    []*spanner.Mutation
	}{
		{
			name:    "Basic",
			samples: samples,
			want: []*spanner.Mutation{
				spanner.Insert("samples", []string{"timestamp", "value", "tsid"}, []interface{}{int64(12334), float64(42.0), int64(123)}),
				spanner.Insert("samples", []string{"timestamp", "value", "tsid"}, []interface{}{int64(1234), float64(123.0), int64(500)}),
				spanner.Insert("samples", []string{"timestamp", "value", "tsid"}, []interface{}{int64(1234), float64(4.0), int64(1000)}),
			},
		},
	}
	for _, tc := range tests {
		d, s := createFakeDBSpanner()
		samplesWorker(context.Background(), d, tc.samples)
		if len(tc.want) != len(s.mutations) {
			t.Errorf("test %s failed. sampleWorker wrote %d samples, when it should have written %d", tc.name, len(s.mutations), len(tc.samples))
			continue
		}
		for i := range tc.want {
			if !cmp.Equal(tc.want[i], s.mutations[i], cmp.AllowUnexported(spanner.Mutation{})) {
				t.Errorf("test %s failed. sampleWorker wrote \n%v\n when it should have written \n%v", tc.name, s.mutations[i], tc.want[i])
			}
		}
	}
}

func TestGenerateQuery(t *testing.T) {
	tests := []struct {
		name   string
		input  *prompb.Query
		want   string
		params map[string]interface{}
		fail   bool
	}{
		{
			name: "Invalid time",
			input: &prompb.Query{
				StartTimestampMs: 10,
				EndTimestampMs:   0,
			},
			fail: true,
		},
		{
			name: "Unrecognized matcher",
			input: &prompb.Query{
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  50,
						Name:  "IDoNotWork",
						Value: "AsAbove",
					},
				},
			},
			fail: true,
		},
		{
			name: "Invalid RE",
			input: &prompb.Query{
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  prompb.LabelMatcher_RE,
						Name:  "IDoNotWork",
						Value: "sel/\\",
					},
				},
			},
			fail: true,
		},
		{
			name: "Invalid NRE",
			input: &prompb.Query{
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  prompb.LabelMatcher_NRE,
						Name:  "IDoNotWork",
						Value: "sel/\\",
					},
				},
			},
			fail: true,
		},
		{
			name: "LabelMatcher_EQ NonEmpty",
			input: &prompb.Query{
				StartTimestampMs: 0,
				EndTimestampMs:   10,
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  prompb.LabelMatcher_EQ,
						Name:  "__name__",
						Value: "test_case",
					},
					{
						Type:  prompb.LabelMatcher_EQ,
						Name:  "monitor",
						Value: "my-monitor",
					},
				},
				Hints: &prompb.ReadHints{
					StepMs: 15000,
				},
			},
			want: `
	SELECT * FROM (
		SELECT l_names, l_values, timestamp, value FROM (
			SELECT tsid, ARRAY_AGG(name) AS l_names, ARRAY_AGG(value) AS l_values FROM (
				SELECT tsid, lid FROM (
					(SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name0 AND value=@value0)
					) USING (lid) INTERSECT ALL
					SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name1 AND value=@value1)
					) USING (lid))
				) JOIN index USING (tsid)
			) JOIN labels USING (lid) GROUP BY tsid
		) JOIN samples USING (tsid) WHERE timestamp <= @endMs AND timestamp >= @startMs
	) TABLESAMPLE RESERVOIR (@sampleCount ROWS)`,
			params: map[string]interface{}{
				"name0":       "__name__",
				"value0":      "test_case",
				"name1":       "monitor",
				"value1":      "my-monitor",
				"endMs":       10,
				"startMs":     0,
				"sampleCount": 10,
			},
		},
		{
			name: "LabelMatcher_EQ, LabelMatcher_NEQ Nonempty",
			input: &prompb.Query{
				StartTimestampMs: 0,
				EndTimestampMs:   1000,
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  prompb.LabelMatcher_EQ,
						Name:  "__name__",
						Value: "test_case",
					},
					{
						Type:  prompb.LabelMatcher_NEQ,
						Name:  "monitor",
						Value: "my-monitor",
					},
				},
				Hints: &prompb.ReadHints{
					StepMs: 100,
				},
			},
			want: `
	SELECT * FROM (
		SELECT l_names, l_values, timestamp, value FROM (
			SELECT tsid, ARRAY_AGG(name) AS l_names, ARRAY_AGG(value) AS l_values FROM (
				SELECT tsid, lid FROM (
					(SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name0 AND value=@value0)
						) USING (lid))
					EXCEPT ALL (
					SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name1 AND value=@value1)
					) USING(lid))
				) JOIN index USING(tsid)
			) JOIN labels USING(lid) GROUP BY tsid
		) JOIN samples USING(tsid) WHERE timestamp <= @endMs AND timestamp >= @startMs
	) TABLESAMPLE RESERVOIR (@sampleCount ROWS)`,
			params: map[string]interface{}{
				"name0":       "__name__",
				"value0":      "test_case",
				"name1":       "monitor",
				"value1":      "my-monitor",
				"endMs":       1000,
				"startMs":     0,
				"sampleCount": 10,
			},
		},
		{
			name: "LabelMatcher_RE Nonempty LabelMatcher_EQ Empty LabelMatcher_RE Empty",
			input: &prompb.Query{
				StartTimestampMs: 0,
				EndTimestampMs:   1234,
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  prompb.LabelMatcher_RE,
						Name:  "job",
						Value: "go.*",
					},
					{
						Type:  prompb.LabelMatcher_EQ,
						Name:  "monitor",
						Value: "",
					},
					{
						Type:  prompb.LabelMatcher_RE,
						Name:  "instance",
						Value: ".*",
					},
				},
				Hints: &prompb.ReadHints{},
			},
			want: `
	SELECT * FROM (
		SELECT l_names, l_values, timestamp, value FROM (
			SELECT tsid, ARRAY_AGG(name) AS l_names, ARRAY_AGG(value) AS l_values FROM (
				SELECT tsid, lid FROM (
					(SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name0 AND REGEXP_CONTAINS(value, ^(@value0)$))
						) USING (lid))
					EXCEPT ALL (
					SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name1)
					) USING(lid) INTERSECT ALL
					SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name2)
					) USING(lid))
				) JOIN index USING(tsid)
			) JOIN labels USING(lid) GROUP BY tsid
		) JOIN samples USING(tsid) WHERE timestamp <= @endMs AND timestamp >= @startMs
	) TABLESAMPLE RESERVOIR (@sampleCount ROWS)`,
			params: map[string]interface{}{
				"name0":       "job",
				"value0":      "go.*",
				"name1":       "monitor",
				"value1":      "",
				"name2":       "instance",
				"value2":      ".*",
				"endMs":       1234,
				"startMs":     0,
				"sampleCount": 1234,
			},
		},
		{
			name: "LabelMatcher_RE Nonempty LabelMatcher_NEQ Empty LabelMatcher_NRE Nonempty",
			input: &prompb.Query{
				StartTimestampMs: 0,
				EndTimestampMs:   1234,
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  prompb.LabelMatcher_RE,
						Name:  "job",
						Value: "go.*",
					},
					{
						Type:  prompb.LabelMatcher_NEQ,
						Name:  "monitor",
						Value: "",
					},
					{
						Type:  prompb.LabelMatcher_NRE,
						Name:  "__name__",
						Value: "kubernetes.*",
					},
				},
				Hints: &prompb.ReadHints{},
			},
			want: `
	SELECT * FROM (
		SELECT l_names, l_values, timestamp, value FROM (
			SELECT tsid, ARRAY_AGG(name) AS l_names, ARRAY_AGG(value) AS l_values FROM (
				SELECT tsid, lid FROM (
					(SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name0 AND REGEXP_CONTAINS(value, ^(@value0)$))
					) USING (lid) INTERSECT ALL
					SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name1)
					) USING (lid))
					EXCEPT ALL (
					SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name2 AND REGEXP_CONTAINS(value, ^(@value2)$))
					) USING(lid))
				) JOIN index USING(tsid)
			) JOIN labels USING(lid) GROUP BY tsid
		) JOIN samples USING(tsid) WHERE timestamp <= @endMs AND timestamp >= @startMs
	) TABLESAMPLE RESERVOIR (@sampleCount ROWS)`,
			params: map[string]interface{}{
				"name0":       "job",
				"value0":      "go.*",
				"name1":       "monitor",
				"value1":      "",
				"name2":       "__name__",
				"value2":      "kubernetes.*",
				"endMs":       1234,
				"startMs":     0,
				"sampleCount": 1234,
			},
		},
		{
			name: "LabelMatcher_NRE Empty",
			input: &prompb.Query{
				StartTimestampMs: 0,
				EndTimestampMs:   1234,
				Matchers: []*prompb.LabelMatcher{
					{
						Type:  prompb.LabelMatcher_NRE,
						Name:  "job",
						Value: ".*",
					},
				},
				Hints: &prompb.ReadHints{},
			},
			want: `
	SELECT * FROM (
		SELECT l_names, l_values, timestamp, value FROM (
			SELECT tsid, ARRAY_AGG(name) AS l_names, ARRAY_AGG(value) AS l_values FROM (
				SELECT tsid, lid FROM (
					(SELECT tsid FROM index JOIN (
						SELECT lid FROM labels WHERE
							(name=@name0)
					) USING (lid))
				) JOIN index USING(tsid)
			) JOIN labels USING(lid) GROUP BY tsid
		) JOIN samples USING(tsid) WHERE timestamp <= @endMs AND timestamp >= @startMs
	) TABLESAMPLE RESERVOIR (@sampleCount ROWS)`,
			params: map[string]interface{}{
				"name0":       "job",
				"value0":      ".*",
				"endMs":       1234,
				"startMs":     0,
				"sampleCount": 1234,
			},
		},
	}
	clean := strings.NewReplacer("\n", "", "\t", "", " ", "")
	for _, tc := range tests {
		got, err := generateQuery(tc.input)
		if tc.fail {
			if err == nil {
				t.Errorf("generateQuery(%v) does not return error when it should", tc.input)
			}
			continue
		}
		if err != nil && !tc.fail {
			t.Errorf("Test %s failed. generateQuery(%v) gave error %v. Expected \n%s", tc.name, tc.input, err, tc.want)
			continue
		}
		cleanGot := clean.Replace(got.SQL)
		cleanWant := clean.Replace(tc.want)
		if cleanGot != cleanWant {
			t.Errorf("Test %s failed. generateQuery(%v)=\n%s\n want \n%s", tc.name, tc.input, cleanGot, cleanWant)
		}
		if diff := pretty.Compare(got.Params, tc.params); diff != "" {
			t.Errorf("Test %s failed. generateQuery(%v) gives params \n%v\n, want \n%v", tc.name, tc.input, got.Params, tc.params)
		}
	}
	return
}
