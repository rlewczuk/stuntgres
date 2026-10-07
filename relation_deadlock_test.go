package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

type deadlockPublicOutcome struct {
	result sql.Result
	err    error
	index  int
}

func deadlockPublicDB(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(5)
	t.Cleanup(func() { db.Close() })
	return db
}
func deadlockPublicConn(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
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
func deadlockPublicRows(t *testing.T, ctx context.Context, c *sql.Conn, q string, want [][]int64) {
	t.Helper()
	rows, err := c.QueryContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.DatabaseTypeName() != "INT4" || column.ScanType() != reflect.TypeOf(int64(0)) {
			t.Fatalf("INT4 metadata: %s %v", column.DatabaseTypeName(), column.ScanType())
		}
	}
	var got [][]int64
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		row := make([]int64, len(columns))
		for i, v := range values {
			n, ok := v.(int64)
			if !ok {
				t.Fatalf("value %T, want int64", v)
			}
			row[i] = n
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
}
func deadlockPublicWorker(t *testing.T, parent context.Context, index int, call func(context.Context) (sql.Result, error)) func() deadlockPublicOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	done := make(chan deadlockPublicOutcome, 1)
	joined := false
	go func() { r, e := call(ctx); done <- deadlockPublicOutcome{r, e, index} }()
	await := func() deadlockPublicOutcome {
		t.Helper()
		select {
		case o := <-done:
			joined = true
			return o
		case <-time.After(10 * time.Second):
			t.Fatal("bounded operation failed to join")
			return deadlockPublicOutcome{}
		}
	}
	t.Cleanup(func() {
		cancel()
		if !joined {
			await()
		}
	})
	return await
}
func deadlockPublicRollback(c *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.ExecContext(ctx, "ROLLBACK")
}

func deadlockPublicError(t *testing.T, err error, position string) *stuntgres.Error {
	t.Helper()
	var d *stuntgres.Error
	if !errors.As(err, &d) {
		t.Fatalf("want 40P01, got %v", err)
	}
	if d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Code != "40P01" || d.Message != "deadlock detected" || d.Hint != "See server log for query details." || d.Position != position || d.Context != "" {
		t.Fatalf("deadlock diagnostic %#v, expected position %q", d, position)
	}
	if d.InternalPosition != "" || d.InternalQuery != "" || d.Schema != "" || d.Table != "" || d.Column != "" || d.DataType != "" || d.Constraint != "" || d.File != "" || d.Line != "" || d.Routine != "" {
		t.Fatalf("unexpected unrelated native fields %#v", d)
	}
	lines := strings.Split(strings.TrimSpace(d.Detail), "\n")
	if len(lines) != 2 {
		t.Fatalf("two-edge detail: %q", d.Detail)
	}
	pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for (AccessShareLock|RowShareLock|RowExclusiveLock|ShareUpdateExclusiveLock|ShareRowExclusiveLock|AccessExclusiveLock|Update|NoKeyUpdate|Share|KeyShare) on relation ([1-9][0-9]*) \(class ([1-9][0-9]*), sub-ID 0, epoch ([1-9][0-9]*)\)( row ([1-9][0-9]*))?; blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	a := pattern.FindStringSubmatch(lines[0])
	b := pattern.FindStringSubmatch(lines[1])
	if a == nil || b == nil || a[1] != b[10] || b[1] != a[10] || a[2] != b[11] || b[2] != a[11] || a[3] != b[12] || b[3] != a[12] {
		t.Fatalf("complete closed native cycle identity: %q", d.Detail)
	}
	for _, edge := range [][]string{a, b} {
		if edge[6] != "1259" || edge[7] != edge[3] || (edge[4] == "Update" || edge[4] == "NoKeyUpdate" || edge[4] == "Share" || edge[4] == "KeyShare") != (edge[9] != "") {
			t.Fatalf("resource class/epoch/row-mode identity: %q", d.Detail)
		}
	}
	return d
}
func deadlockPublicAborted(t *testing.T, ctx context.Context, c *sql.Conn) {
	t.Helper()
	_, err := c.ExecContext(ctx, "SELECT v FROM u")
	var d *stuntgres.Error
	if !errors.As(err, &d) || d.Code != "25P02" || d.Message != "current transaction is aborted, commands ignored until end of transaction block" || d.Position != "" {
		t.Fatalf("aborted recovery: %v", err)
	}
}

// Both conflicting commands are bounded; neither transaction is rolled back
// until the survivor has completed. This catches lock release only on ROLLBACK.
func deadlockPublicCycle(t *testing.T, db *sql.DB, kind string, cached bool, observe func(context.Context, *sql.Conn, *sql.Conn, string), diagnostic func(error, int, string)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	a := deadlockPublicConn(t, ctx, db)
	b := deadlockPublicConn(t, ctx, db)
	conns := []*sql.Conn{a, b}
	deadlockPublicExec(t, ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
	deadlockPublicExec(t, ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
	deadlockPublicExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
	deadlockPublicExec(t, ctx, a, "INSERT INTO u VALUES(20)", 1)
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
		deadlockPublicRows(t, ctx, reader, "SELECT id,v FROM t ORDER BY id", [][]int64{{1, 10}, {2, 20}})
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
	deadlockPublicAborted(t, ctx, conns[victim])
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
	deadlockPublicRows(t, ctx, a, "SELECT id,v FROM t ORDER BY id", want)
	deadlockPublicRows(t, ctx, a, "SELECT v FROM u ORDER BY v", u)
	deadlockPublicExec(t, ctx, conns[victim], "BEGIN", 0)
	deadlockPublicExec(t, ctx, conns[victim], "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	deadlockPublicExec(t, ctx, conns[victim], "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	deadlockPublicExec(t, ctx, conns[victim], "UPDATE t SET v=9 WHERE id=2", 1)
	deadlockPublicExec(t, ctx, conns[victim], "ROLLBACK", 0)
}

func deadlockPublicTable(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			deadlockPublicCycle(t, deadlockPublicDB(t), "table", cached, nil, func(err error, _ int, _ string) { deadlockPublicError(t, err, "") })
		})
	}
}
func deadlockPublicRowsMixed(t *testing.T) {
	for _, kind := range []string{"rows", "delete", "partial", "mixed"} {
		for _, cached := range []bool{false, true} {
			t.Run(kind+"/"+fmt.Sprint(cached), func(t *testing.T) {
				deadlockPublicCycle(t, deadlockPublicDB(t), kind, cached, nil, func(err error, _ int, _ string) {
					d := deadlockPublicError(t, err, "")
					if !strings.Contains(d.Detail, " row ") {
						t.Fatal("row identity absent", d.Detail)
					}
				})
			})
		}
	}
}

// Direct and semantic Prepare perform relation analysis; cached execution does
// not. Victim is not predetermined: assert the phase of the actual loser.
func deadlockPublicPreparation(t *testing.T) {
	for _, phase := range []string{"direct", "prepare", "cached"} {
		for _, tc := range []struct{ q, pos string }{
			{"SELECT v FROM t", "15"}, {"INSERT INTO t VALUES(1,7)", "13"}, {"UPDATE t SET v=7 WHERE id=1", "8"}, {"DELETE FROM t WHERE id=1", "13"}, {" SELECT v FROM public.t", "16"},
			{"SELECT v FROM t FOR KEY SHARE", "15"}, {"SELECT v FROM t FOR SHARE", "15"}, {"SELECT v FROM t FOR NO KEY UPDATE", "15"}, {"SELECT v FROM t FOR UPDATE", "15"},
			{"/*é*/ SELECT v FROM public.t FOR KEY SHARE", "21"}, {"/*é*/ SELECT v FROM public.t FOR SHARE", "21"}, {"/*é*/ SELECT v FROM public.t FOR NO KEY UPDATE", "21"}, {"/*é*/ SELECT v FROM public.t FOR UPDATE", "21"},
		} {
			t.Run(phase+"/"+tc.q, func(t *testing.T) {
				db := deadlockPublicDB(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				t.Cleanup(cancel)
				a := deadlockPublicConn(t, ctx, db)
				b := deadlockPublicConn(t, ctx, db)
				deadlockPublicExec(t, ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
				deadlockPublicExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
				var cached *sql.Stmt
				if phase == "cached" {
					var err error
					cached, err = b.PrepareContext(ctx, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { cached.Close() })
				}
				for _, c := range []*sql.Conn{a, b} {
					deadlockPublicExec(t, ctx, c, "BEGIN", 0)
					t.Cleanup(func() { deadlockPublicRollback(c) })
				}
				deadlockPublicExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
				deadlockPublicExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
				first := deadlockPublicWorker(t, ctx, 0, func(ctx context.Context) (sql.Result, error) {
					return a.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
				})
				second := deadlockPublicWorker(t, ctx, 1, func(ctx context.Context) (sql.Result, error) {
					switch phase {
					case "prepare":
						s, err := b.PrepareContext(ctx, tc.q)
						if s != nil {
							s.Close()
							if err != nil {
								return nil, fmt.Errorf("failed Prepare returned a handle")
							}
						}
						return nil, err
					case "cached":
						return cached.ExecContext(ctx)
					default:
						return b.ExecContext(ctx, tc.q)
					}
				})
				outcomes := []deadlockPublicOutcome{first(), second()}
				victim := -1
				for i, o := range outcomes {
					if o.err != nil {
						if victim >= 0 {
							t.Fatalf("two victims: %v %v", outcomes[0].err, outcomes[1].err)
						}
						victim = i
						position := ""
						if i == 1 && phase != "cached" {
							position = tc.pos
						}
						deadlockPublicError(t, o.err, position)
					}
				}
				if victim < 0 {
					t.Fatal("no victim")
				}
				deadlockPublicAborted(t, ctx, []*sql.Conn{a, b}[victim])
				deadlockPublicExec(t, ctx, []*sql.Conn{a, b}[victim], "ROLLBACK", 0)
				deadlockPublicExec(t, ctx, []*sql.Conn{a, b}[1-victim], "ROLLBACK", 0)
			})
		}
	}
}

func deadlockPublicSoftQueue(t *testing.T, db *sql.DB, observe func(context.Context, *sql.Conn, *sql.Conn, *sql.Conn, int)) {
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
	deadlockPublicRows(t, ctx, a, "SELECT v FROM t", [][]int64{{10}})
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

func TestRelationDeadlockPublicTable(t *testing.T)       { deadlockPublicTable(t) }
func TestRelationDeadlockPublicRowsMixed(t *testing.T)   { deadlockPublicRowsMixed(t) }
func TestRelationDeadlockPublicPreparation(t *testing.T) { deadlockPublicPreparation(t) }
func TestRelationDeadlockPublicSoftQueue(t *testing.T) {
	deadlockPublicSoftQueue(t, deadlockPublicDB(t), nil)
}
