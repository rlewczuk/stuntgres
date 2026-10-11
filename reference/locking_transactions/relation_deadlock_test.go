package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type deadlockPublicOutcome struct {
	result sql.Result
	err    error
	index  int
}

func deadlockPublicConn(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deadlockPublicRollback(c); c.Close() })
	return c
}
func deadlockPublicExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, count int64) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	deadlockPublicCount(t, r, count)
}
func deadlockPublicCount(t *testing.T, r sql.Result, want int64) {
	t.Helper()
	if r == nil {
		t.Fatal("missing result")
	}
	n, err := r.RowsAffected()
	if err != nil || n != want {
		t.Fatalf("count %d (%v), want %d", n, err, want)
	}
}
func deadlockPublicRows(t *testing.T, ctx context.Context, c *sql.Conn, postgres bool, q string, want [][]int64) {
	t.Helper()
	rows, err := c.QueryContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][]int64
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	scan := reflect.TypeFor[int64]()
	if postgres {
		scan = reflect.TypeFor[int32]()
	}
	for _, column := range metadata {
		if column.DatabaseTypeName() != "INT4" || column.ScanType() != scan {
			t.Fatalf("INT4 metadata %s/%v, want INT4/%v", column.DatabaseTypeName(), column.ScanType(), scan)
		}
	}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		row := make([]int64, len(values))
		for i, v := range values {
			n, ok := v.(int64)
			if !ok {
				t.Fatalf("INT4 value %T, want int64", v)
			}
			row[i] = n
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v want %v", got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
func deadlockPublicWorker(t *testing.T, parent context.Context, index int, call func(context.Context) (sql.Result, error)) func() deadlockPublicOutcome {
	t.Helper()
	await := fixture.LockOperation(t, parent, func(ctx context.Context) fixture.RelationOutcome {
		r, err := call(ctx)
		return fixture.RelationOutcome{Result: r, Err: err}
	})
	return func() deadlockPublicOutcome {
		o := await()
		return deadlockPublicOutcome{result: o.Result, err: o.Err, index: index}
	}
}
func deadlockPublicRollback(c *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.ExecContext(ctx, "ROLLBACK")
}

func deadlockPublicAborted(t *testing.T, ctx context.Context, c *sql.Conn, query string) {
	t.Helper()
	_, err := c.ExecContext(ctx, query)
	fixture.WaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
}

// Both conflicting commands are bounded; neither transaction is rolled back
// until the survivor has completed. This catches lock release only on ROLLBACK.
func deadlockPublicCycle(t *testing.T, db *sql.DB, kind string, cached bool, postgres bool, observe func(context.Context, *sql.Conn, *sql.Conn, string), diagnostic func(error, int, string)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	a := deadlockPublicConn(t, ctx, db)
	b := deadlockPublicConn(t, ctx, db)
	conns := []*sql.Conn{a, b}
	pids := []int{0, 0}
	oids := map[int64]bool{}
	xids := map[string]int{}
	var dboid int64
	var deletedTuple string
	if postgres {
		pids[0] = fixture.LockPID(t, ctx, a)
		pids[1] = fixture.LockPID(t, ctx, b)
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&dboid); err != nil {
			t.Fatal(err)
		}
	}
	deadlockPublicExec(t, ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
	deadlockPublicExec(t, ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
	deadlockPublicExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
	deadlockPublicExec(t, ctx, a, "INSERT INTO u VALUES(20)", 1)
	if postgres && kind == "delete" {
		if err := a.QueryRowContext(ctx, "SELECT ctid::text FROM t WHERE id=1").Scan(&deletedTuple); err != nil {
			t.Fatal(err)
		}
	}
	initial := []string{"UPDATE t SET v=101 WHERE id=1", "UPDATE t SET v=202 WHERE id=2"}
	closing := []string{"UPDATE t SET v=102 WHERE id=2", "UPDATE t SET v=201 WHERE id=1"}
	count := int64(1)
	switch kind {
	case "table":
		initial = []string{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "LOCK TABLE u IN ACCESS EXCLUSIVE MODE"}
		closing = []string{"LOCK TABLE u IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t IN ACCESS EXCLUSIVE MODE"}
		count = 0
	case "delete":
		initial[0] = "DELETE FROM t WHERE id=1"
		closing[1] = "DELETE FROM t WHERE id=1"
	case "partial":
		closing[0] = "UPDATE t SET v=102"
	case "mixed":
		initial[1] = "LOCK TABLE u IN ACCESS EXCLUSIVE MODE"
		closing[0] = "LOCK TABLE u IN ACCESS EXCLUSIVE MODE"
	}
	statements := make([]*sql.Stmt, 2)
	if cached {
		for i, c := range conns {
			s, e := c.PrepareContext(ctx, closing[i])
			if e != nil {
				t.Fatal(e)
			}
			statements[i] = s
			t.Cleanup(func() { s.Close() })
		}
	}
	for i, c := range conns {
		deadlockPublicExec(t, ctx, c, "BEGIN", 0)
		t.Cleanup(func() { deadlockPublicRollback(c) })
		n := int64(1)
		if strings.HasPrefix(initial[i], "LOCK") {
			n = 0
		}
		deadlockPublicExec(t, ctx, c, initial[i], n)
	}
	if kind == "table" {
		deadlockPublicExec(t, ctx, a, "INSERT INTO t VALUES(3,101)", 1)
		deadlockPublicExec(t, ctx, b, "INSERT INTO u VALUES(202)", 1)
	}
	if kind == "mixed" {
		deadlockPublicExec(t, ctx, b, "INSERT INTO u VALUES(202)", 1)
	}
	if kind != "table" {
		reader := deadlockPublicConn(t, ctx, db)
		deadlockPublicRows(t, ctx, reader, postgres, "SELECT id,v FROM t ORDER BY id", [][]int64{{1, 10}, {2, 20}})
	}
	if postgres {
		oids[fixture.LockOID(t, ctx, "public.t")] = true
		oids[fixture.LockOID(t, ctx, "public.u")] = true
		for i, conn := range conns {
			var xid string
			if err := conn.QueryRowContext(ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
				t.Fatal(err)
			}
			xids[xid] = pids[i]
		}
		observe = func(ctx context.Context, _ *sql.Conn, _ *sql.Conn, kind string) {
			if kind == "table" || kind == "mixed" {
				fixture.LockWaitBehind(t, ctx, pids[0], pids[1], fixture.LockOID(t, ctx, "public.u"), "AccessExclusiveLock")
			} else {
				fixture.RowWait(t, ctx, pids[0], pids[1])
			}
		}
		diagnostic = func(err error, victim int, kind string) {
			fixture.DeadlockPhysical(t, err, pids, oids, dboid, xids, victim, kind, deletedTuple)
		}
	}
	run := func(i int) func(context.Context) (sql.Result, error) {
		return func(ctx context.Context) (sql.Result, error) {
			if cached {
				return statements[i].ExecContext(ctx)
			}
			return conns[i].ExecContext(ctx, closing[i])
		}
	}
	first := deadlockPublicWorker(t, ctx, 0, run(0))
	if observe != nil {
		observe(ctx, a, b, kind)
	}
	second := deadlockPublicWorker(t, ctx, 1, run(1))
	out := []deadlockPublicOutcome{first(), second()}
	victim := -1
	for i, o := range out {
		if o.err != nil {
			if victim != -1 {
				t.Fatalf("two failures: %v / %v", out[0].err, out[1].err)
			}
			victim = i
			if o.result != nil {
				t.Fatal("victim returned result")
			}
			diagnostic(o.err, i, kind)
		} else {
			want := count
			if kind == "mixed" && i == 0 {
				want = 0
			}
			if kind == "partial" && i == 0 {
				want = 2
			}
			deadlockPublicCount(t, o.result, want)
		}
	}
	if victim < 0 {
		t.Fatal("no deadlock victim")
	}
	survivor := 1 - victim
	deadlockPublicAborted(t, ctx, conns[victim], "SELECT v FROM u")
	deadlockPublicExec(t, ctx, conns[victim], "ROLLBACK", 0)
	deadlockPublicExec(t, ctx, conns[survivor], "COMMIT", 0)
	want := [][]int64{{1, 101}, {2, 102}}
	u := [][]int64{{20}}
	switch kind {
	case "table":
		want = [][]int64{{1, 10}, {2, 20}}
		if survivor == 0 {
			want = append(want, []int64{3, 101})
		} else {
			u = append(u, []int64{202})
		}
	case "rows":
		if survivor == 1 {
			want = [][]int64{{1, 201}, {2, 202}}
		}
	case "partial":
		want = [][]int64{{1, 102}, {2, 102}}
		if survivor == 1 {
			want = [][]int64{{1, 201}, {2, 202}}
		}
	case "delete":
		want = [][]int64{{2, 102}}
		if survivor == 1 {
			want = [][]int64{{2, 202}}
		}
	case "mixed":
		want = [][]int64{{1, 101}, {2, 20}}
		if survivor == 1 {
			want = [][]int64{{1, 201}, {2, 20}}
			u = append(u, []int64{202})
		}
	}
	deadlockPublicRows(t, ctx, a, postgres, "SELECT id,v FROM t ORDER BY id", want)
	deadlockPublicRows(t, ctx, a, postgres, "SELECT v FROM u ORDER BY v", u)
	deadlockPublicExec(t, ctx, conns[victim], "BEGIN", 0)
	deadlockPublicExec(t, ctx, conns[victim], "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	deadlockPublicExec(t, ctx, conns[victim], "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	deadlockPublicExec(t, ctx, conns[victim], "UPDATE t SET v=9 WHERE id=2", 1)
	deadlockPublicExec(t, ctx, conns[victim], "ROLLBACK", 0)
}

func deadlockPublicSoftQueue(t *testing.T, db *sql.DB, postgres bool, observe func(context.Context, *sql.Conn, *sql.Conn, *sql.Conn, int)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	a := deadlockPublicConn(t, ctx, db)
	b := deadlockPublicConn(t, ctx, db)
	c := deadlockPublicConn(t, ctx, db)
	deadlockPublicExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
	deadlockPublicExec(t, ctx, a, "INSERT INTO t VALUES(10)", 1)
	deadlockPublicExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
	deadlockPublicExec(t, ctx, a, "INSERT INTO u VALUES(20)", 1)
	for _, conn := range []*sql.Conn{a, b, c} {
		deadlockPublicExec(t, ctx, conn, "BEGIN", 0)
		t.Cleanup(func() { deadlockPublicRollback(conn) })
	}
	if postgres {
		ap := fixture.LockPID(t, ctx, a)
		bp := fixture.LockPID(t, ctx, b)
		cp := fixture.LockPID(t, ctx, c)
		oid := fixture.LockOID(t, ctx, "public.t")
		observe = func(ctx context.Context, _ *sql.Conn, _ *sql.Conn, _ *sql.Conn, step int) {
			if step == 0 {
				fixture.LockWaitBehind(t, ctx, bp, ap, oid, "AccessExclusiveLock")
			} else {
				fixture.LockWaitBehind(t, ctx, cp, bp, oid, "AccessShareLock")
			}
		}
	}
	deadlockPublicRows(t, ctx, a, postgres, "SELECT v FROM t", [][]int64{{10}})
	deadlockPublicExec(t, ctx, c, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
	rb := deadlockPublicWorker(t, ctx, 1, func(ctx context.Context) (sql.Result, error) {
		return b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	})
	if observe != nil {
		observe(ctx, a, b, c, 0)
	}
	rc := deadlockPublicWorker(t, ctx, 2, func(ctx context.Context) (sql.Result, error) {
		rows, err := c.QueryContext(ctx, "SELECT v FROM t")
		if err == nil {
			defer rows.Close()
			var n int64
			if !rows.Next() {
				return nil, fmt.Errorf("missing row")
			}
			if err = rows.Scan(&n); err != nil || n != 10 {
				return nil, fmt.Errorf("row %d: %v", n, err)
			}
			if rows.Next() {
				return nil, fmt.Errorf("extra row")
			}
			err = rows.Err()
		}
		return nil, err
	})
	if observe != nil {
		observe(ctx, a, b, c, 1)
	}
	ra := deadlockPublicWorker(t, ctx, 0, func(ctx context.Context) (sql.Result, error) {
		rows, err := a.QueryContext(ctx, "SELECT v FROM u")
		if err == nil {
			defer rows.Close()
			var n int64
			if !rows.Next() {
				return nil, fmt.Errorf("missing row")
			}
			if err = rows.Scan(&n); err != nil || n != 20 {
				return nil, fmt.Errorf("row %d: %v", n, err)
			}
			if rows.Next() {
				return nil, fmt.Errorf("extra row")
			}
			err = rows.Err()
		}
		return nil, err
	})
	if o := rc(); o.err != nil {
		t.Fatal("soft reorder must complete before A releases AS", o.err)
	}
	deadlockPublicExec(t, ctx, c, "COMMIT", 0)
	if o := ra(); o.err != nil {
		t.Fatal(o.err)
	}
	deadlockPublicExec(t, ctx, a, "COMMIT", 0)
	o := rb()
	if o.err != nil {
		t.Fatal(o.err)
	}
	deadlockPublicCount(t, o.result, 0)
	deadlockPublicExec(t, ctx, b, "COMMIT", 0)
	deadlockPublicExec(t, ctx, c, "BEGIN", 0)
	deadlockPublicExec(t, ctx, c, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	deadlockPublicExec(t, ctx, c, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	deadlockPublicExec(t, ctx, c, "ROLLBACK", 0)
}

func TestReferenceRelationDeadlockTable(t *testing.T) { referenceDeadlockKinds(t, []string{"table"}) }
func TestReferenceRelationDeadlockRows(t *testing.T) {
	referenceDeadlockKinds(t, []string{"rows", "delete", "partial"})
}
func TestReferenceRelationDeadlockMixed(t *testing.T) { referenceDeadlockKinds(t, []string{"mixed"}) }
func referenceDeadlockKinds(t *testing.T, kinds []string) {
	for _, kind := range kinds {
		for _, cached := range []bool{false, true} {
			t.Run(kind+"/"+fmt.Sprint(cached), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						deadlockPublicCycle(t, backend.DB, kind, cached, backend.PG, nil, func(err error, _ int, _ string) { fixture.DeadlockPublicError(t, err, "") })
					})
				}
			})
		}
	}
}
func TestReferenceRelationDeadlockSoftQueue(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { deadlockPublicSoftQueue(t, backend.DB, backend.PG, nil) })
	}
}
