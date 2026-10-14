package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var pgcColumns = []string{"name", "statement", "is_holdable", "is_binary", "is_scrollable", "creation_time"}
var pgcTypes = []string{"TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "TIMESTAMPTZ"}

func pgcScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			defer c.ExecContext(context.Background(), "CLOSE ALL")
			body(t, p, b, c)
		})
	}
}

type pgcTuple struct {
	Name, Statement      string
	Hold, Binary, Scroll bool
	Created              time.Time
}

func pgcQuery(name string) string {
	return "SELECT name,statement,is_holdable,is_binary,is_scrollable,creation_time FROM pg_catalog.pg_cursors WHERE name='" + name + "' ORDER BY name"
}

func pgcMetadata(t *testing.T, r *sql.Rows, pg bool) {
	t.Helper()
	cols, err := r.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cols, pgcColumns) {
		t.Fatalf("columns %v", cols)
	}
	meta, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 6 {
		t.Fatalf("column count %d", len(meta))
	}
	for i, m := range meta {
		wantScan := []reflect.Type{reflect.TypeFor[string](), reflect.TypeFor[string](), reflect.TypeFor[bool](), reflect.TypeFor[bool](), reflect.TypeFor[bool](), reflect.TypeFor[time.Time]()}[i]
		if m.DatabaseTypeName() != pgcTypes[i] || m.ScanType() != wantScan {
			t.Fatalf("column %d: type %s scan %v want %s/%v", i, m.DatabaseTypeName(), m.ScanType(), pgcTypes[i], wantScan)
		}
		n, known := m.Nullable()
		if pg && (known || n) || !pg && (!known || !n) {
			t.Fatalf("column %d Nullable %t/%t", i, n, known)
		}
	}
}

func pgcRead(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, name string) []pgcTuple {
	t.Helper()
	r, err := x.QueryContext(p.Ctx, pgcQuery(name))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pgcMetadata(t, r, b.PG)
	var result []pgcTuple
	for r.Next() {
		var row pgcTuple
		if err := r.Scan(&row.Name, &row.Statement, &row.Hold, &row.Binary, &row.Scroll, &row.Created); err != nil {
			t.Fatal(err)
		}
		if row.Created.IsZero() {
			t.Fatal("zero creation_time")
		}
		result = append(result, row)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return result
}

func pgcOne(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, name, source string, hold, scroll bool) pgcTuple {
	t.Helper()
	rows := pgcRead(t, p, b, x, name)
	if len(rows) != 1 {
		t.Fatalf("%s rows %#v", name, rows)
	}
	r := rows[0]
	if r.Name != name || r.Statement != source || r.Hold != hold || r.Binary || r.Scroll != scroll {
		t.Fatalf("metadata %#v want %q/%q/%t/false/%t", r, name, source, hold, scroll)
	}
	return r
}

func pgcSame(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, want pgcTuple) {
	t.Helper()
	got := pgcOne(t, p, b, x, want.Name, want.Statement, want.Hold, want.Scroll)
	if !got.Created.Equal(want.Created) {
		t.Fatalf("creation_time changed %v -> %v", want.Created, got.Created)
	}
}

func pgcEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, names ...string) {
	t.Helper()
	for _, name := range names {
		if rows := pgcRead(t, p, b, x, name); len(rows) != 0 {
			t.Fatalf("%s remains: %#v", name, rows)
		}
	}
}

func pgcClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	var stamp time.Time
	if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func pgcBracket(t *testing.T, stamp, before, after time.Time) {
	t.Helper()
	if stamp.Before(before) || stamp.After(after) {
		t.Fatalf("creation %v outside [%v,%v]", stamp, before, after)
	}
}
