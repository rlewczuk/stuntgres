package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTypesCreate(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "sql", true: "go"}[goTx]+map[bool]string{false: "_rollback", true: "_commit"}[commit], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						var e fixture.Executor = c
						var tx *sql.Tx
						if goTx {
							var err error
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
						} else {
							fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
							defer c.ExecContext(p.Ctx, "ROLLBACK")
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						for _, q := range []string{
							"CREATE DOMAIN tt_word AS varchar(3) COLLATE \"C\" DEFAULT 'ok' CHECK(VALUE<>'bad')",
							"CREATE DOMAIN tt_child AS tt_word",
							"CREATE TYPE tt_pair AS(id integer,note text)",
							"CREATE DOMAIN tt_box AS tt_pair",
							"CREATE TABLE tt_values(id integer,v tt_child,a tt_word[],p tt_box)",
							"CREATE TABLE tt_typed OF tt_pair",
						} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO tt_values VALUES(1,DEFAULT,'[0:1]={ok,NULL}','(7,x)'),(2,NULL,NULL,NULL)", 2))
						run(fixture.Exec("INSERT INTO tt_typed VALUES(7,'x')", 1))
						values := fixture.Select("SELECT id,v::text,a::text,p::text FROM tt_values ORDER BY id", []string{"id", "v", "a", "p"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "ok", "[0:1]={ok,NULL}", "(7,x)"}, {int64(2), nil, nil, nil}})
						run(values)
						graph := fixture.Select("SELECT t.typtype,a.typelem=t.oid AS reciprocal,c.reltype=t.oid AS backing FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_class c ON c.oid=t.typrelid WHERE t.oid='tt_pair'::regtype", []string{"typtype", "reciprocal", "backing"}, []string{"CHAR", "BOOL", "BOOL"}, [][]any{{"c", true, true}})
						run(graph)
						run(fixture.Select("SELECT atttypmod FROM pg_attribute WHERE attrelid='tt_values'::regclass AND attname='v'", []string{"atttypmod"}, []string{"INT4"}, [][]any{{int64(-1)}}))
						run(fixture.Select("SELECT typtypmod FROM pg_type WHERE oid='tt_word'::regtype", []string{"typtypmod"}, []string{"INT4"}, [][]any{{int64(7)}}))
						count := "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_word','_tt_word','tt_child','_tt_child','tt_pair','_tt_pair','tt_box','_tt_box')"
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(count, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Exec("SAVEPOINT bad_value", 0))
						d := fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(3)")
						run(fixture.ErrorDiagnostic("INSERT INTO tt_values(id,v) VALUES(3,'long')", d))
						run(fixture.Exec("ROLLBACK TO bad_value", 0))
						d = fixture.ControlsReferenceDiagnostic("23514", `value for domain tt_child violates check constraint "tt_word_check"`)
						d.Schema, d.DataType, d.Constraint = "public", "tt_child", "tt_word_check"
						run(fixture.ErrorDiagnostic("INSERT INTO tt_values(id,v) VALUES(3,'bad')", d))
						run(fixture.Exec("ROLLBACK TO bad_value", 0))
						run(values)
						if goTx {
							var err error
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
						} else {
							end := "ROLLBACK"
							if commit {
								end = "COMMIT"
							}
							run(fixture.Exec(end, 0))
						}
						want := int64(0)
						if commit {
							want = 8
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, values)
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, graph)
						}
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(count, []string{"count"}, []string{"INT8"}, [][]any{{want}}))
					})
				}
			})
		}
	}
}
