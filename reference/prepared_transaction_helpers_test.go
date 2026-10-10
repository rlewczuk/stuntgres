package reference_test

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

func preparedReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) { body(t, p, b) })
	}
}

func preparedReferenceLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Registered after fixture/session cleanup, thus LIFO rolls back each specific
// GID before physical owners and the schema are destroyed. Never clean peers.
func preparedReferenceCleanup(t *testing.T, b referenceBackend, gid string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := b.db.ExecContext(ctx, "ROLLBACK PREPARED "+preparedReferenceLiteral(gid))
		if err != nil {
			d := referenceExtractDiagnostic(t, err)
			if d.Code != "42704" {
				t.Errorf("prepared cleanup %q: %v", gid, err)
			}
		}
	})
}

func preparedReferencePrepare(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, gid string) {
	t.Helper()
	preparedReferenceCleanup(t, b, gid)
	referenceLockExec(t, p.ctx, x, "PREPARE TRANSACTION "+preparedReferenceLiteral(gid), 0)
}

func preparedReferenceRows(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, values ...int64) {
	t.Helper()
	referenceRunStep(t, p.ctx, x, b.recorder, referenceTransactionRows(false, q, values...))
}

func preparedReferenceError(t *testing.T, ctx context.Context, x referenceExecutor, q, code, message string) {
	t.Helper()
	r, err := x.ExecContext(ctx, q)
	if r != nil {
		t.Fatal("failed control returned result", q)
	}
	want := controlsReferenceDiagnostic(code, message)
	referenceAssertDiagnostic(t, err, &want)
}

type preparedReferenceEntry struct {
	xid      uint64
	gid      string
	prepared time.Time
	owner    sql.NullString
	database string
}

func preparedReferenceView(t *testing.T, p *referencePair, x referenceExecutor, gid string) []preparedReferenceEntry {
	t.Helper()
	rows, err := x.QueryContext(p.ctx, "SELECT * FROM pg_catalog.pg_prepared_xacts WHERE gid="+preparedReferenceLiteral(gid))
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
		if err := rows.Scan(&raw, &e.gid, &e.prepared, &e.owner, &e.database); err != nil {
			t.Fatal(err)
		}
		// pq exposes unknown scalar OID28 as raw decimal bytes, not an OID/int.
		bytes, ok := raw.([]byte)
		if !ok {
			t.Fatalf("raw XID %T (%v), want []byte", raw, raw)
		}
		e.xid, err = strconv.ParseUint(string(bytes), 10, 32)
		if err != nil || e.xid == 0 {
			t.Fatal(raw, err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func preparedReferenceOne(t *testing.T, p *referencePair, x referenceExecutor, gid string) preparedReferenceEntry {
	t.Helper()
	entries := preparedReferenceView(t, p, x, gid)
	if len(entries) != 1 {
		t.Fatal("prepared entry count", len(entries), gid)
	}
	return entries[0]
}

func preparedReferenceAbsent(t *testing.T, p *referencePair, x referenceExecutor, gid string) {
	t.Helper()
	if entries := preparedReferenceView(t, p, x, gid); len(entries) != 0 {
		t.Fatal("unexpected prepared entry", fmt.Sprint(entries))
	}
}
