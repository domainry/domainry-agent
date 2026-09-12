package base

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ConnectionOptions struct{ Path, DSN, Schema string }
type Connection struct {
	DB                       *sql.DB
	Driver, Schema, FilePath string
	ReleaseStartup           func() error
	closeLocal               func() error
}

func (c *Connection) Close() error {
	var err error
	if c.ReleaseStartup != nil {
		err = c.ReleaseStartup()
	}
	if c.DB != nil {
		err = errors.Join(err, c.DB.Close())
		c.DB = nil
	}
	if c.closeLocal != nil {
		err = errors.Join(err, c.closeLocal())
		c.closeLocal = nil
	}
	return err
}

// LockFile is held for the lifetime of local file ownership.
func LockFile(path string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("host storage is already in use: %w", err)
	}
	return sync.OnceValue(func() error { return errors.Join(unix.Flock(int(f.Fd()), unix.LOCK_UN), f.Close()) }), nil
}
func (c *Connection) SetLocalRelease(release func() error) { c.closeLocal = release }

// AcquireStartup pins one physical session for the entire recursive migration
// sequence. Engine adapters supply advisory-lock operations; the pool remains
// available to module callbacks. Releasing a broken session discards it.
func AcquireStartup(ctx context.Context, db *sql.DB, try func(context.Context, *sql.Conn) (bool, error), unlock func(context.Context, *sql.Conn) error) (func() error, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	discard := func() { conn.Raw(func(any) error { return driver.ErrBadConn }); conn.Close() }
	lockCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		ok, err := try(lockCtx, conn)
		if err != nil {
			discard()
			return nil, fmt.Errorf("acquire host migration lock: %w", err)
		}
		if ok {
			break
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-lockCtx.Done():
			timer.Stop()
			discard()
			return nil, fmt.Errorf("host migration lock timeout: %w", lockCtx.Err())
		case <-timer.C:
		}
	}
	return sync.OnceValue(func() error {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := unlock(releaseCtx, conn); err != nil {
			discard()
			return err
		}
		return conn.Close()
	}), nil
}
