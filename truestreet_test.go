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
	"testing"

	"github.com/google/truestreet/database"

	"github.com/kylelemons/godebug/pretty"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/prompb"
)

func TestProtoToTimeseries(t *testing.T) {
	metric := model.Metric{"Type": "Test", "Foo": "Bar"}
	metric2 := model.Metric{"Animal": "Cow", "Type": "Drill"}
	fp1 := metric.Fingerprint()
	fp2 := metric2.Fingerprint()

	tests := []struct {
		input *prompb.WriteRequest
		want  *database.TimeSeries
	}{
		{
			input: &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "Type", Value: "Test"},
							{Name: "Foo", Value: "Bar"},
						},
						Samples: []prompb.Sample{
							{Value: 42.1, Timestamp: 1234},
							{Value: 10.5, Timestamp: 1760},
							{Value: 55.5, Timestamp: 5280},
						},
					},
				},
			},
			want: &database.TimeSeries{
				Metrics: []model.Metric{metric},
				Samples: []*database.Sample{
					{Value: 42.1, Timestamp: 1234, TSID: fp1},
					{Value: 10.5, Timestamp: 1760, TSID: fp1},
					{Value: 55.5, Timestamp: 5280, TSID: fp1},
				},
			},
		},
		{
			input: &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "Type", Value: "Test"},
							{Name: "Foo", Value: "Bar"},
						},
						Samples: []prompb.Sample{
							{Value: 42.1, Timestamp: 1234},
							{Value: 10.5, Timestamp: 1760},
							{Value: 55.5, Timestamp: 5280},
						},
					},
					{
						Labels: []prompb.Label{
							{Name: "Animal", Value: "Cow"},
							{Name: "Type", Value: "Drill"},
						},
						Samples: []prompb.Sample{
							{Value: 10.1, Timestamp: 1},
							{Value: 5.0, Timestamp: 10},
							{Value: 10.2, Timestamp: 42},
						},
					},
				},
			},
			want: &database.TimeSeries{
				Metrics: []model.Metric{metric, metric2},
				Samples: []*database.Sample{
					{Value: 42.1, Timestamp: 1234, TSID: fp1},
					{Value: 10.5, Timestamp: 1760, TSID: fp1},
					{Value: 55.5, Timestamp: 5280, TSID: fp1},
					{Value: 10.1, Timestamp: 1, TSID: fp2},
					{Value: 5.0, Timestamp: 10, TSID: fp2},
					{Value: 10.2, Timestamp: 42, TSID: fp2},
				},
			},
		},
	}

	for _, tc := range tests {
		got, err := protoToTimeseries(tc.input)
		if diff := pretty.Compare(tc.want, got); diff != "" || err != nil {
			t.Errorf("protoToTimeseries(%v) = %v, %v; want %v, nil", tc.input, got, err, tc.want)
		}
	}

}

func TestProtoToSamplesErrors(t *testing.T) {
	tests := []*prompb.WriteRequest{
		nil,
	}
	for _, tc := range tests {
		got, err := protoToTimeseries(tc)
		if err == nil {
			t.Errorf("protoToTimeseries(%v) = %v, %v; want _, error", tc, got, err)
		}
	}
}
