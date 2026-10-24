package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionTransactions(t *testing.T) {
	for _, route := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			for _, mode := range []string{"", " AS ASSIGNMENT", " AS IMPLICIT"} {
				t.Run(fmt.Sprintf("%s/held=%v/context=%s", route, held, mode), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
							peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
							defer c.ExecContext(p.Ctx, "ROLLBACK")
							runOn := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
							for _, q := range []string{castDefinitionSource, castDefinitionScore, "CREATE TABLE cast_dest(n integer)", "CREATE FUNCTION cast_receiver(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT $1+100 $$"} {
								runOn(c, fixture.Exec(q, 0))
							}
							create, drop := castDefinitionEdge+mode, "DROP CAST (cast_src AS integer)"
							statements := map[string]*sql.Stmt{}
							if held {
								for _, q := range []string{create, drop} {
									stmt, err := c.PrepareContext(p.Ctx, q)
									if err != nil {
										t.Fatal("quiet utility Prepare", err)
									}
									defer stmt.Close()
									statements[q] = stmt
								}
							}
							count := func(x fixture.Executor, n int64) {
								runOn(x, fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='integer'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
							}
							missing := func(x fixture.Executor) {
								d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type cast_src to integer")
								d.Position = "25"
								s := fixture.ErrorDiagnostic("SELECT ('red'::cast_src)::integer", d)
								s.SelectRows = true
								runOn(x, s)
							}
							count(c, 0)
							missing(peer)
							for _, commit := range []bool{false, true} {
								var x fixture.Executor = c
								var tx *sql.Tx
								if route == "go" {
									var err error
									tx, err = c.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									x = tx
								} else if route == "sql" {
									runOn(c, fixture.Exec("BEGIN", 0))
								}
								run := func(s fixture.Step) {
									var executor fixture.Executor = x
									if stmt := statements[s.Query]; stmt != nil {
										if tx != nil {
											stmt = tx.StmtContext(p.Ctx, stmt)
											defer stmt.Close()
										}
										executor = fixture.HeldStatement{Executor: x, Statement: stmt, SQL: s.Query}
									}
									runOn(executor, s)
								}
								run(fixture.Exec(create, 0))
								count(x, 1)
								context := "e"
								if mode == " AS ASSIGNMENT" {
									context = "a"
								}
								if mode == " AS IMPLICIT" {
									context = "i"
								}
								run(fixture.Select("SELECT castcontext::text,castmethod::text,castfunc='cast_score(cast_src)'::regprocedure::oid AS routine FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='integer'::regtype", []string{"castcontext", "castmethod", "routine"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{context, "f", true}}))
								q := "SELECT ($1::cast_src)::integer AS answer"
								consumer, err := x.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								for _, v := range []struct{ input, answer any }{{"red", int64(41)}, {"blue", int64(42)}, {nil, int64(99)}} {
									s := fixture.Select(q, []string{"answer"}, []string{"INT4"}, [][]any{{v.answer}}, v.input)
									runOn(fixture.HeldStatement{Executor: x, Statement: consumer, SQL: q}, s)
								}
								consumer.Close()
								if route != "autocommit" {
									count(peer, 0)
									missing(peer)
									run(fixture.Exec("SAVEPOINT prior", 0))
								} else {
									count(peer, 1)
								}
								if mode != "" {
									run(fixture.Exec("INSERT INTO cast_dest VALUES ('red'::cast_src)", 1))
								}
								if mode == " AS IMPLICIT" {
									run(fixture.Select("SELECT cast_receiver('red'::cast_src) AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(141)}}))
								}
								run(fixture.Exec(drop, 0))
								count(x, 0)
								if route != "autocommit" {
									run(fixture.Exec("ROLLBACK TO prior", 0))
									count(x, 1)
									run(fixture.Select("SELECT ('red'::cast_src)::integer AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(41)}}))
									run(fixture.Exec("ROLLBACK TO prior", 0))
									run(fixture.Exec("RELEASE prior", 0))
									if tx != nil {
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
									if commit {
										count(peer, 1)
										runOn(peer, fixture.Select("SELECT ('blue'::cast_src)::integer AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(42)}}))
										runOn(peer, fixture.Exec(drop, 0))
									} else {
										count(peer, 0)
										missing(peer)
									}
								}
							}
						})
					}
				})
			}
		}
	}
}

func TestReferenceCastDefinitionTransactionPrivateTypes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"BEGIN", "CREATE SCHEMA cast_private", "CREATE TYPE cast_private.src AS ENUM ('red','blue')", "CREATE TYPE cast_private.dst AS ENUM ('red','blue')", "CREATE CAST (cast_private.src AS cast_private.dst) WITH INOUT AS ASSIGNMENT", "CREATE TABLE cast_private.data(v cast_private.dst)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO cast_private.data VALUES ('blue'::cast_private.src)", 1))
			run(fixture.Select("SELECT v::text AS v FROM cast_private.data", []string{"v"}, []string{"TEXT"}, [][]any{{"blue"}}))
			run(fixture.Exec("SAVEPOINT private", 0))
			run(fixture.Exec("DROP CAST (cast_private.src AS cast_private.dst)", 0))
			run(fixture.Exec("ROLLBACK TO private", 0))
			run(fixture.Exec("INSERT INTO cast_private.data VALUES ('red'::cast_private.src)", 1))
			run(fixture.Exec("RELEASE private", 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT count(*) AS n FROM pg_type WHERE typname IN ('src','dst') AND typnamespace IN (SELECT oid FROM pg_namespace WHERE nspname='cast_private')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	}
}
