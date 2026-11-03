package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmBinding(t *testing.T) {
	pgTrgmTextAdmissionCases(t)
	t.Run("AllRoutines", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			for _, d := range pgTrgmDeclarations {
				t.Run(d.name, func(t *testing.T) {
					run := pgTrgmRun(t, p, b, c)
					args, literal := "$1", "'cat'"
					if d.arity == 2 {
						args += ",$2"
						literal += ",'cat'"
					}
					if d.inputs == "real" {
						args, literal = "$1::real", "0.25::real"
					}
					if d.arity == 0 {
						args, literal = "", ""
					}
					q := "SELECT " + d.name + "(" + args + ")"
					typ := "FLOAT4"
					var want any = float64(1)
					if d.result == "boolean" {
						typ, want = "BOOL", true
					}
					if d.name == "show_trgm" {
						typ, want = "_TEXT", pgTrgmArray("  c", " ca", "at ", "cat")
					}
					if d.result == "real" && d.name != "similarity" && d.name != "word_similarity" && d.name != "strict_word_similarity" {
						want = float64(0)
					}
					if d.name == "set_limit" || d.name == "show_limit" {
						run(fixture.Exec("SET pg_trgm.similarity_threshold='0.25'", 0))
						want = float64(0.25)
					}
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					frames := [][]any{{"cat"}, {nil}}
					if d.arity == 2 {
						frames = [][]any{{"cat", "cat"}, {nil, "cat"}, {"cat", nil}, {nil, nil}, {"cat", "cat"}}
					}
					if d.inputs == "real" {
						frames = [][]any{{"0.25"}, {nil}, {"0.25"}}
					}
					if d.arity == 0 {
						frames = [][]any{{}, {}}
					}
					for _, frame := range frames {
						v := want
						for _, arg := range frame {
							if arg == nil {
								v = nil
							}
						}
						s := pgTrgmScalar(b, q, d.name, typ, v, frame...)
						run(s)
						run(fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					run(pgTrgmScalar(b, "SELECT "+d.name+"("+literal+")", d.name, typ, want))
					if d.arity != 0 {
						nullArgs := "NULL::text"
						if d.inputs == "real" {
							nullArgs = "NULL::real"
						}
						if d.arity == 2 {
							nullArgs += ",'cat'::text"
						}
						run(pgTrgmScalar(b, "SELECT "+d.name+"("+nullArgs+")", d.name, typ, nil))
						// STRICT scalar sources return one typed NULL, not zero SRF rows.
						run(pgTrgmScalar(b, "SELECT * FROM "+d.name+"("+nullArgs+")", d.name, typ, nil))
					} else {
						run(pgTrgmScalar(b, "SELECT * FROM "+d.name+"()", d.name, typ, want))
					}
					run(pgTrgmSelect(b, "SELECT "+d.name+"("+literal+") WHERE false", []string{d.name}, []string{typ}, nil))
					run(fixture.Prepared(pgTrgmSelect(b, "SELECT "+d.name+"("+literal+") WHERE false", []string{d.name}, []string{typ}, nil)))
				})
			}
			run(fixture.Exec("CREATE TABLE trgm_input(id int,v text,w varchar(20))", 0))
			run(fixture.Exec("INSERT INTO trgm_input VALUES(1,'cat','CAT'),(2,NULL,'cat')", 2))
			run(pgTrgmSelect(b,
				"SELECT similarity(v,w),show_trgm(w) FROM trgm_input ORDER BY id",
				[]string{"similarity", "show_trgm"}, []string{"FLOAT4", "_TEXT"},
				[][]any{{float64(1), pgTrgmArray("  c", " ca", "at ", "cat")}, {nil, pgTrgmArray("  c", " ca", "at ", "cat")}},
			))
			run(pgTrgmScalar(b, "SELECT similarity($1,$1)", "similarity", "FLOAT4", float64(1), "cat"))
			run(fixture.Select("SELECT id FROM trgm_input WHERE similarity(v,w)=1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			pgTrgmNotices(t, notices)
		})
	})
	t.Run("StrictSiblingDemand", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			run(fixture.Exec("CREATE SEQUENCE trgm_eval", 0))
			run(fixture.Exec("CREATE TABLE trgm_null(v text)", 0))
			run(fixture.Exec("INSERT INTO trgm_null VALUES(NULL)", 1))
			for _, name := range []string{"similarity", "word_similarity", "strict_word_similarity", "similarity_op", "similarity_dist"} {
				for _, prepared := range []bool{false, true} {
					// Runtime NULL must not suppress a demanded volatile sibling.
					q := "SELECT " + name + "(v,nextval('trgm_eval')::text) FROM trgm_null"
					typ := "FLOAT4"
					if name == "similarity_op" {
						typ = "BOOL"
					}
					s := pgTrgmScalar(b, q, name, typ, nil)
					s.Prepared = prepared
					run(s)
				}
			}
			run(fixture.Select("SELECT last_value,is_called FROM trgm_eval", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), true}}))
			for _, prepared := range []bool{false, true} {
				s := pgTrgmFailure("SELECT similarity(v,(1/(length(v)+0))::text) FROM (SELECT ''::text AS v) x", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.Prepared = prepared
				run(s)
			}
			// Two occurrences, each consumed once, in left-to-right demand order.
			run(pgTrgmScalar(b, "SELECT similarity(nextval('trgm_eval')::text,nextval('trgm_eval')::text)", "similarity", "FLOAT4", pgTrgmReal(b.PG, 1, 5)))
			run(fixture.Select("SELECT last_value FROM trgm_eval", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(12)}}))
			pgTrgmNotices(t, notices)
		})
	})
}
