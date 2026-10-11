package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func PreparedReferenceScenario(t *testing.T, body func(*testing.T, *Pair, Backend)) {
	t.Helper()
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func PreparedReferenceLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Registered after fixture/session cleanup, thus LIFO rolls back each specific
// GID before physical owners and the schema are destroyed. Never clean peers.
func PreparedReferenceCleanup(t *testing.T, b Backend, gid string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := b.DB.ExecContext(ctx, "ROLLBACK PREPARED "+PreparedReferenceLiteral(gid))
		if err != nil {
			d := ExtractDiagnostic(t, err)
			if d.Code != "42704" {
				t.Errorf("prepared cleanup %q: %v", gid, err)
			}
		}
	})
}

func PreparedReferencePrepare(t *testing.T, p *Pair, b Backend, x Executor, gid string) {
	t.Helper()
	PreparedReferenceCleanup(t, b, gid)
	LockExec(t, p.Ctx, x, "PREPARE TRANSACTION "+PreparedReferenceLiteral(gid), 0)
}

func PreparedReferenceRows(t *testing.T, p *Pair, b Backend, x Executor, q string, values ...int64) {
	t.Helper()
	RunStep(t, p.Ctx, x, b.Recorder, TransactionRows(false, q, values...))
}

func PreparedReferenceError(t *testing.T, ctx context.Context, x Executor, q, code, message string) {
	t.Helper()
	r, err := x.ExecContext(ctx, q)
	if r != nil {
		t.Fatal("failed control returned result", q)
	}
	want := ControlsReferenceDiagnostic(code, message)
	AssertDiagnostic(t, err, &want)
}

type preparedReferenceEntry struct {
	Xid      uint64
	Gid      string
	Prepared time.Time
	Owner    sql.NullString
	Database string
}

func preparedReferenceView(t *testing.T, p *Pair, x Executor, gid string) []preparedReferenceEntry {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_prepared_xacts WHERE gid="+PreparedReferenceLiteral(gid))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"transaction", "gid", "prepared", "owner", "database"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"XID", "TEXT", "TIMESTAMPTZ", "NAME", "NAME"} {
		if types[i].DatabaseTypeName() != name {
			t.Fatal(i, types[i].DatabaseTypeName(), name)
		}
	}
	var entries []preparedReferenceEntry
	for rows.Next() {
		var raw any
		var e preparedReferenceEntry
		if err := rows.Scan(&raw, &e.Gid, &e.Prepared, &e.Owner, &e.Database); err != nil {
			t.Fatal(err)
		}
		// pq exposes unknown scalar OID28 as raw decimal bytes, not an OID/int.
		bytes, ok := raw.([]byte)
		if !ok {
			t.Fatalf("raw XID %T (%v), want []byte", raw, raw)
		}
		e.Xid, err = strconv.ParseUint(string(bytes), 10, 32)
		if err != nil || e.Xid == 0 {
			t.Fatal(raw, err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func PreparedReferenceOne(t *testing.T, p *Pair, x Executor, gid string) preparedReferenceEntry {
	t.Helper()
	entries := preparedReferenceView(t, p, x, gid)
	if len(entries) != 1 {
		t.Fatal("prepared entry count", len(entries), gid)
	}
	return entries[0]
}

func PreparedReferenceAbsent(t *testing.T, p *Pair, x Executor, gid string) {
	t.Helper()
	if entries := preparedReferenceView(t, p, x, gid); len(entries) != 0 {
		t.Fatal("unexpected prepared entry", fmt.Sprint(entries))
	}
}
