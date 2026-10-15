package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func blockingScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func blockingConn(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, int) {
	t.Helper()
	c := namespaceConn(t, p, b)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = c.ExecContext(ctx, "ROLLBACK")
	})
	if b.PG {
		return c, fixture.LockPID(t, p.Ctx, c)
	}
	// TEMP is initialized outside every transaction which might be prepared.
	namespaceExec(t, p, b, c, "CREATE TEMP SEQUENCE blocking_pid_identity")
	var name string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", namespaceOID(t, p, c)).Scan(&name); err != nil {
		t.Fatal(err)
	}
	suffix, ok := strings.CutPrefix(name, "pg_temp_")
	id, err := strconv.ParseUint(suffix, 10, 64)
	if !ok || err != nil || id == 0 || id > 2147483647 {
		t.Fatal("native physical identity", name, err)
	}
	return c, int(id)
}

func blockingMetadata(b fixture.Backend) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range types {
			if column.DatabaseTypeName() != "_INT4" {
				t.Fatal("inquiry type", column.DatabaseTypeName())
			}
			scan := reflect.TypeFor[string]()
			if b.PG {
				scan = reflect.TypeFor[any]()
			}
			if column.ScanType() != scan {
				t.Fatal("inquiry ScanType", column.ScanType(), scan)
			}
			nullable, known := column.Nullable()
			if b.PG {
				if nullable || known {
					t.Fatal("pq nullability", nullable, known)
				}
			} else if !nullable || !known {
				t.Fatal("native nullability", nullable, known)
			}
		}
	}
}

func blockingBag(t *testing.T, b fixture.Backend, rows *sql.Rows) []int {
	t.Helper()
	defer rows.Close()
	blockingMetadata(b)(t, rows)
	if !rows.Next() {
		t.Fatal("missing inquiry row", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var text string
	if b.PG {
		v, ok := raw.([]byte)
		if !ok {
			t.Fatalf("pq array carrier %T", raw)
		}
		text = string(v)
	} else {
		v, ok := raw.(string)
		if !ok {
			t.Fatalf("native array carrier %T", raw)
		}
		text = v
	}
	if rows.Next() {
		t.Fatal("extra inquiry row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if text == "{}" {
		return []int{}
	}
	if !strings.HasPrefix(text, "{") || !strings.HasSuffix(text, "}") {
		t.Fatal("not an int4 array", text)
	}
	var bag []int
	for _, s := range strings.Split(text[1:len(text)-1], ",") {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			t.Fatal(text, err)
		}
		bag = append(bag, int(n))
	}
	sort.Ints(bag)
	return bag
}

func blockingRead(t *testing.T, ctx context.Context, b fixture.Backend, c fixture.Executor, function string, pid int) []int {
	t.Helper()
	rows, err := c.QueryContext(ctx, "SELECT "+function+"($1)", pid)
	if err != nil {
		t.Fatal(err)
	}
	return blockingBag(t, b, rows)
}

func blockingWant(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, function string, pid int, want ...int) {
	t.Helper()
	got := blockingRead(t, p.Ctx, b, c, function, pid)
	sort.Ints(want)
	if len(got) != len(want) || (len(want) != 0 && !reflect.DeepEqual(got, want)) {
		t.Fatal(function, pid, "got", got, "want", want)
	}
}

// An observed exact blocker bag proves admission, not a launched goroutine or
// a sleep. The identity expectation comes from an independent physical source.
func blockingWait(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, function string, pid int, want ...int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 4*time.Second)
	defer cancel()
	sort.Ints(want)
	for {
		got := blockingRead(t, ctx, b, c, function, pid)
		if reflect.DeepEqual(got, want) {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("wait admission", function, got, want, ctx.Err())
		}
		runtime.Gosched()
	}
}

func blockingLock(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, prepared bool) func() fixture.RelationOutcome {
	t.Helper()
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stmt.Close() })
	}
	return fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		var r sql.Result
		var err error
		if prepared {
			r, err = stmt.ExecContext(ctx)
		} else {
			r, err = c.ExecContext(ctx, q)
		}
		return fixture.RelationOutcome{Result: r, Err: err}
	})
}

func blockingLockDone(t *testing.T, await func() fixture.RelationOutcome) {
	t.Helper()
	o := await()
	if o.Err != nil || o.Result == nil {
		t.Fatal("lock completion", o.Err)
	}
	if n, err := o.Result.RowsAffected(); err != nil || n != 0 {
		t.Fatal("lock count", n, err)
	}
}
