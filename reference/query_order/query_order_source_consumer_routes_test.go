package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
			ordered := fixture.Select("SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(6)}, {int64(1), int64(3)}, {int64(4), int64(2)}, {int64(3), nil}})
			for _, s := range []fixture.Step{ordered,
				fixture.Select("SELECT id FROM t ORDER BY a ASC NULLS FIRST,b DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}, {int64(4)}}),
				fixture.Select("SELECT id,a FROM t ORDER BY 2 DESC NULLS LAST,1", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(4), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}),
			} {
				run(s)
				run(fixture.Prepared(s))
			}
			q := "SELECT id FROM t ORDER BY a*$1 NULLS LAST,id"
			stmt, err := b.DB.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				for i, arg := range []any{1, -1, nil, 1} {
					ids := []int64{2, 1, 4, 3}
					if i == 1 {
						ids = []int64{1, 4, 2, 3}
					}
					if i == 2 {
						ids = []int64{1, 2, 3, 4}
					}
					rows, err := stmt.Query(arg)
					fixture.StringCoreRetainedRows(t, rows, err, []string{"id"}, []string{"INT4"}, referenceLimitIDs(ids...), false)
				}
			}()
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT id FROM t ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "27"},
				{"SELECT a AS x,b AS x FROM t ORDER BY x", "42702", `ORDER BY "x" is ambiguous`, "38"},
			} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				s.ErrorStage = "prepare"
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE TABLE v(id INT)", 0))
			run(fixture.Exec("INSERT INTO v VALUES(1),(2),(3)", 3))
			for _, key := range []string{"n", "1", "nextval('s')"} {
				run(fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
				run(fixture.Select("SELECT nextval('s') AS n FROM v ORDER BY "+key+" DESC", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}}))
				var next int64
				if err := b.DB.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
					t.Fatal(next, err)
				}
				run(fixture.Exec("DROP SEQUENCE s", 0))
			}
			run(fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
			run(fixture.Select("SELECT id,nextval('s') AS n FROM v ORDER BY id DESC", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(1)}, {int64(2), int64(2)}, {int64(1), int64(3)}}))
			var next int64
			if err := b.DB.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
				t.Fatal(next, err)
			}
			run(fixture.Exec("INSERT INTO v VALUES(4),(5)", 2))
			run(fixture.Exec("CREATE SEQUENCE repeated_s START WITH 1", 0))
			run(fixture.Exec("CREATE VIEW repeated_order AS SELECT id FROM v WHERE id>2 ORDER BY nextval('repeated_s'),nextval('repeated_s') DESC", 0))
			s := fixture.Select("SELECT id FROM repeated_order ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}, {int64(5)}})
			run(s)
			if err := b.DB.QueryRow("SELECT nextval('repeated_s')").Scan(&next); err != nil || next != 4 {
				t.Fatal("repeated hidden filtered keys", next, err)
			}
			stmt, err = b.DB.Prepare(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			fixture.StringCoreRetainedRows(t, rows, err, s.Columns, s.Types, s.Rows, false)
			stmt.Close()
			if err := b.DB.QueryRow("SELECT nextval('repeated_s')").Scan(&next); err != nil || next != 8 {
				t.Fatal("fresh repeated hidden keys", next, err)
			}
			if !b.PG {
				held, err := b.DB.Query(ordered.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("INSERT INTO t VALUES(5,9,9)", 1))
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fixture.StringCoreRetainedRows(t, held, nil, ordered.Columns, ordered.Types, ordered.Rows, false)
			}
			run(ordered)
			run(fixture.Exec("CREATE TABLE d(id INT)", 0))
			run(fixture.Exec("INSERT INTO d VALUES(9)", 1))
			s = fixture.ImplicitError("INSERT INTO d SELECT id FROM t ORDER BY a,1/(id-4)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT id FROM d ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}

// Full empty/populated VOID analysis route union, including projected sequence
// non-evaluation, is intentionally not replaced by the populated-only control.
func TestReferenceQueryOrderVoidSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE order_void_t(id INT)", 0))
			run(fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
			seed := func() {
				var v any
				if err := c.QueryRowContext(p.Ctx, "SELECT setseed(.25)").Scan(&v); err != nil {
					t.Fatal(err)
				}
			}
			draw := func() float64 {
				var v float64
				if err := c.QueryRowContext(p.Ctx, "SELECT random()").Scan(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			seed()
			draw()
			continuation := draw()
			for _, populated := range []bool{false, true} {
				if populated {
					run(fixture.Exec("INSERT INTO order_void_t VALUES(1),(2),(3)", 3))
				}
				for _, tc := range []struct{ q, position string }{
					{"SELECT id FROM order_void_t ORDER BY setseed(0)", "38"},
					{"SELECT setseed(0) AS v FROM order_void_t ORDER BY v", "51"},
					{"SELECT setseed(0) FROM order_void_t ORDER BY 1", "46"},
					{"SELECT nextval('s') FROM order_void_t ORDER BY setseed(0)", "48"},
				} {
					for _, prepared := range []bool{false, true} {
						run(fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
						seed()
						draw()
						s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query.", Position: tc.position})
						s.SelectRows = true
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						run(s)
						if got := draw(); got != continuation {
							t.Fatal("VOID analysis changed random continuation", got, continuation)
						}
						var next int64
						if err := c.QueryRowContext(p.Ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 1 {
							t.Fatal("VOID analysis evaluated output", next, err)
						}
					}
				}
			}
		})
	}
}
