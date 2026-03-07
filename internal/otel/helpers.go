package otel

import (
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
)

type dataPoint struct {
	value        float64
	timeUnixNano uint64
	attributes   map[string]string
}

func extractDataPoints(m *metricsv1.Metric) []dataPoint {
	var points []dataPoint

	switch d := m.Data.(type) {
	case *metricsv1.Metric_Sum:
		for _, dp := range d.Sum.DataPoints {
			points = append(points, dataPoint{
				value:        numberValue(dp),
				timeUnixNano: dp.TimeUnixNano,
				attributes:   kvListToMap(dp.Attributes),
			})
		}
	case *metricsv1.Metric_Gauge:
		for _, dp := range d.Gauge.DataPoints {
			points = append(points, dataPoint{
				value:        numberValue(dp),
				timeUnixNano: dp.TimeUnixNano,
				attributes:   kvListToMap(dp.Attributes),
			})
		}
	case *metricsv1.Metric_Histogram:
		for _, dp := range d.Histogram.DataPoints {
			sum := 0.0
			if dp.Sum != nil {
				sum = *dp.Sum
			}
			points = append(points, dataPoint{
				value:        sum,
				timeUnixNano: dp.TimeUnixNano,
				attributes:   kvListToMap(dp.Attributes),
			})
		}
	}

	return points
}

func numberValue(dp *metricsv1.NumberDataPoint) float64 {
	switch v := dp.Value.(type) {
	case *metricsv1.NumberDataPoint_AsDouble:
		return v.AsDouble
	case *metricsv1.NumberDataPoint_AsInt:
		return float64(v.AsInt)
	}
	return 0
}

func kvListToMap(attrs []*commonv1.KeyValue) map[string]string {
	m := make(map[string]string)
	for _, kv := range attrs {
		if kv.Value != nil {
			m[kv.Key] = kv.Value.GetStringValue()
		}
	}
	return m
}

func extractAttributes(res *resourcev1.Resource) map[string]string {
	if res == nil {
		return map[string]string{}
	}
	return kvListToMap(res.Attributes)
}
