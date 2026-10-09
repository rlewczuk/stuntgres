package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
)

func TestReferenceQueryOrderSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
			ordered := referenceSelect("SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(6)}, {int64(1), int64(3)}, {int64(4), int64(2)}, {int64(3), nil}})
			for _, s := range []referenceStep{ordered,
				referenceSelect("SELECT id FROM t ORDER BY a ASC NULLS FIRST,b DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}, {int64(4)}}),
				referenceSelect("SELECT id,a FROM t ORDER BY 2 DESC NULLS LAST,1", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(4), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}),
			} {
				run(s)
				run(referencePrepared(s))
			}
			q := "SELECT id FROM t ORDER BY a*$1 NULLS LAST,id"
			stmt, err := b.db.Prepare(q)
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
					stringCoreRetainedRows(t, rows, err, []string{"id"}, []string{"INT4"}, referenceLimitIDs(ids...), false)
				}
			}()
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT id FROM t ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "27"},
				{"SELECT a AS x,b AS x FROM t ORDER BY x", "42702", `ORDER BY "x" is ambiguous`, "38"},
			} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.selectRows = true
				s.errorStage = "execute"
				run(s)
				s.errorStage = "prepare"
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE v(id INT)", 0))
			run(referenceExec("INSERT INTO v VALUES(1),(2),(3)", 3))
			for _, key := range []string{"n", "1", "nextval('s')"} {
				run(referenceExec("CREATE SEQUENCE s START WITH 1", 0))
				run(referenceSelect("SELECT nextval('s') AS n FROM v ORDER BY "+key+" DESC", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}}))
				var next int64
				if err := b.db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
					t.Fatal(next, err)
				}
				run(referenceExec("DROP SEQUENCE s", 0))
			}
			run(referenceExec("CREATE SEQUENCE s START WITH 1", 0))
			run(referenceSelect("SELECT id,nextval('s') AS n FROM v ORDER BY id DESC", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(1)}, {int64(2), int64(2)}, {int64(1), int64(3)}}))
			var next int64
			if err := b.db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
				t.Fatal(next, err)
			}
			run(referenceExec("INSERT INTO v VALUES(4),(5)", 2))
			run(referenceExec("CREATE SEQUENCE repeated_s START WITH 1", 0))
			run(referenceExec("CREATE VIEW repeated_order AS SELECT id FROM v WHERE id>2 ORDER BY nextval('repeated_s'),nextval('repeated_s') DESC", 0))
			s := referenceSelect("SELECT id FROM repeated_order ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}, {int64(5)}})
			run(s)
			if err := b.db.QueryRow("SELECT nextval('repeated_s')").Scan(&next); err != nil || next != 4 {
				t.Fatal("repeated hidden filtered keys", next, err)
			}
			stmt, err = b.db.Prepare(s.query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			stringCoreRetainedRows(t, rows, err, s.columns, s.types, s.rows, false)
			stmt.Close()
			if err := b.db.QueryRow("SELECT nextval('repeated_s')").Scan(&next); err != nil || next != 8 {
				t.Fatal("fresh repeated hidden keys", next, err)
			}
			if !b.pg {
				held, err := b.db.Query(ordered.query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("INSERT INTO t VALUES(5,9,9)", 1))
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				stringCoreRetainedRows(t, held, nil, ordered.columns, ordered.types, ordered.rows, false)
			}
			run(ordered)
			run(referenceExec("CREATE TABLE d(id INT)", 0))
			run(referenceExec("INSERT INTO d VALUES(9)", 1))
			s = referenceImplicitError("INSERT INTO d SELECT id FROM t ORDER BY a,1/(id-4)", referenceDiagnostic{Code: "22012", Message: "division by zero"})
			s.errorStage = "execute"
			run(s)
			run(referenceSelect("SELECT id FROM d ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}

// Full empty/populated VOID analysis route union, including projected sequence
// non-evaluation, is intentionally not replaced by the populated-only control.
func TestReferenceQueryOrderVoidSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			run(referenceExec("CREATE TABLE order_void_t(id INT)", 0))
			run(referenceExec("CREATE SEQUENCE s START WITH 1", 0))
			seed := func() {
				var v any
				if err := c.QueryRowContext(p.ctx, "SELECT setseed(.25)").Scan(&v); err != nil {
					t.Fatal(err)
				}
			}
			draw := func() float64 {
				var v float64
				if err := c.QueryRowContext(p.ctx, "SELECT random()").Scan(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			seed()
			draw()
			continuation := draw()
			for _, populated := range []bool{false, true} {
				if populated {
					run(referenceExec("INSERT INTO order_void_t VALUES(1),(2),(3)", 3))
				}
				for _, tc := range []struct{ q, position string }{
					{"SELECT id FROM order_void_t ORDER BY setseed(0)", "38"},
					{"SELECT setseed(0) AS v FROM order_void_t ORDER BY v", "51"},
					{"SELECT setseed(0) FROM order_void_t ORDER BY 1", "46"},
					{"SELECT nextval('s') FROM order_void_t ORDER BY setseed(0)", "48"},
				} {
					for _, prepared := range []bool{false, true} {
						run(referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
						seed()
						draw()
						s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query.", Position: tc.position})
						s.selectRows = true
						s.errorStage = "execute"
						if prepared {
							s = referencePrepared(s)
							s.errorStage = "prepare"
						}
						run(s)
						if got := draw(); got != continuation {
							t.Fatal("VOID analysis changed random continuation", got, continuation)
						}
						var next int64
						if err := c.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 1 {
							t.Fatal("VOID analysis evaluated output", next, err)
						}
					}
				}
			}
		})
	}
}
