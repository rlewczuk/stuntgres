package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDefaultLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					record := backend.Recorder
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.DefaultReferenceRunStep(t, p, backend.DB, record, s)
					}
					run(fixture.Exec("CREATE TABLE d(id INT,i INT DEFAULT 11,b BOOL DEFAULT FALSE)", 0))
					run(fixture.Exec("INSERT INTO d(id) VALUES(0)", 1))
					held, err := backend.DB.PrepareContext(p.Ctx, "INSERT INTO d(id) VALUES($1)")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					defaults, err := backend.DB.PrepareContext(p.Ctx, "INSERT INTO d VALUES($1,DEFAULT,DEFAULT)")
					if err != nil {
						t.Fatal(err)
					}
					defer defaults.Close()
					run(fixture.Exec("ALTER TABLE d ALTER COLUMN i SET DEFAULT 7", 0))
					for index, s := range []*sql.Stmt{held, defaults} {
						r, err := s.ExecContext(p.Ctx, index+1)
						if err != nil {
							t.Fatal(err)
						}
						if n, e := r.RowsAffected(); e != nil || n != 1 {
							t.Fatal(n, e)
						}
					}
					run(fixture.Exec("INSERT INTO d(id) VALUES(3)", 1))
					run(fixture.Exec("INSERT INTO d VALUES(4,DEFAULT,DEFAULT),(5,99,TRUE),(6,NULL,NULL)", 3))
					run(fixture.Exec("INSERT INTO d(id) VALUES(1)", 1))
					run(fixture.Exec("ALTER TABLE ONLY(d) ALTER i SET DEFAULT 9", 0))
					r, err := held.ExecContext(p.Ctx, 7)
					if err != nil {
						t.Fatal(err)
					}
					if n, e := r.RowsAffected(); e != nil || n != 1 {
						t.Fatal(n, e)
					}
					run(fixture.Exec("ALTER TABLE d* ALTER i DROP DEFAULT", 0))
					run(fixture.Exec("ALTER TABLE ONLY d ALTER i DROP DEFAULT", 0))
					r, err = defaults.ExecContext(p.Ctx, 8)
					if err != nil {
						t.Fatal(err)
					}
					if n, e := r.RowsAffected(); e != nil || n != 1 {
						t.Fatal(n, e)
					}
					run(fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(0), int64(11), false}, {int64(1), int64(7), false}, {int64(1), int64(7), false}, {int64(2), int64(7), false}, {int64(3), int64(7), false}, {int64(4), int64(7), false}, {int64(5), int64(99), true}, {int64(6), nil, nil}, {int64(7), int64(9), false}, {int64(8), nil, false}}))
					for _, expr := range []string{"TRUE OR FALSE", "NOT FALSE", "NULL IS NULL"} {
						run(fixture.Exec("ALTER TABLE d ALTER b SET DEFAULT "+expr, 0))
						run(fixture.Exec("INSERT INTO d VALUES(9,NULL,DEFAULT)", 1))
						run(fixture.Select("SELECT b FROM d WHERE id=9", []string{"b"}, []string{"BOOL"}, [][]any{{true}}))
						run(fixture.Exec("DELETE FROM d WHERE id=9", 1))
					}
					run(fixture.Exec("CREATE TABLE q(i INT DEFAULT 1)", 0))
					run(fixture.Exec("ALTER TABLE public.q ALTER i SET DEFAULT 7", 0))
					run(fixture.Exec("INSERT INTO q DEFAULT VALUES", 1))
					run(fixture.Exec("ALTER TABLE q ALTER i DROP DEFAULT", 0))
					run(fixture.Exec("INSERT INTO q DEFAULT VALUES", 1))
					run(fixture.Select("SELECT * FROM q ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
					run(fixture.Exec("CREATE TABLE source(id INT)", 0))
					run(fixture.Exec("INSERT INTO source VALUES(10)", 1))
					run(fixture.Exec("ALTER TABLE d ALTER i SET DEFAULT 7", 0))
					run(fixture.Exec("INSERT INTO d(id) SELECT id FROM source", 1))
					run(fixture.Exec("INSERT INTO d(id) SELECT id FROM source WHERE id=999", 0))
					run(fixture.Select("SELECT i FROM d WHERE id=10", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}

func TestReferenceAlterDefaultOwnership(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"alter_default_owner", "alter_default_reader"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					record := backend.Recorder
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, record, s) }
					run(fixture.Exec("CREATE USER alter_default_owner", 0))
					run(fixture.Exec("CREATE USER alter_default_reader", 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION alter_default_owner", 0))
					fixture.WithPublicSchemaOwner(t, p, backend, "alter_default_owner", func() { run(fixture.Exec("CREATE TABLE t(id INT,i INT DEFAULT 7)", 0)) })
					run(fixture.Exec("INSERT INTO t VALUES(0,11)", 1))
					for index, q := range []string{"ALTER TABLE t ALTER i SET DEFAULT 'oops'", "ALTER TABLE t ALTER i DROP DEFAULT"} {
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						run(fixture.Exec("SET SESSION AUTHORIZATION alter_default_reader", 0))
						// Independently specified for each backend: ownership wins over
						// invalid SET input; utility Prepare succeeds, Execute denies.
						failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"})
						failure.ErrorStage = "execute"
						run(failure)
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						run(fixture.Exec("SET SESSION AUTHORIZATION alter_default_owner", 0))
						run(fixture.Exec(fmt.Sprintf("INSERT INTO t(id) VALUES(%d)", index*2+1), 1))
						run(fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES(%d,DEFAULT)", index*2+2), 1))
						want := [][]any{{int64(0), int64(11)}}
						for id := 1; id <= index*2+2; id++ {
							want = append(want, []any{int64(id), int64(7)})
						}
						run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, want))
					}
					run(fixture.Exec("ALTER TABLE t ALTER i SET DEFAULT 9", 0))
					run(fixture.Exec("INSERT INTO t(id) VALUES(5)", 1))
					run(fixture.Exec("ALTER TABLE t ALTER i DROP DEFAULT", 0))
					run(fixture.Exec("INSERT INTO t VALUES(6,DEFAULT)", 1))
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(11)}, {int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}, {int64(4), int64(7)}, {int64(5), int64(9)}, {int64(6), nil}}))
				})
			}
		})
	}
}

func TestReferenceAlterDefaultDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,i INT DEFAULT 7,b BOOL DEFAULT FALSE,v VARCHAR(2))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(0,11,FALSE,'ok')", 1))
			cases := []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"ALTER TABLE absent ALTER i SET DEFAULT 7", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`}},
				{"ALTER TABLE absent ALTER i DROP DEFAULT", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`}},
				{"ALTER TABLE t ALTER missing SET DEFAULT 'oops'", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
				{"ALTER TABLE t ALTER missing DROP DEFAULT", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
				{"ALTER TABLE t ALTER i SET DEFAULT TRUE", fixture.Diagnostic{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
				{"ALTER TABLE t ALTER i SET DEFAULT 'oops'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
				{"ALTER TABLE t ALTER i SET DEFAULT '2147483648'", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`}},
			}
			for _, expr := range []string{"i", "b", "CAST(i AS INT)", "CASE WHEN TRUE THEN 1 ELSE i END"} {
				cases = append(cases, struct {
					q    string
					want fixture.Diagnostic
				}{"ALTER TABLE t ALTER i SET DEFAULT " + expr, fixture.Diagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression"}})
			}
			for _, expr := range []string{"$1", "CAST($1 AS INT)", "CASE WHEN TRUE THEN 1 ELSE $1 END"} {
				cases = append(cases, struct {
					q    string
					want fixture.Diagnostic
				}{"ALTER TABLE t ALTER i SET DEFAULT " + expr, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1"}})
			}
			for _, col := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
				for _, action := range []string{"SET DEFAULT 7", "DROP DEFAULT"} {
					cases = append(cases, struct {
						q    string
						want fixture.Diagnostic
					}{"ALTER TABLE t ALTER " + col + " " + action, fixture.Diagnostic{Code: "0A000", Message: `cannot alter system column "` + col + `"`}})
				}
			}
			for _, tc := range cases {
				s := fixture.ImplicitError(tc.q, tc.want)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("INSERT INTO t(id) VALUES(1)", 1))
				run(fixture.Select("SELECT i,b FROM t WHERE id=1", []string{"i", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(7), false}}))
				run(fixture.Exec("DELETE FROM t WHERE id=1", 1))
				run(fixture.Select("SELECT * FROM t", []string{"id", "i", "b", "v"}, []string{"INT4", "INT4", "BOOL", "VARCHAR"}, [][]any{{int64(0), int64(11), false, "ok"}}))
			}
			run(fixture.Exec("CREATE SEQUENCE s", 0))
			for _, action := range []string{"SET DEFAULT 7", "DROP DEFAULT"} {
				run(fixture.ImplicitError("ALTER TABLE s ALTER i "+action, fixture.Diagnostic{Code: "42809", Message: `ALTER action ALTER COLUMN ... SET DEFAULT cannot be performed on relation "s"`, Detail: "This operation is not supported for sequences."}))
			}
		})
	}
}

func TestReferenceAlterDefaultDeferred(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ typ, expr, code, message string }{{"INT", "2147483648", "22003", "integer out of range"}, {"INT", "1/0", "22012", "division by zero"}, {"VARCHAR(2)", "'long'::text", "22001", "value too long for type character varying(2)"}} {
			t.Run(fmt.Sprint(prepared)+tc.expr, func(t *testing.T) {
				p := fixture.NewScenario(t)
				run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,i "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO t VALUES(0,NULL)", 1))
				run(fixture.Exec("ALTER TABLE t ALTER i SET DEFAULT "+tc.expr, 0))
				fixture.ArithmeticRoundtrip(t, p, "t")
				run(fixture.Exec("INSERT INTO t VALUES(1,NULL)", 1))
				typ := "INT4"
				if tc.typ == "VARCHAR(2)" {
					typ = "VARCHAR"
				}
				for _, q := range []string{"INSERT INTO t(id) VALUES(2)", "INSERT INTO t VALUES(2,NULL),(3,DEFAULT)"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "i"}, []string{"INT4", typ}, [][]any{{int64(0), nil}, {int64(1), nil}}))
				}
			})
		}
	}
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE nn(id INT,i INT NOT NULL DEFAULT 7)", 0))
	p.Step(t, fixture.Exec("INSERT INTO nn VALUES(0,7)", 1))
	for _, action := range []string{"SET DEFAULT NULL", "DROP DEFAULT"} {
		p.Step(t, fixture.Exec("ALTER TABLE nn ALTER i "+action, 0))
		for _, q := range []string{"INSERT INTO nn VALUES(3,NULL)", "INSERT INTO nn(id) VALUES(3)", "INSERT INTO nn VALUES(3,DEFAULT)"} {
			p.Step(t, fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "nn", Column: "i"}))
		}
	}
	p.Step(t, fixture.Select("SELECT * FROM nn", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(7)}}))
}
