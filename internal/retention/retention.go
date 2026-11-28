// Package retention deletes old OTel rows and stream files (#918).
package retention

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/myuon/agmux/internal/config"
)

const (
	otelBatchSize = 5000
	// InitialDelay is how long after startup the first run happens.
	InitialDelay = time.Minute
	// Interval is the period between runs.
	Interval = 24 * time.Hour
)

// Cleaner prunes data older than the configured retention days.
type Cleaner struct {
	DB         *sql.DB
	StreamsDir string
	Config     func() config.RetentionConfig
	Logger     *slog.Logger

	Now        func() time.Time
	BatchSleep time.Duration // pause between delete batches
}

// New creates a Cleaner. cfg is evaluated on every run so edits to
// config.toml take effect without a restart when the caller reloads it.
func New(db *sql.DB, streamsDir string, cfg func() config.RetentionConfig, logger *slog.Logger) *Cleaner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Cleaner{DB: db, StreamsDir: streamsDir, Config: cfg, Logger: logger, Now: time.Now, BatchSleep: 100 * time.Millisecond}
}

// Run blocks, running once after InitialDelay and then every Interval until
// ctx is cancelled.
func (c *Cleaner) Run(ctx context.Context) {
	timer := time.NewTimer(InitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		c.RunOnce(ctx)
		timer.Reset(Interval)
	}
}

// RunOnce performs one OTel prune and one stream prune.
func (c *Cleaner) RunOnce(ctx context.Context) {
	cfg := c.Config()
	if cfg.OtelDays > 0 {
		if err := c.PruneOtel(ctx, cfg.OtelDays); err != nil {
			c.Logger.Warn("retention: otel prune failed", "error", err)
		}
	}
	if cfg.StreamDays > 0 {
		if err := c.PruneStreams(ctx, cfg.StreamDays); err != nil {
			c.Logger.Warn("retention: stream prune failed", "error", err)
		}
	}
}

// PruneOtel deletes otel_events / otel_metrics rows older than days, in batches.
func (c *Cleaner) PruneOtel(ctx context.Context, days int) error {
	cutoff := c.Now().AddDate(0, 0, -days)
	for _, table := range []string{"otel_events", "otel_metrics"} {
		var total int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			res, err := c.DB.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %[1]s WHERE id IN (SELECT id FROM %[1]s WHERE timestamp < ? LIMIT %[2]d)`, table, otelBatchSize),
				cutoff)
			if err != nil {
				return fmt.Errorf("delete from %s: %w", table, err)
			}
			n, _ := res.RowsAffected()
			total += n
			if n == 0 {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.BatchSleep):
			}
		}
		if total > 0 {
			c.Logger.Info("retention: pruned otel rows", "table", table, "rows", total, "older_than_days", days)
		}
	}
	return nil
}

// PruneStreams deletes <id>.jsonl files not modified for days, but only when
// the session no longer exists or is archived / paused / exited.
func (c *Cleaner) PruneStreams(ctx context.Context, days int) error {
	entries, err := os.ReadDir(c.StreamsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := c.Now().AddDate(0, 0, -days)
	var files int
	var bytes int64
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		deletable, err := c.deletable(ctx, id)
		if err != nil {
			return err
		}
		if !deletable {
			continue
		}
		if err := os.Remove(filepath.Join(c.StreamsDir, name)); err != nil {
			if !os.IsNotExist(err) {
				c.Logger.Warn("retention: remove stream failed", "file", name, "error", err)
			}
			continue
		}
		files++
		bytes += fi.Size()
	}
	if files > 0 {
		c.Logger.Info("retention: pruned stream files", "files", files, "bytes", bytes, "older_than_days", days)
	}
	return nil
}

func (c *Cleaner) deletable(ctx context.Context, id string) (bool, error) {
	var status string
	err := c.DB.QueryRowContext(ctx, `SELECT status FROM sessions WHERE id = ?`, id).Scan(&status)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	switch status {
	case "archived", "paused", "exited":
		return true, nil
	}
	return false, nil
}
