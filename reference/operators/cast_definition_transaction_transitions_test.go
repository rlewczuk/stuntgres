package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastDefinitionTransactionTransitions(t *testing.T) {
	for _, route := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/held=%v/commit=%v", route, held, commit), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
							peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
							defer c.ExecContext(p.Ctx, "ROLLBACK")
							runOn := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
							for _, q := range []string{castDefinitionSource, castDefinitionScore, castDefinitionEdge} {
								runOn(c, fixture.Exec(q, 0))
							}
							var oid int64
							if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='integer'::regtype").Scan(&oid); err != nil || oid <= 0 {
								t.Fatal(oid, err)
							}
							q := "DROP CAST (cast_src AS integer)"
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							var x fixture.Executor = c
							var tx *sql.Tx
							if route == "go" {
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
								var e fixture.Executor = x
								if held && s.Query == q {
									st := stmt
									if tx != nil {
										st = tx.StmtContext(p.Ctx, stmt)
										defer st.Close()
									}
									e = fixture.HeldStatement{Executor: x, Statement: st, SQL: q}
								}
								runOn(e, s)
							}
							present := func(e fixture.Executor, n int64) {
								runOn(e, fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE oid=$1", []string{"n"}, []string{"INT8"}, [][]any{{n}}, oid))
							}
							use := fixture.Select("SELECT ('blue'::cast_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
							if route != "autocommit" {
								run(fixture.Exec("SAVEPOINT old", 0))
							}
							run(fixture.Exec(q, 0))
							present(x, 0)
							if route != "autocommit" {
								present(peer, 1)
								runOn(peer, use)
								run(fixture.Exec("ROLLBACK TO old", 0))
								present(x, 1)
								run(use)
								run(fixture.Exec(q, 0))
								run(fixture.Exec("RELEASE old", 0))
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
									present(peer, 0)
								} else {
									present(peer, 1)
									runOn(peer, use)
								}
							} else {
								present(peer, 0)
							}
						})
					}
				})
			}
		}
	}
}

func TestReferenceCastDefinitionTransactionBinary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			castDefinitionCleanup(t, p, b, [2]string{"xid", "real"})
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			stmt, err := c.PrepareContext(p.Ctx, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, commit := range []bool{false, true} {
				run(fixture.Exec("BEGIN", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "CREATE CAST (xid AS real) WITHOUT FUNCTION"}, b.Recorder, fixture.Exec("CREATE CAST (xid AS real) WITHOUT FUNCTION", 0))
				run(fixture.Select("SELECT ($1::xid)::real AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{float64(1)}}, "1065353216"))
				run(fixture.Select("SELECT castmethod::text,castcontext::text,castfunc=0 AS no_routine FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"castmethod", "castcontext", "no_routine"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"b", "e", true}}))
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				run(fixture.Exec(end, 0))
				n := int64(0)
				if commit {
					n = 1
				}
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
			}
		})
	}
}
