package session

import (
	"fmt"
	"strings"

	"github.com/myuon/agmux/internal/config"
)

// appendOTelEnv appends OpenTelemetry environment variables to enable
// Claude Code telemetry collection by agmux's built-in OTLP receiver.
func appendOTelEnv(env []string) []string {
	cfg, err := config.Load()
	if err != nil {
		return env
	}

	port := cfg.Server.Port
	if port == 0 {
		port = 4321
	}

	otelVars := map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":      "1",
		"OTEL_METRICS_EXPORTER":             "otlp",
		"OTEL_LOGS_EXPORTER":                "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":       "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT":       fmt.Sprintf("http://localhost:%d", port),
		"OTEL_METRIC_EXPORT_INTERVAL":        "30000",
		"OTEL_LOGS_EXPORT_INTERVAL":          "5000",
	}

	// Don't override if already set
	existing := make(map[string]bool)
	for _, e := range env {
		key := strings.SplitN(e, "=", 2)[0]
		existing[key] = true
	}

	for k, v := range otelVars {
		if !existing[k] {
			env = append(env, k+"="+v)
		}
	}

	return env
}

// otelEnvPrefix returns a shell env prefix string for terminal mode.
// e.g. "CLAUDE_CODE_ENABLE_TELEMETRY=1 OTEL_METRICS_EXPORTER=otlp ... "
func otelEnvPrefix(apiPort int) string {
	port := apiPort
	if port == 0 {
		port = 4321
	}

	return fmt.Sprintf(
		"CLAUDE_CODE_ENABLE_TELEMETRY=1 OTEL_METRICS_EXPORTER=otlp OTEL_LOGS_EXPORTER=otlp OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:%d ",
		port,
	)
}
