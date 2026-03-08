package otel

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// Receiver handles incoming OTLP HTTP requests and stores data in SQLite.
type Receiver struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewReceiver(db *sql.DB, logger *slog.Logger) *Receiver {
	return &Receiver{
		db:     db,
		logger: logger.With("component", "otel_receiver"),
	}
}

// HandleMetrics handles POST /v1/metrics (OTLP HTTP protobuf).
func (r *Receiver) HandleMetrics(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		r.logger.Error("read metrics body", "error", err)
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	var exportReq collectormetricsv1.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &exportReq); err != nil {
		r.logger.Error("unmarshal metrics", "error", err)
		http.Error(w, "unmarshal failed", http.StatusBadRequest)
		return
	}

	count := 0
	for _, rm := range exportReq.ResourceMetrics {
		resourceAttrs := extractAttributes(rm.Resource)

		// Extract session_id from resource attributes
		sessionID := ""
		if rm.Resource != nil {
			for _, kv := range rm.Resource.Attributes {
				if kv.Key == "session.id" {
					sessionID = kv.Value.GetStringValue()
				}
			}
		}

		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				dataPoints := extractDataPoints(m)
				for _, dp := range dataPoints {
					attrsJSON, _ := json.Marshal(dp.attributes)
					resJSON, _ := json.Marshal(resourceAttrs)
					ts := time.Unix(0, int64(dp.timeUnixNano))
					_, err := r.db.Exec(
						`INSERT INTO otel_metrics (name, value, attributes, resource_attributes, session_id, timestamp)
						 VALUES (?, ?, ?, ?, ?, ?)`,
						m.Name, dp.value, string(attrsJSON), string(resJSON), sessionID, ts,
					)
					if err != nil {
						r.logger.Error("insert metric", "error", err, "name", m.Name)
					} else {
						count++
					}
				}
			}
		}
	}

	r.logger.Debug("received metrics", "count", count)

	resp := &collectormetricsv1.ExportMetricsServiceResponse{}
	respBytes, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)
}

// HandleLogs handles POST /v1/logs (OTLP HTTP protobuf).
func (r *Receiver) HandleLogs(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		r.logger.Error("read logs body", "error", err)
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	var exportReq collectorlogsv1.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &exportReq); err != nil {
		r.logger.Error("unmarshal logs", "error", err)
		http.Error(w, "unmarshal failed", http.StatusBadRequest)
		return
	}

	count := 0
	for _, rl := range exportReq.ResourceLogs {
		resourceAttrs := extractAttributes(rl.Resource)

		sessionID := ""
		if rl.Resource != nil {
			for _, kv := range rl.Resource.Attributes {
				if kv.Key == "session.id" {
					sessionID = kv.Value.GetStringValue()
				}
			}
		}

		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				attrs := kvListToMap(lr.Attributes)

				// Use event name from attributes or body
				eventName := ""
				if v, ok := attrs["event.name"]; ok {
					eventName = v
				}

				bodyStr := ""
				if lr.Body != nil {
					bodyStr = lr.Body.GetStringValue()
					if bodyStr == "" {
						// Try to serialize as JSON if it's a complex type
						if kvList := lr.Body.GetKvlistValue(); kvList != nil {
							bodyMap := make(map[string]string)
							for _, kv := range kvList.Values {
								bodyMap[kv.Key] = kv.Value.GetStringValue()
							}
							b, _ := json.Marshal(bodyMap)
							bodyStr = string(b)
						}
					}
				}

				// Fallback: extract session.id from log record attributes if not in resource
				logSessionID := sessionID
				if logSessionID == "" {
					if v, ok := attrs["session.id"]; ok {
						logSessionID = v
					}
				}

				attrsJSON, _ := json.Marshal(attrs)
				resJSON, _ := json.Marshal(resourceAttrs)
				ts := time.Unix(0, int64(lr.TimeUnixNano))

				_, err := r.db.Exec(
					`INSERT INTO otel_events (name, body, attributes, resource_attributes, session_id, timestamp)
					 VALUES (?, ?, ?, ?, ?, ?)`,
					eventName, bodyStr, string(attrsJSON), string(resJSON), logSessionID, ts,
				)
				if err != nil {
					r.logger.Error("insert event", "error", err, "name", eventName)
				} else {
					count++
				}
			}
		}
	}

	r.logger.Debug("received logs", "count", count)

	resp := &collectorlogsv1.ExportLogsServiceResponse{}
	respBytes, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)
}
