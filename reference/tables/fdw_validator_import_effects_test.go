package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWSQLValidatorImportEffects(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
				fdwLive(t, p, b, reader, run)
				run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
				fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.a(id integer)", 0)
				fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.a VALUES(11)", 1)
				fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.z(id integer)", 0)
				fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.z VALUES(33)", 1)
				run(fixture.Exec("CREATE TABLE fdw_validator_audit(kind oid)", 0))
				run(fixture.Exec("CREATE TABLE fdw_validator_gate(v integer)", 0))
				run(fixture.Exec("INSERT INTO fdw_validator_gate VALUES(0)", 1))
				run(fixture.Exec("CREATE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'INSERT INTO public.fdw_validator_audit SELECT $2; SELECT 1'", 0))
				// Keep the existing genuine importer and connection fixture. Only
				// the catalog validator is SQL; no importer callback is synthesized.
				handler := "fdw_sql_handler"
				serverOptions, mappingOptions := "", ""
				if b.PG {
					handler = "postgres_fdw_handler"
					serverOptions = " OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')"
					mappingOptions = " OPTIONS(user 'stuntgres',password 'stuntgres')"
				}
				run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta HANDLER "+handler+" VALIDATOR public.fdw_accept", 0))
				run(fixture.Exec("CREATE SERVER fdw_second FOREIGN DATA WRAPPER fdw_meta"+serverOptions, 0))
				run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_second"+mappingOptions, 0))
				run(fixture.Exec("TRUNCATE fdw_validator_audit", 0))
				run(fixture.Exec("INSERT INTO fdw_validator_audit VALUES(99)", 1))
				execute := func(step fixture.Step) {
					step.Prepared = prepared
					run(step)
				}
				importAll := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_second INTO fdw_import"
				metadata := func(rows [][]any) {
					run(fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, rows))
				}
				seed := [][]any{{int64(99)}}
				// Each genuine definition has one table and one column validator
				// invocation. Assert the full bag, not a provider-derived count.
				accepted := [][]any{{int64(99)}, {int64(1249)}, {int64(1249)}, {int64(3118)}, {int64(3118)}}
				sourceUnchanged := func() {
					steps := []fixture.Step{
						fixture.Select("SELECT c.relname,a.attname,t.typname,tn.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace WHERE n.nspname='fdw_source' AND c.relname IN ('a','z') AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum", []string{"relname", "attname", "typname", "nspname"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{"a", "id", "int4", "pg_catalog"}, {"z", "id", "int4", "pg_catalog"}}),
						fixture.Select("SELECT id FROM fdw_source.a", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}),
						fixture.Select("SELECT id FROM fdw_source.z", []string{"id"}, []string{"INT4"}, [][]any{{int64(33)}}),
						fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}),
					}
					for _, step := range steps {
						if b.PG {
							run(step)
						} else {
							fdwImportSourceRows(t, p.Ctx, reader, step)
						}
					}
					run(fixture.Select("SELECT v FROM fdw_validator_gate", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
				}
				metadata(nil)
				run(fdwValidatorAudit(seed))
				sourceUnchanged()
				for _, nullResult := range []bool{false, true} {
					// Both authentic providers supply table_name. Accept every job
					// for a before rejecting z's table job, after its INSERT.
					// The gate keeps division by zero a reached runtime error.
					body := "SELECT CASE WHEN 'table_name=z'=ANY($1) THEN 1/v ELSE 1 END FROM public.fdw_validator_gate"
					d := fdwDiagnostic("22012", "division by zero")
					d.Context = "SQL function \"fdw_accept\" statement 2\nimporting foreign table \"z\""
					if nullResult {
						body = "SELECT CASE WHEN 'table_name=z'=ANY($1) THEN NULL::integer ELSE 1 END"
						var oid int64
						if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='fdw_accept' AND pronamespace='public'::regnamespace").Scan(&oid); err != nil {
							t.Fatal(err)
						}
						d = fdwDiagnostic("XX000", fmt.Sprintf("function %d returned NULL", oid))
						d.Context = `importing foreign table "z"`
					}
					run(fixture.Exec("CREATE OR REPLACE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS $fdw$INSERT INTO public.fdw_validator_audit SELECT $2; "+body+"$fdw$", 0))
					execute(fixture.ErrorDiagnostic(importAll, d))
					metadata(nil)
					run(fdwValidatorAudit(seed))
					sourceUnchanged()
				}
				run(fixture.Exec("CREATE OR REPLACE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'INSERT INTO public.fdw_validator_audit SELECT $2; SELECT 1'", 0))
				run(fixture.Exec("BEGIN", 0))
				execute(fixture.Exec(importAll, 0))
				metadata([][]any{{"a"}, {"z"}})
				run(fdwValidatorAudit(accepted))
				run(fixture.Select("SELECT id FROM fdw_import.a UNION ALL SELECT id FROM fdw_import.z ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(33)}}))
				sourceUnchanged()
				run(fixture.Exec("ROLLBACK", 0))
				metadata(nil)
				run(fdwValidatorAudit(seed))
				sourceUnchanged()
				execute(fixture.Exec(importAll, 0))
				metadata([][]any{{"a"}, {"z"}})
				run(fdwValidatorAudit(accepted))
				run(fixture.Select("SELECT id FROM fdw_import.a UNION ALL SELECT id FROM fdw_import.z ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(33)}}))
				sourceUnchanged()
			})
		})
	}
}
