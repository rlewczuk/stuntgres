package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

func ExtensionDump(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var b bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// A separate physical owner is essential: Conn.Close alone returns a pool lease.
// The existing Joined fixture boundary keeps a failing close witness bounded;
// every successful close joins its worker before inspecting the peer catalog.
type ExtensionTempOwner struct {
	db       *sql.DB
	Conn     *sql.Conn
	Recorder *Recorder
	Notices  []Diagnostic
	once     sync.Once
	done     chan struct{}
	timedOut bool // Test-owner thread only; fixture fallback is failure-only.
	err      error
}

func ExtensionTemporaryOwner(t *testing.T, p *Pair, pg bool) *ExtensionTempOwner {
	t.Helper()
	o := &ExtensionTempOwner{done: make(chan struct{})}
	o.db, o.Conn, o.Recorder = NoticeReferencePool(t, p, pg, &o.Notices, NoticeReferenceOption{Joined: func() bool { return !o.timedOut }})
	t.Cleanup(func() { o.Close(t) })
	return o
}

func (o *ExtensionTempOwner) Close(t *testing.T) bool {
	t.Helper()
	if o.timedOut {
		return false
	}
	o.once.Do(func() {
		go func() {
			err := o.Conn.Close()
			if errors.Is(err, sql.ErrConnDone) {
				err = nil
			}
			o.err = errors.Join(err, o.db.Close())
			close(o.done)
		}()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-o.done:
		if o.err != nil {
			t.Error("physical TEMP owner close", o.err)
			return false
		}
		return true
	case <-ctx.Done():
		o.timedOut = true
		t.Error("physical TEMP owner close exceeded finite cleanup deadline", ctx.Err())
		return false
	}
}

func ExtensionTempIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func ExtensionTempSeed(t *testing.T, p *Pair, o *ExtensionTempOwner) string {
	t.Helper()
	RunStep(t, p.Ctx, o.Conn, o.Recorder, Exec("CREATE TEMP TABLE ext_temp_seed(id INT)", 0))
	var name string
	if err := o.Conn.QueryRowContext(p.Ctx, "SELECT schemaname FROM pg_tables WHERE tablename='ext_temp_seed'").Scan(&name); err != nil || !strings.HasPrefix(name, "pg_temp_") {
		t.Fatal("actual TEMP namespace", name, err)
	}
	return name
}
