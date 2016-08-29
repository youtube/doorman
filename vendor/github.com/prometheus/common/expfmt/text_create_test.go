// Copyright 2014 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package expfmt

import (
	"bytes"
	"math"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func testCreate(t testing.TB) {
	var scenarios = []struct {
		in  *dto.MetricFamily
		out string
	}{
		// 0: Counter, NaN as value, timestamp given.
		{
			in: &dto.MetricFamily{
				Name: "name",
				Help: "two-line\n doc  str\\ing",
				Type: dto.MetricType_COUNTER,
				Metric: []*dto.Metric{
					&dto.Metric{
						Label: []*dto.LabelPair{
							&dto.LabelPair{
								Name:  "labelname",
								Value: "val1",
							},
							&dto.LabelPair{
								Name:  "basename",
								Value: "basevalue",
							},
						},
						Counter: &dto.Counter{
							Value: math.NaN(),
						},
					},
					&dto.Metric{
						Label: []*dto.LabelPair{
							&dto.LabelPair{
								Name:  "labelname",
								Value: "val2",
							},
							&dto.LabelPair{
								Name:  "basename",
								Value: "basevalue",
							},
						},
						Counter: &dto.Counter{
							Value: .23,
						},
						TimestampMs: 1234567890,
					},
				},
			},
			out: `# HELP name two-line\n doc  str\\ing
# TYPE name counter
name{labelname="val1",basename="basevalue"} NaN
name{labelname="val2",basename="basevalue"} 0.23 1234567890
`,
		},
		// 1: Gauge, some escaping required, +Inf as value, multi-byte characters in label values.
		{
			in: &dto.MetricFamily{
				Name: "gauge_name",
				Help: "gauge\ndoc\nstr\"ing",
				Type: dto.MetricType_GAUGE,
				Metric: []*dto.Metric{
					&dto.Metric{
						Label: []*dto.LabelPair{
							&dto.LabelPair{
								Name:  "name_1",
								Value: "val with\nnew line",
							},
							&dto.LabelPair{
								Name:  "name_2",
								Value: "val with \\backslash and \"quotes\"",
							},
						},
						Gauge: &dto.Gauge{
							Value: math.Inf(+1),
						},
					},
					&dto.Metric{
						Label: []*dto.LabelPair{
							&dto.LabelPair{
								Name:  "name_1",
								Value: "Björn",
							},
							&dto.LabelPair{
								Name:  "name_2",
								Value: "佖佥",
							},
						},
						Gauge: &dto.Gauge{
							Value: 3.14E42,
						},
					},
				},
			},
			out: `# HELP gauge_name gauge\ndoc\nstr"ing
# TYPE gauge_name gauge
gauge_name{name_1="val with\nnew line",name_2="val with \\backslash and \"quotes\""} +Inf
gauge_name{name_1="Björn",name_2="佖佥"} 3.14e+42
`,
		},
		// 2: Untyped, no help, one sample with no labels and -Inf as value, another sample with one label.
		{
			in: &dto.MetricFamily{
				Name: "untyped_name",
				Type: dto.MetricType_UNTYPED,
				Metric: []*dto.Metric{
					&dto.Metric{
						Untyped: &dto.Untyped{
							Value: math.Inf(-1),
						},
					},
					&dto.Metric{
						Label: []*dto.LabelPair{
							&dto.LabelPair{
								Name:  "name_1",
								Value: "value 1",
							},
						},
						Untyped: &dto.Untyped{
							Value: -1.23e-45,
						},
					},
				},
			},
			out: `# TYPE untyped_name untyped
untyped_name -Inf
untyped_name{name_1="value 1"} -1.23e-45
`,
		},
		// 3: Summary.
		{
			in: &dto.MetricFamily{
				Name: "summary_name",
				Help: "summary docstring",
				Type: dto.MetricType_SUMMARY,
				Metric: []*dto.Metric{
					&dto.Metric{
						Summary: &dto.Summary{
							SampleCount: 42,
							SampleSum:   -3.4567,
							Quantile: []*dto.Quantile{
								&dto.Quantile{
									Quantile: 0.5,
									Value:    -1.23,
								},
								&dto.Quantile{
									Quantile: 0.9,
									Value:    .2342354,
								},
								&dto.Quantile{
									Quantile: 0.99,
									Value:    0,
								},
							},
						},
					},
					&dto.Metric{
						Label: []*dto.LabelPair{
							&dto.LabelPair{
								Name:  "name_1",
								Value: "value 1",
							},
							&dto.LabelPair{
								Name:  "name_2",
								Value: "value 2",
							},
						},
						Summary: &dto.Summary{
							SampleCount: 4711,
							SampleSum:   2010.1971,
							Quantile: []*dto.Quantile{
								&dto.Quantile{
									Quantile: 0.5,
									Value:    1,
								},
								&dto.Quantile{
									Quantile: 0.9,
									Value:    2,
								},
								&dto.Quantile{
									Quantile: 0.99,
									Value:    3,
								},
							},
						},
					},
				},
			},
			out: `# HELP summary_name summary docstring
# TYPE summary_name summary
summary_name{quantile="0.5"} -1.23
summary_name{quantile="0.9"} 0.2342354
summary_name{quantile="0.99"} 0
summary_name_sum -3.4567
summary_name_count 42
summary_name{name_1="value 1",name_2="value 2",quantile="0.5"} 1
summary_name{name_1="value 1",name_2="value 2",quantile="0.9"} 2
summary_name{name_1="value 1",name_2="value 2",quantile="0.99"} 3
summary_name_sum{name_1="value 1",name_2="value 2"} 2010.1971
summary_name_count{name_1="value 1",name_2="value 2"} 4711
`,
		},
		// 4: Histogram
		{
			in: &dto.MetricFamily{
				Name: "request_duration_microseconds",
				Help: "The response latency.",
				Type: dto.MetricType_HISTOGRAM,
				Metric: []*dto.Metric{
					&dto.Metric{
						Histogram: &dto.Histogram{
							SampleCount: 2693,
							SampleSum:   1756047.3,
							Bucket: []*dto.Bucket{
								&dto.Bucket{
									UpperBound:      100,
									CumulativeCount: 123,
								},
								&dto.Bucket{
									UpperBound:      120,
									CumulativeCount: 412,
								},
								&dto.Bucket{
									UpperBound:      144,
									CumulativeCount: 592,
								},
								&dto.Bucket{
									UpperBound:      172.8,
									CumulativeCount: 1524,
								},
								&dto.Bucket{
									UpperBound:      math.Inf(+1),
									CumulativeCount: 2693,
								},
							},
						},
					},
				},
			},
			out: `# HELP request_duration_microseconds The response latency.
# TYPE request_duration_microseconds histogram
request_duration_microseconds_bucket{le="100"} 123
request_duration_microseconds_bucket{le="120"} 412
request_duration_microseconds_bucket{le="144"} 592
request_duration_microseconds_bucket{le="172.8"} 1524
request_duration_microseconds_bucket{le="+Inf"} 2693
request_duration_microseconds_sum 1.7560473e+06
request_duration_microseconds_count 2693
`,
		},
		// 5: Histogram with missing +Inf bucket.
		{
			in: &dto.MetricFamily{
				Name: "request_duration_microseconds",
				Help: "The response latency.",
				Type: dto.MetricType_HISTOGRAM,
				Metric: []*dto.Metric{
					&dto.Metric{
						Histogram: &dto.Histogram{
							SampleCount: 2693,
							SampleSum:   1756047.3,
							Bucket: []*dto.Bucket{
								&dto.Bucket{
									UpperBound:      100,
									CumulativeCount: 123,
								},
								&dto.Bucket{
									UpperBound:      120,
									CumulativeCount: 412,
								},
								&dto.Bucket{
									UpperBound:      144,
									CumulativeCount: 592,
								},
								&dto.Bucket{
									UpperBound:      172.8,
									CumulativeCount: 1524,
								},
							},
						},
					},
				},
			},
			out: `# HELP request_duration_microseconds The response latency.
# TYPE request_duration_microseconds histogram
request_duration_microseconds_bucket{le="100"} 123
request_duration_microseconds_bucket{le="120"} 412
request_duration_microseconds_bucket{le="144"} 592
request_duration_microseconds_bucket{le="172.8"} 1524
request_duration_microseconds_bucket{le="+Inf"} 2693
request_duration_microseconds_sum 1.7560473e+06
request_duration_microseconds_count 2693
`,
		},
		// 6: No metric type, should result in default type Counter.
		{
			in: &dto.MetricFamily{
				Name: "name",
				Help: "doc string",
				Metric: []*dto.Metric{
					&dto.Metric{
						Counter: &dto.Counter{
							Value: math.Inf(-1),
						},
					},
				},
			},
			out: `# HELP name doc string
# TYPE name counter
name -Inf
`,
		},
	}

	for i, scenario := range scenarios {
		out := bytes.NewBuffer(make([]byte, 0, len(scenario.out)))
		n, err := MetricFamilyToText(out, scenario.in)
		if err != nil {
			t.Errorf("%d. error: %s", i, err)
			continue
		}
		if expected, got := len(scenario.out), n; expected != got {
			t.Errorf(
				"%d. expected %d bytes written, got %d",
				i, expected, got,
			)
		}
		if expected, got := scenario.out, out.String(); expected != got {
			t.Errorf(
				"%d. expected out=%q, got %q",
				i, expected, got,
			)
		}
	}

}

func TestCreate(t *testing.T) {
	testCreate(t)
}

func BenchmarkCreate(b *testing.B) {
	for i := 0; i < b.N; i++ {
		testCreate(b)
	}
}

func testCreateError(t testing.TB) {
	var scenarios = []struct {
		in  *dto.MetricFamily
		err string
	}{
		// 0: No metric.
		{
			in: &dto.MetricFamily{
				Name:   "name",
				Help:   "doc string",
				Type:   dto.MetricType_COUNTER,
				Metric: []*dto.Metric{},
			},
			err: "MetricFamily has no metrics",
		},
		// 1: No metric name.
		{
			in: &dto.MetricFamily{
				Help: "doc string",
				Type: dto.MetricType_UNTYPED,
				Metric: []*dto.Metric{
					&dto.Metric{
						Untyped: &dto.Untyped{
							Value: math.Inf(-1),
						},
					},
				},
			},
			err: "MetricFamily has no name",
		},
		// 2: Wrong type.
		{
			in: &dto.MetricFamily{
				Name: "name",
				Help: "doc string",
				Type: dto.MetricType_COUNTER,
				Metric: []*dto.Metric{
					&dto.Metric{
						Untyped: &dto.Untyped{
							Value: math.Inf(-1),
						},
					},
				},
			},
			err: "expected counter in metric",
		},
	}

	for i, scenario := range scenarios {
		var out bytes.Buffer
		_, err := MetricFamilyToText(&out, scenario.in)
		if err == nil {
			t.Errorf("%d. expected error, got nil", i)
			continue
		}
		if expected, got := scenario.err, err.Error(); strings.Index(got, expected) != 0 {
			t.Errorf(
				"%d. expected error starting with %q, got %q",
				i, expected, got,
			)
		}
	}

}

func TestCreateError(t *testing.T) {
	testCreateError(t)
}

func BenchmarkCreateError(b *testing.B) {
	for i := 0; i < b.N; i++ {
		testCreateError(b)
	}
}
