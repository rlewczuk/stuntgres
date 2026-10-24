package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const transactionalSchemaFlyway = `CREATE TABLE "public"."flyway_schema_history" (
    "installed_rank" INT NOT NULL,
    "version" VARCHAR(50),
    "description" VARCHAR(200) NOT NULL,
    "type" VARCHAR(20) NOT NULL,
    "script" VARCHAR(1000) NOT NULL,
    "checksum" INTEGER,
    "installed_by" VARCHAR(100) NOT NULL,
    "installed_on" TIMESTAMP NOT NULL DEFAULT now(),
    "execution_time" INTEGER NOT NULL,
    "success" BOOLEAN NOT NULL
)`

// Utilities run through the held object, not a fresh Prepare on every execution.
func transactionalSchemaRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, held bool, s fixture.Step) {
	t.Helper()
	if held && s.State != "25P02" {
		stmt, err := e.PrepareContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal("prepare", s.Query, err)
		}
		defer stmt.Close()
		e = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferenceTransactionalSchemaFlyway(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("go=%t/held=%t/commit=%t", goTx, held, commit), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							fixture.LockExec(t, p.Ctx, c, "SET TIME ZONE 'UTC'", 0)
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
							run := func(s fixture.Step) { transactionalSchemaRun(t, p, b, e, false, s) }
							transactionalSchemaRun(t, p, b, e, held, fixture.Exec(transactionalSchemaFlyway, 0))
							run(fixture.Exec(`INSERT INTO public.flyway_schema_history(installed_rank,version,description,type,script,checksum,installed_by,execution_time,success) VALUES(1,NULL,'first','SQL','V1.sql',NULL,'tester',4,true)`, 1))
							run(fixture.Exec(`INSERT INTO public.flyway_schema_history VALUES(2,'2','second','SQL','V2.sql',9,'tester',DEFAULT,5,false)`, 1))
							run(fixture.Select(`SELECT installed_rank,version,description,type,script,checksum,installed_by,installed_on=transaction_timestamp()::timestamp AS tx_time,execution_time,success FROM public.flyway_schema_history ORDER BY installed_rank`, []string{"installed_rank", "version", "description", "type", "script", "checksum", "installed_by", "tx_time", "execution_time", "success"}, []string{"INT4", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "INT4", "VARCHAR", "BOOL", "INT4", "BOOL"}, [][]any{{int64(1), nil, "first", "SQL", "V1.sql", nil, "tester", true, int64(4), true}, {int64(2), "2", "second", "SQL", "V2.sql", int64(9), "tester", true, int64(5), false}}))
							metadata := `SELECT a.attname,t.typname,a.atttypmod,a.attnotnull FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid='public.flyway_schema_history'::regclass AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`
							run(fixture.Select(metadata, []string{"attname", "typname", "atttypmod", "attnotnull"}, []string{"NAME", "NAME", "INT4", "BOOL"}, [][]any{{"installed_rank", "int4", int64(-1), true}, {"version", "varchar", int64(54), false}, {"description", "varchar", int64(204), true}, {"type", "varchar", int64(24), true}, {"script", "varchar", int64(1004), true}, {"checksum", "int4", int64(-1), false}, {"installed_by", "varchar", int64(104), true}, {"installed_on", "timestamp", int64(-1), true}, {"execution_time", "int4", int64(-1), true}, {"success", "bool", int64(-1), true}}))
							closure := transactionalSchemaFlywayClosure(t, p, b, e)
							transactionalSchemaPeerClosure(t, p, b, peer, closure, false)
							for _, column := range []struct {
								name string
								n    int
							}{{"version", 50}, {"description", 200}, {"type", 20}, {"script", 1000}, {"installed_by", 100}} {
								for _, ch := range []string{"x", "é"} {
									run(fixture.Exec("SAVEPOINT length_limit", 0))
									run(fixture.Exec("UPDATE public.flyway_schema_history SET "+column.name+"=$1 WHERE installed_rank=1", 1, strings.Repeat(ch, column.n)))
									d := fixture.ControlsReferenceDiagnostic("22001", fmt.Sprintf("value too long for type character varying(%d)", column.n))
									run(fixture.ErrorDiagnostic("UPDATE public.flyway_schema_history SET "+column.name+"=$1 WHERE installed_rank=1", d, strings.Repeat(ch, column.n+1)))
									run(fixture.Exec("ROLLBACK TO length_limit", 0))
								}
							}
							for _, column := range []struct {
								name  string
								index int
							}{{"installed_rank", 0}, {"description", 2}, {"type", 3}, {"script", 4}, {"installed_by", 6}, {"installed_on", 7}, {"execution_time", 8}, {"success", 9}} {
								values := []string{"3", "NULL", "'d'", "'SQL'", "'f'", "NULL", "'u'", "TIMESTAMP '2000-01-01 00:00:00'", "1", "true"}
								detail := []string{"3", "null", "d", "SQL", "f", "null", "u", "2000-01-01 00:00:00", "1", "t"}
								values[column.index], detail[column.index] = "NULL", "null"
								run(fixture.Exec("SAVEPOINT required_column", 0))
								d := fixture.ControlsReferenceDiagnostic("23502", fmt.Sprintf("null value in column %q of relation \"flyway_schema_history\" violates not-null constraint", column.name))
								d.Schema, d.Table, d.Column = "public", "flyway_schema_history", column.name
								d.Detail = "Failing row contains (" + strings.Join(detail, ", ") + ")."
								run(fixture.ErrorDiagnostic("INSERT INTO public.flyway_schema_history VALUES("+strings.Join(values, ",")+")", d))
								run(fixture.Exec("ROLLBACK TO required_column", 0))
							}
							absence := fixture.Select(`SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, absence)
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(`SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('flyway_schema_history','_flyway_schema_history')`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
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
							} else if commit {
								run(fixture.Exec("COMMIT", 0))
							} else {
								run(fixture.Exec("ROLLBACK", 0))
							}
							transactionalSchemaPeerClosure(t, p, b, peer, closure, commit)
							if commit {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM public.flyway_schema_history", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
								for {
									var advanced bool
									if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()::timestamp > installed_on FROM public.flyway_schema_history WHERE installed_rank=1").Scan(&advanced); err != nil {
										t.Fatal(err)
									}
									if advanced {
										break
									}
								}
								fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
								fixture.LockExec(t, p.Ctx, c, `INSERT INTO public.flyway_schema_history VALUES(3,NULL,'later','SQL','V3.sql',NULL,'tester',DEFAULT,1,true)`, 1)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT installed_on=transaction_timestamp()::timestamp AS current_default FROM public.flyway_schema_history WHERE installed_rank=3`, []string{"current_default"}, []string{"BOOL"}, [][]any{{true}}))
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT installed_on > (SELECT installed_on FROM public.flyway_schema_history WHERE installed_rank=1) AS later_than_create FROM public.flyway_schema_history WHERE installed_rank=3`, []string{"later_than_create"}, []string{"BOOL"}, [][]any{{true}}))
								fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
							} else {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, absence)
							}
						})
					}
				})
			}
		}
	}
}
