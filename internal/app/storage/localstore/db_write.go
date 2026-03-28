package localstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (d *Database) withWriteTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("localstore: db is nil")
	}
	if fn == nil {
		return nil
	}
	return d.withWriteLock(ctx, func() error {
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() {
			_ = tx.Rollback()
		}()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (d *Database) withWriteLock(ctx context.Context, fn func() error) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("localstore: db is nil")
	}
	if fn == nil {
		return nil
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return retrySQLiteBusy(ctx, fn)
}

func retrySQLiteBusy(ctx context.Context, fn func() error) error {
	delay := 10 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := fn()
		if !isSQLiteBusy(err) || attempt >= 7 {
			return err
		}
		if ctx == nil {
			time.Sleep(delay)
		} else {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "database schema is locked")
}
