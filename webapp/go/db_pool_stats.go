package isureal

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

const dbPoolStatsInterval = 10 * time.Second

type poolCounters struct {
	maxIdleClosed     int64
	maxIdleTimeClosed int64
	maxLifetimeClosed int64
	waitCount         int64
	waitDuration      time.Duration
}

func countersFromStats(stats sql.DBStats) poolCounters {
	return poolCounters{
		maxIdleClosed:     stats.MaxIdleClosed,
		maxIdleTimeClosed: stats.MaxIdleTimeClosed,
		maxLifetimeClosed: stats.MaxLifetimeClosed,
		waitCount:         stats.WaitCount,
		waitDuration:      stats.WaitDuration,
	}
}

func logDBPoolStats(logger *slog.Logger, region string, previousAt time.Time, previous poolCounters, now time.Time, stats sql.DBStats) poolCounters {
	counters := countersFromStats(stats)
	interval := now.Sub(previousAt)
	logger.Info("db_pool_stats",
		"sample_time", now.UTC().Format(time.RFC3339Nano),
		"region", region,
		"interval_ms", interval.Milliseconds(),
		"open_connections", stats.OpenConnections,
		"in_use", stats.InUse,
		"idle", stats.Idle,
		"max_open_connections", stats.MaxOpenConnections,
		"max_idle_closed_total", counters.maxIdleClosed,
		"max_idle_closed_delta", counters.maxIdleClosed-previous.maxIdleClosed,
		"max_idle_time_closed_total", counters.maxIdleTimeClosed,
		"max_idle_time_closed_delta", counters.maxIdleTimeClosed-previous.maxIdleTimeClosed,
		"max_lifetime_closed_total", counters.maxLifetimeClosed,
		"max_lifetime_closed_delta", counters.maxLifetimeClosed-previous.maxLifetimeClosed,
		"wait_count_total", counters.waitCount,
		"wait_count_delta", counters.waitCount-previous.waitCount,
		"wait_duration_ms_total", counters.waitDuration.Milliseconds(),
		"wait_duration_ms_delta", (counters.waitDuration - previous.waitDuration).Milliseconds(),
	)
	return counters
}

func startDBPoolStats(parent context.Context, db *sqlx.DB, logger *slog.Logger, region string) func() {
	return startDBPoolStatsAtInterval(parent, db, logger, region, dbPoolStatsInterval)
}

func startDBPoolStatsAtInterval(parent context.Context, db *sqlx.DB, logger *slog.Logger, region string, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	previousAt := time.Now()
	previous := logDBPoolStats(logger, region, previousAt, poolCounters{}, previousAt, db.Stats())

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				previous = logDBPoolStats(logger, region, previousAt, previous, now, db.Stats())
				previousAt = now
			}
		}
	}()

	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			cancel()
			<-done
		})
	}
}
