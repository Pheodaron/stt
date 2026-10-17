package logger

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"vks-stt-v2/internal/config"
)

func TestProductionLogsUseJSONAndFilterDebug(t *testing.T) {
	output := captureLogs(t, config.Config{})
	var record struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
		Workers int    `json:"workers"`
	}
	if err := json.Unmarshal([]byte(output), &record); err != nil {
		t.Fatalf("expected one JSON record, got %q: %v", output, err)
	}
	if record.Level != "INFO" || record.Message != "info probe" || record.Workers != 2 {
		t.Fatalf("unexpected log record: %+v", record)
	}
}

func TestDevelopmentLogsUseTextAndIncludeDebug(t *testing.T) {
	output := captureLogs(t, config.Config{IsDev: true})
	if !strings.Contains(output, "debug probe") || !strings.Contains(output, "info probe") {
		t.Fatalf("missing development log messages: %q", output)
	}
	if json.Valid([]byte(output)) {
		t.Fatalf("expected text logs, got JSON: %q", output)
	}
}

func captureLogs(t *testing.T, cfg config.Config) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	previousStderr, previousLogger := os.Stderr, slog.Default()
	t.Cleanup(func() {
		os.Stderr = previousStderr
		slog.SetDefault(previousLogger)
		file.Close()
	})
	os.Stderr = file
	Init(cfg)
	slog.Debug("debug probe")
	slog.Info("info probe", "workers", 2)
	output, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}
