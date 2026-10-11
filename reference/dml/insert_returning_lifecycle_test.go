package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// pq may stream an output prefix before a later executor error. Native promises
// eager failure and no cursor. Both routes assert the same full diagnostic and
// stored sentinel; draining is not permission to accept partial native results.
func insertReturningFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, prepared, query bool, want fixture.Diagnostic) {
	t.Helper()
	want.Severity = "ERROR"
	want.SeverityUnlocalized = "ERROR"
	if !query {
		s := fixture.ImplicitError(q, want)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		return
	}
	rows, closeStmt, err := fixture.TransactionQuery(p.Ctx, x, q, prepared)
	if closeStmt != nil {
		defer closeStmt()
	}
	if rows != nil {
		defer rows.Close()
		if !b.PG {
			t.Fatal("native failed command exposed cursor")
		}
		cols, e := rows.Columns()
		if e != nil {
			t.Fatal(e)
		}
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range dest {
			dest[i] = &values[i]
		}
		for rows.Next() {
			if e = rows.Scan(dest...); e != nil {
				t.Fatal(e)
			}
		}
		err = rows.Err()
		rows.Close()
	}
	fixture.AssertDiagnostic(t, err, &want)
}
func TestReferenceInsertReturningParameters(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
		s := fixture.Select("INSERT INTO t VALUES(1,'a') RETURNING $1 AS label", []string{"label"}, []string{"TEXT"}, [][]any{{"fresh"}}, "fresh")
		s.Prepared = prepared
		run(s)
		s = fixture.Select("INSERT INTO t VALUES($1,$2) RETURNING $1,id,$2,v,$3 AS output_only", []string{"?column?", "id", "?column?", "v", "output_only"}, []string{"INT4", "INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(2), int64(2), "b", "b", nil}}, 2, "b", nil)
		s.Prepared = prepared
		run(s)
	})
}
func TestReferenceInsertReturningPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s START 101 CACHE 1", 0))
			run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v TEXT DEFAULT 'd')", 0))
			q := "INSERT INTO t(id) VALUES($1) RETURNING old.id,new.id,$2 AS label,nextval('s') AS draw"
			held, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT is_called FROM s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			for i, label := range []any{"one", nil} {
				s := fixture.Select(q, []string{"id", "id", "label", "draw"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{nil, int64(i + 1), label, int64(101 + i)}}, i+1, label)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: q}, b.Recorder, s)
			}
			run(fixture.Exec("PREPARE ins(INT,TEXT) AS INSERT INTO t(id) VALUES($1) RETURNING old.id,new.id,$2 AS label", 0))
			run(fixture.Select("EXECUTE ins(3,'sql')", []string{"id", "id", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{nil, int64(3), "sql"}}))
			run(fixture.Exec("DEALLOCATE ins", 0))
			run(fixture.Exec("PREPARE inferred AS INSERT INTO t(id) VALUES(4) RETURNING $1 AS label", 0))
			run(fixture.Select("EXECUTE inferred('inferred')", []string{"label"}, []string{"TEXT"}, [][]any{{"inferred"}}))
			run(fixture.Exec("DEALLOCATE inferred", 0))
			star, err := a.PrepareContext(p.Ctx, "INSERT INTO t(id) VALUES(5) RETURNING *")
			if err != nil {
				t.Fatal(err)
			}
			defer star.Close()
			run(fixture.Exec("ALTER TABLE t RENAME COLUMN v TO renamed", 0))
			rows, err := star.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("changed shape exposed cursor")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"})
			run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
		})
	}
}
func TestReferenceInsertReturningMetadata(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,v VARCHAR(5),n NUMERIC(8,2),ts TIMESTAMP(3),b BYTEA)", 0))
		for _, empty := range []bool{false, true} {
			q := `INSERT INTO t VALUES(1,'word',12.30,'2024-01-02 03:04:05.123','\x0102')`
			expected := [][]any{{nil, int64(1), "word", "12.30", time.Date(2024, 1, 2, 3, 4, 5, 123000000, time.UTC), "\x01\x02"}}
			if empty {
				q = "INSERT INTO t SELECT 2,'word',12.30,'2024-01-02 03:04:05.123'::timestamp,'\\x0102'::bytea WHERE FALSE"
				expected = [][]any{}
			}
			s := insertReturningBag(q+" RETURNING old.id,new.id,v,n,ts,b", []string{"id", "id", "v", "n", "ts", "b"}, []string{"INT4", "INT4", "VARCHAR", "NUMERIC", "TIMESTAMP", "BYTEA"}, expected, prepared)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				m, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				if n, k := m[0].Nullable(); (!b.PG && (!k || !n)) || (b.PG && k) {
					t.Fatal("OLD nullable", n, k)
				}
				if n, k := m[1].Nullable(); (!b.PG && (!k || n)) || (b.PG && k) {
					t.Fatal("NEW nullable", n, k)
				}
				if n, k := m[2].Length(); !k || n != 5 {
					t.Fatal("varchar length", n, k)
				}
				if n, scale, k := m[3].DecimalSize(); !k || n != 8 || scale != 2 {
					t.Fatal("numeric size", n, scale, k)
				}
				want := reflect.TypeOf("")
				if b.PG {
					want = reflect.TypeOf((*any)(nil)).Elem()
				}
				if m[3].ScanType() != want {
					t.Fatal("numeric scan", m[3].ScanType(), want)
				}
				if _, _, k := m[4].DecimalSize(); k {
					t.Fatal("timestamp decimal size must be unknown")
				}
			}
			run(s)
		}
	})
}
func TestReferenceInsertReturningAtomicity(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(9,'sentinel')", 1))
		insertReturningPrecanceled(t, p, b, prepared)
		for _, query := range []bool{false, true} {
			for _, q := range []string{
				"INSERT INTO t VALUES(1,'1'),(2,'bad') RETURNING v::INT",
				"INSERT INTO t VALUES(1,'1'),(2,'2') RETURNING 10/(2-id)",
				"INSERT INTO t VALUES(9,'1'),(2,'bad') ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING v::INT",
			} {
				want := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}
				if q == "INSERT INTO t VALUES(1,'1'),(2,'2') RETURNING 10/(2-id)" {
					want = fixture.Diagnostic{Code: "22012", Message: "division by zero"}
				}
				insertReturningFailure(t, p, b, a, q, prepared, query, want)
				run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "sentinel"}}))
			}
		}
		for _, savepoint := range []bool{false, true} {
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(8,'kept')", 1)
			if savepoint {
				fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT kept", 0)
			}
			insertReturningFailure(t, p, b, tx, "INSERT INTO t VALUES(1,'1'),(2,'2') RETURNING 10/(2-id)", prepared, true, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			if savepoint {
				fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO SAVEPOINT kept", 0)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(9)}}))
			} else {
				s := fixture.ImplicitError("SELECT id FROM t", fixture.TransactionAborted())
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
func TestReferenceInsertReturningVolatility(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE s START 101 CACHE 1", 0))
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT NOT NULL)", 0))
		s := insertReturningBag("INSERT INTO t VALUES(1,10),(2,20) RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(101)}, {int64(102)}}, prepared)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
		for _, q := range []string{"INSERT INTO t VALUES(1,99) ON CONFLICT DO NOTHING RETURNING nextval('s')", "INSERT INTO t VALUES(1,99) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE FALSE RETURNING nextval('s')", "INSERT INTO t VALUES(1,99) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE NULL RETURNING nextval('s')"} {
			s := fixture.Select(q, []string{"nextval"}, []string{"INT8"}, [][]any{})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(103)}}))
		// Hypothesis: output for proposal 3 is demanded before proposal 4's failing
		// constraint. The pinned checkpoint must resolve this, not native output.
		insertReturningFailure(t, p, b, a, "INSERT INTO t VALUES(3,30),(4,NULL) RETURNING nextval('s')", prepared, true, fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (4, null).", Schema: "public", Table: "t", Column: "v"})
		run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(105)}}))
		// Output and the next proposal share a sequence: one expression per side
		// avoids assumptions about sibling target-list order. VALUES has fixed order.
		s = fixture.Select("INSERT INTO t VALUES(3,nextval('s')),(4,nextval('s')) RETURNING v,nextval('s') AS draw", []string{"v", "draw"}, []string{"INT4", "INT8"}, [][]any{{int64(106), int64(107)}, {int64(108), int64(109)}})
		s.Prepared = prepared
		s.Bag = true
		run(s)
		insertReturningFailure(t, p, b, a, "INSERT INTO t VALUES(5,50),(6,60) RETURNING CASE WHEN id=5 THEN nextval('s') ELSE 1/(id-id) END", prepared, true, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(111)}}))
		run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
	})
}
