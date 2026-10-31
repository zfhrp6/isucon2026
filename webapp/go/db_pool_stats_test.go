package isureal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

type sampleSignalWriter struct {
	mu      sync.Mutex
	output  bytes.Buffer
	samples chan struct{}
}

func (w *sampleSignalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.output.Write(p)
	w.mu.Unlock()
	select {
	case w.samples <- struct{}{}:
	default:
	}
	return n, err
}

func (w *sampleSignalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}

func TestLogDBPoolStatsRecordsCountersAndDeltas(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	previousAt := time.Date(2026, time.October, 31, 0, 0, 0, 0, time.UTC)
	previous := poolCounters{
		maxIdleClosed:     2,
		maxIdleTimeClosed: 3,
		maxLifetimeClosed: 4,
		waitCount:         5,
		waitDuration:      6 * time.Millisecond,
	}
	stats := sql.DBStats{
		OpenConnections:    9,
		InUse:              7,
		Idle:               2,
		MaxOpenConnections: 30,
		MaxIdleClosed:      8,
		MaxIdleTimeClosed:  10,
		MaxLifetimeClosed:  13,
		WaitCount:          16,
		WaitDuration:       20 * time.Millisecond,
	}

	logDBPoolStats(logger, "asia", previousAt, previous, previousAt.Add(10*time.Second), stats)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]float64{
		"open_connections":           9,
		"in_use":                     7,
		"idle":                       2,
		"max_open_connections":       30,
		"max_idle_closed_total":      8,
		"max_idle_closed_delta":      6,
		"max_idle_time_closed_total": 10,
		"max_idle_time_closed_delta": 7,
		"max_lifetime_closed_total":  13,
		"max_lifetime_closed_delta":  9,
		"wait_count_total":           16,
		"wait_count_delta":           11,
		"wait_duration_ms_total":     20,
		"wait_duration_ms_delta":     14,
		"interval_ms":                10000,
	} {
		if got, ok := record[key].(float64); !ok || got != want {
			t.Errorf("%s = %v, want %v", key, record[key], want)
		}
	}
	if record["region"] != "asia" {
		t.Errorf("region = %v, want asia", record["region"])
	}
	if _, ok := record["time"]; !ok {
		t.Fatal("JSON log is missing its timestamp")
	}
	if _, ok := record["sample_time"]; !ok {
		t.Fatal("JSON log is missing the pool sample time")
	}
	if strings.Count(output.String(), `"time"`) != 1 {
		t.Fatalf("JSON log must contain one timestamp key: %s", output.String())
	}
}

func TestDBPoolStatsMonitorStopsWithContext(t *testing.T) {
	output := &sampleSignalWriter{samples: make(chan struct{}, 4)}
	logger := slog.New(slog.NewJSONHandler(output, nil))
	db, err := sqlx.Open("pgx", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	stop := startDBPoolStatsAtInterval(ctx, db, logger, "asia", 5*time.Millisecond)
	for range 2 {
		select {
		case <-output.samples:
		case <-time.After(time.Second):
			cancel()
			stop()
			t.Fatal("timed out waiting for pool statistics sample")
		}
	}
	cancel()
	stop()
	if count := strings.Count(output.String(), `"msg":"db_pool_stats"`); count < 2 {
		t.Fatalf("monitor emitted %d samples before stop, want at least 2", count)
	}
}
