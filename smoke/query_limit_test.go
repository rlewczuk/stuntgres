package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"runtime"
	"stuntgres"
	"testing"
	"time"
)

func TestQueryLimitSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := db.Exec(q)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != want {
			t.Fatal(q, n, want, err)
		}
	}
	exec("CREATE TABLE t(id INT NOT NULL,v INT)", 0)
	exec("INSERT INTO t VALUES(1,2),(2,0),(3,3),(4,4)", 4)
	for _, q := range []string{"SELECT id FROM t ORDER BY id LIMIT 2 OFFSET 1", "SELECT id FROM t ORDER BY id OFFSET 1 ROW FETCH NEXT 2 ROWS ONLY"} {
		rows, err := db.Query(q)
		orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		stmt.Close()
	}
	stmt, err := db.Prepare("SELECT id FROM t ORDER BY id LIMIT $1 OFFSET $2")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		count, offset any
		want          [][]any
	}{{2, 1, [][]any{{int64(2)}, {int64(3)}}}, {0, 0, [][]any{}}, {nil, 3, [][]any{{int64(4)}}}} {
		rows, err := stmt.Query(tc.count, tc.offset)
		orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, tc.want)
	}
	badRows, badErr := stmt.Query(1.5, 1)
	if badRows != nil {
		badRows.Close()
		t.Fatal("fractional BIGINT input exposed cursor")
	}
	if err := typeCoreDiagnostic(badErr, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "1.5"`, Context: "unnamed portal parameter $1 = '...'"}); err != nil {
		t.Fatal(err)
	}
	exec("CREATE TABLE typed(f DOUBLE PRECISION)", 0)
	exec("INSERT INTO typed VALUES(2.5)", 1)
	typedQuery := "SELECT t.id FROM t,typed WHERE typed.f=$1 ORDER BY t.id LIMIT $1 OFFSET 1"
	floatRows, err := db.Query(typedQuery, float64(2.5))
	orderSmokeRows(t, floatRows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
	typedStmt, err := db.Prepare(typedQuery)
	if err != nil {
		t.Fatal(err)
	}
	floatRows, err = typedStmt.Query(float64(2.5))
	orderSmokeRows(t, floatRows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
	typedStmt.Close()
	for _, suffix := range []string{"LIMIT ALL", "LIMIT NULL", "OFFSET NULL"} {
		rows, err := db.Query("SELECT id FROM t ORDER BY id " + suffix)
		orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}})
	}
	rows, err := db.Query("SELECT id FROM t ORDER BY id FETCH FIRST ROW ONLY")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
	rows, err = db.Query("SELECT 10/v AS n FROM t LIMIT 1")
	orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT4"}, [][]any{{int64(5)}})
	rows, err = db.Query("SELECT 10/v AS n FROM t ORDER BY id LIMIT 0")
	orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT4"}, [][]any{})
	rows, err = db.Query("SELECT 10/v AS n FROM t ORDER BY id LIMIT 1")
	var diagnostic *stuntgres.Error
	if rows != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		if rows != nil {
			rows.Close()
		}
		t.Fatal(rows, err)
	}
	exec("CREATE SEQUENCE s START WITH 1", 0)
	exec("CREATE VIEW drawn WITH (security_barrier=true) AS SELECT nextval('s') AS n FROM t", 0)
	for _, q := range []string{"SELECT n FROM drawn LIMIT 0", "SELECT n FROM drawn LIMIT 1"} {
		rows, err = db.Query(q)
		want := [][]any{}
		if q == "SELECT n FROM drawn LIMIT 1" {
			want = [][]any{{int64(1)}}
		}
		orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT8"}, want)
	}
	var next int64
	if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 2 {
		t.Fatal(next, err)
	}
	exec("CREATE SEQUENCE bounds_s START WITH 1", 0)
	bounds, err := db.Prepare("SELECT id FROM t ORDER BY id LIMIT nextval('bounds_s') OFFSET nextval('bounds_s')")
	if err != nil {
		t.Fatal(err)
	}
	var called bool
	if err := db.QueryRow("SELECT is_called FROM bounds_s").Scan(&called); err != nil || called {
		bounds.Close()
		t.Fatal("Prepare evaluated bound", called, err)
	}
	rows, err = bounds.Query()
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
	bounds.Close()
	if err := db.QueryRow("SELECT nextval('bounds_s')").Scan(&next); err != nil || next != 3 {
		t.Fatal("bounds were not OFFSET then LIMIT once", next, err)
	}
	exec("CREATE SEQUENCE demand_s START WITH 1", 0)
	rows, err = db.Query("SELECT nextval('demand_s') AS n FROM t ORDER BY id OFFSET 1 LIMIT 1")
	orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}})
	if err := db.QueryRow("SELECT nextval('demand_s')").Scan(&next); err != nil || next != 3 {
		t.Fatal(next, err)
	}
	for _, tc := range []struct {
		suffix string
		want   [][]any
		next   int64
	}{{"LIMIT 0", [][]any{}, 1}, {"LIMIT 1", [][]any{{int64(1)}}, 2}, {"OFFSET 1", [][]any{}, 2}} {
		exec("ALTER SEQUENCE demand_s RESTART WITH 1", 0)
		rows, err = db.Query("SELECT nextval('demand_s') " + tc.suffix)
		orderSmokeRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, tc.want)
		if err := db.QueryRow("SELECT nextval('demand_s')").Scan(&next); err != nil || next != tc.next {
			t.Fatal(tc.suffix, next, err)
		}
	}
	exec("CREATE SEQUENCE cache_s START WITH 1", 0)
	rows, err = db.Query("SELECT nextval('cache_s') AS n FROM t ORDER BY n DESC OFFSET 1 LIMIT 2")
	orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}})
	if err := db.QueryRow("SELECT nextval('cache_s')").Scan(&next); err != nil || next != 5 {
		t.Fatal("cache redraw", next, err)
	}
	for _, tc := range []struct{ q, code, message, position string }{
		{"SELECT id FROM t LIMIT TRUE", "42804", "argument of LIMIT must be type bigint, not type boolean", "24"},
		{"SELECT id FROM t LIMIT id", "42P10", "argument of LIMIT must not contain variables", "24"},
		{"SELECT id FROM t WHERE FALSE LIMIT -1", "2201W", "LIMIT must not be negative", ""},
	} {
		rows, err = db.Query(tc.q)
		var d *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if rows != nil || !errors.As(err, &d) || *d != want {
			if rows != nil {
				rows.Close()
			}
			t.Fatal(tc.q, err, d, want)
		}
	}
	cancelCtx, stop := context.WithCancel(ctx)
	cancelled, err := db.QueryContext(cancelCtx, "SELECT nextval('cache_s') AS n FROM t LIMIT 2")
	if err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	if cancelled.Next() || !errors.Is(cancelled.Err(), context.Canceled) {
		cancelled.Close()
		t.Fatal("public cancellation did not stick", cancelled.Err())
	}
	if cancelled.Next() {
		cancelled.Close()
		t.Fatal("cancelled Next resumed SQL")
	}
	cancelled.Close()
	if err := db.QueryRow("SELECT nextval('cache_s')").Scan(&next); err != nil || next != 8 {
		t.Fatal("public Next evaluated SQL", next, err)
	}
	exec("CREATE VIEW sliced AS SELECT id FROM t ORDER BY id OFFSET 1 FETCH FIRST 2 ROWS ONLY", 0)
	exec("ALTER TABLE t RENAME COLUMN id TO renamed", 0)
	held, err := db.Query("SELECT id FROM sliced LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		held.Close()
		t.Fatal(err)
	}
	exec("INSERT INTO t VALUES(5,5)", 1)
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		held.Close()
		t.Fatal(err)
	}
	orderSmokeRows(t, held, nil, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}})
	rows, err = db.Query("SELECT id FROM sliced")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
	exec("CREATE TABLE dst(id INT)", 0)
	exec("INSERT INTO dst VALUES(7),(8),(9) OFFSET 1 FETCH FIRST ROW ONLY", 1)
	exec("INSERT INTO dst SELECT renamed FROM t ORDER BY renamed OFFSET 1 LIMIT 2", 2)
	rows, err = db.Query("SELECT id FROM dst ORDER BY id")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(8)}})
	exec("CREATE TABLE small(id SMALLINT)", 0)
	exec("INSERT INTO small VALUES(1),(32768) LIMIT 1", 1)
	_, err = db.Exec("INSERT INTO small VALUES(2),(32768) LIMIT 2")
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" {
		t.Fatal("selected assignment did not fail atomically", err)
	}
	rows, err = db.Query("SELECT id FROM small")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT2"}, [][]any{{int64(1)}})
	exec("CREATE TABLE failing_default(id INT,n INT DEFAULT 1/0)", 0)
	for _, prepared := range []bool{false, true} {
		for _, suffix := range []string{"LIMIT 0", "LIMIT 1"} {
			query := "INSERT INTO failing_default(id) SELECT id FROM small " + suffix
			var result sql.Result
			if prepared {
				statement, prepareErr := db.PrepareContext(ctx, query)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				result, err = statement.ExecContext(ctx)
				statement.Close()
			} else {
				result, err = db.ExecContext(ctx, query)
			}
			if result != nil {
				t.Fatal("default preflight exposed result", result)
			}
			if diagnosticErr := typeCoreDiagnostic(err, stuntgres.Error{Code: "22012", Message: "division by zero"}); diagnosticErr != nil {
				t.Fatal(query, diagnosticErr)
			}
			rows, err = db.QueryContext(ctx, "SELECT id FROM failing_default")
			orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{})
		}
	}
	own, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := own.ExecContext(ctx, "INSERT INTO small VALUES(4)"); err != nil {
		own.Rollback()
		t.Fatal(err)
	}
	result, err := own.ExecContext(ctx, "INSERT INTO small SELECT id FROM small ORDER BY id DESC LIMIT 1")
	if err != nil {
		own.Rollback()
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		own.Rollback()
		t.Fatal(n, err)
	}
	if err := own.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT id FROM small ORDER BY id")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT2"}, [][]any{{int64(1)}, {int64(4)}, {int64(4)}})
}

func TestQueryLimitLockSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(4)
	for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,v INT)", "INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		"CREATE TABLE u(id INT)", "INSERT INTO u VALUES(1),(2),(3),(4)",
		"CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE",
		"CREATE VIEW ordered_marked AS SELECT id,v FROM t ORDER BY id FOR UPDATE",
		"CREATE VIEW limited_marked AS SELECT id,v FROM t ORDER BY id LIMIT 1 FOR UPDATE",
		"CREATE VIEW offset_marked AS SELECT id,v FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE",
		"CREATE VIEW zero_marked AS SELECT id,v FROM t LIMIT 0 FOR UPDATE",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, q := range []string{"SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE", "SELECT id FROM t ORDER BY id LIMIT 0 FOR UPDATE"} {
		holder, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := holder.QueryContext(ctx, q)
		want := [][]any{}
		if q != "SELECT id FROM t ORDER BY id LIMIT 0 FOR UPDATE" {
			want = [][]any{{int64(2)}}
		}
		orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, want)
		for _, id := range []int{3, 4} {
			if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=$1", id); err != nil {
				holder.Rollback()
				t.Fatal("tail write blocked", id, err)
			}
		}
		if len(want) > 0 {
			blocked, stop := context.WithTimeout(ctx, 250*time.Millisecond)
			_, err := db.ExecContext(blocked, "UPDATE t SET v=v+1 WHERE id=1")
			stop()
			if !errors.Is(err, context.DeadlineExceeded) {
				holder.Rollback()
				t.Fatal("skipped-row writer was not cancelled", err)
			}
		} else {
			for _, id := range []int{1, 2} {
				if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=$1", id); err != nil {
					holder.Rollback()
					t.Fatal("zero held a row", id, err)
				}
			}
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=1"); err != nil {
			t.Fatal("cancelled waiter left a grant", err)
		}
	}
	for _, tc := range []struct {
		q, name, typ string
		want         [][]any
		free         []int
	}{
		{"SELECT t.id FROM t JOIN u ON t.id=u.id ORDER BY t.id OFFSET 1 LIMIT 1 FOR UPDATE", "id", "INT4", [][]any{{int64(2)}}, []int{3, 4}},
		{"SELECT count(*) FROM marked WHERE id=1", "count", "INT8", [][]any{{int64(1)}}, []int{2, 3, 4}},
		{"SELECT id FROM ordered_marked WHERE id=1", "id", "INT4", [][]any{{int64(1)}}, []int{2, 3, 4}},
		{"SELECT id FROM limited_marked WHERE id=2", "id", "INT4", [][]any{}, []int{2, 3, 4}},
		{"SELECT id FROM offset_marked WHERE id=3", "id", "INT4", [][]any{}, []int{3, 4}},
		{"SELECT count(*) FROM zero_marked", "count", "INT8", [][]any{{int64(0)}}, []int{1, 2, 3, 4}},
		{"SELECT id FROM marked ORDER BY id LIMIT 0", "id", "INT4", [][]any{}, []int{1, 2, 3, 4}},
	} {
		holder, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := holder.QueryContext(ctx, tc.q)
		orderSmokeRows(t, rows, err, []string{tc.name}, []string{tc.typ}, tc.want)
		for _, id := range tc.free {
			if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=$1", id); err != nil {
				holder.Rollback()
				t.Fatal(tc.q, "free frontier blocked", id, err)
			}
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=1"); err != nil {
			t.Fatal("local frontier retained after rollback", err)
		}
	}
	holder, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := holder.QueryContext(ctx, "SELECT id FROM marked ORDER BY id OFFSET 1 LIMIT 1")
	orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}})
	blocked, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	_, err = db.ExecContext(blocked, "UPDATE t SET v=v+1 WHERE id=4")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		holder.Rollback()
		t.Fatal("stored outer Sort tail writer was not cancelled", err)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=4"); err != nil {
		t.Fatal("stored tail cancellation cleanup", err)
	}
	for _, q := range []string{"CREATE TABLE cache_t(id INT,v INT)", "INSERT INTO cache_t VALUES(1,10),(2,20)", "CREATE SEQUENCE gate START WITH 1", "CREATE VIEW cache_marked AS SELECT id,v+1 AS val,nextval('gate') AS entered FROM cache_t FOR UPDATE"} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, prepared := range []bool{false, true} {
		for _, suffix := range []string{"", " LIMIT 1"} {
			if _, err := db.ExecContext(ctx, "UPDATE cache_t SET v=10 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "ALTER SEQUENCE gate RESTART WITH 1"); err != nil {
				t.Fatal(err)
			}
			query := "SELECT val FROM cache_marked ORDER BY val" + suffix
			var stmt *sql.Stmt
			if prepared {
				stmt, err = db.PrepareContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
			}
			holder, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := holder.QueryContext(ctx, "SELECT id FROM cache_t WHERE id=1 FOR UPDATE")
			orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
			if _, err := holder.ExecContext(ctx, "UPDATE cache_t SET v=30 WHERE id=1"); err != nil {
				holder.Rollback()
				t.Fatal(err)
			}
			type result struct {
				rows *sql.Rows
				err  error
			}
			done := make(chan result, 1)
			queryCtx, stopQuery := context.WithCancel(ctx)
			finished := false
			t.Cleanup(func() {
				stopQuery()
				holder.Rollback()
				if !finished {
					select {
					case abandoned := <-done:
						if abandoned.rows != nil {
							abandoned.rows.Close()
						}
					case <-time.After(5 * time.Second):
						t.Error("stored frontier query did not terminate")
					}
				}
				if stmt != nil {
					stmt.Close()
				}
			})
			go func() {
				var r *sql.Rows
				var e error
				if prepared {
					r, e = stmt.QueryContext(queryCtx)
				} else {
					r, e = db.QueryContext(queryCtx, query)
				}
				done <- result{r, e}
			}()
			// A real lower-view output proves that the old candidate was
			// captured before release. Exact wait/hold ownership is tested
			// internally; this adds no public testing hook or timed guess.
			for {
				var called bool
				if err := holder.QueryRowContext(ctx, "SELECT is_called FROM gate").Scan(&called); err != nil {
					holder.Rollback()
					t.Fatal(err)
				}
				if called {
					break
				}
				runtime.Gosched()
			}
			if err := holder.Commit(); err != nil {
				t.Fatal(err)
			}
			got := <-done
			finished = true
			want := [][]any{{int64(21)}, {int64(31)}}
			if suffix != "" {
				want = [][]any{{int64(21)}}
			}
			orderSmokeRows(t, got.rows, got.err, []string{"val"}, []string{"INT4"}, want)
			if stmt != nil {
				stmt.Close()
			}
		}
	}
}
