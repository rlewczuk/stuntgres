package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Later imported definitions execute in the same statement-private catalog as
// earlier completed definitions, including their column option metadata.
func TestReferenceFDWSQLValidatorImportVisibility(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		run(fixture.Exec("CREATE TABLE fdw_import.visibility_sentinel(id integer,note text)", 0))
		run(fixture.Exec("INSERT INTO fdw_import.visibility_sentinel VALUES(91,'preexisting')", 1))
		var sentinelOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'fdw_import.visibility_sentinel'::regclass::oid::bigint").Scan(&sentinelOID); err != nil {
			t.Fatal(err)
		}
		if sentinelOID <= 0 {
			t.Fatal("invalid sentinel identity", sentinelOID)
		}
		run(fixture.Exec("CREATE TABLE fdw_visibility_audit(options text,kind oid)", 0))
		run(fixture.Exec("CREATE TABLE fdw_visibility_log(relname text,attnum integer,attname text,options text)", 0))
		// Keep the same genuine side effects for both validators. Only b's
		// table-level invocation captures a's completed descriptor and columns.
		const visibleA = " FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname='fdw_import' AND c.relname='import_a' AND a.attnum>0 AND NOT a.attisdropped"
		const body = "INSERT INTO fdw_visibility_audit VALUES($1::text,$2); INSERT INTO fdw_visibility_log SELECT c.relname::text,a.attnum,a.attname::text,a.attfdwoptions::text" + visibleA + " AND 'table_name=import_b'=ANY($1); "
		const rejects = "SELECT CASE WHEN 'table_name=import_b'=ANY($1) AND EXISTS(SELECT 1" + visibleA + " AND a.attname='id' AND a.attfdwoptions::text='{column_name=id}') THEN ($1[2])::integer ELSE 7 END"
		run(fixture.Exec("CREATE FUNCTION fdw_visibility_reject(text[],oid) RETURNS integer LANGUAGE SQL AS $$"+body+rejects+"$$", 0))
		run(fixture.Exec("CREATE FUNCTION fdw_visibility_accept(text[],oid) RETURNS integer LANGUAGE SQL AS $$"+body+"SELECT 7$$", 0))
		wrapper := "fdw_native"
		if b.PG {
			wrapper = "postgres_fdw"
			defer c.ExecContext(p.Ctx, "ALTER FOREIGN DATA WRAPPER postgres_fdw VALIDATOR postgres_fdw_validator")
		}
		sentinel := func() {
			run(fixture.Select("SELECT id,note FROM fdw_import.visibility_sentinel ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(91), "preexisting"}}))
			run(fixture.Select("SELECT oid::bigint AS oid,relkind AS kind FROM pg_class WHERE oid='fdw_import.visibility_sentinel'::regclass::oid", []string{"oid", "kind"}, []string{"INT8", "CHAR"}, [][]any{{sentinelOID, "r"}}))
		}
		const importBoth = "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import"
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER "+wrapper+" VALIDATOR fdw_visibility_reject", 0))
		for _, prepared := range []bool{false, true} {
			d := fdwDiagnostic("22P02", `invalid input syntax for type integer: "table_name=import_b"`)
			d.Context = "SQL function \"fdw_visibility_reject\" statement 3\nimporting foreign table \"import_b\""
			s := fixture.ErrorDiagnostic(importBoth, d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			fdwImportBag(run, "visibility_sentinel")
			sentinel()
			run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_visibility_audit ORDER BY kind,options", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{}))
			run(fixture.Select("SELECT relname,attnum,attname,options FROM fdw_visibility_log ORDER BY attnum", []string{"relname", "attnum", "attname", "options"}, []string{"TEXT", "INT4", "TEXT", "TEXT"}, [][]any{}))
		}
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER "+wrapper+" VALIDATOR fdw_visibility_accept", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec(importBoth, 0)
			s.Prepared = prepared
			run(s)
			fdwImportBag(run, "import_a", "import_b", "visibility_sentinel")
			sentinel()
			observed := fixture.Select("SELECT relname,attnum,attname,options FROM fdw_visibility_log ORDER BY attnum", []string{"relname", "attnum", "attname", "options"}, []string{"TEXT", "INT4", "TEXT", "TEXT"}, [][]any{
				{"import_a", int64(1), "id", "{column_name=id}"},
				{"import_a", int64(2), "label", "{column_name=label}"},
				{"import_a", int64(3), "amount", "{column_name=amount}"},
				{"import_a", int64(4), "flag", "{column_name=flag}"},
			})
			observed.Prepared = prepared
			run(observed)
			run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_visibility_audit ORDER BY kind,options", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{
				{"{column_name=amount}", int64(1249)}, {"{column_name=flag}", int64(1249)}, {"{column_name=id}", int64(1249)}, {"{column_name=key}", int64(1249)}, {"{column_name=label}", int64(1249)}, {"{column_name=note}", int64(1249)},
				{"{schema_name=fdw_source,table_name=import_a}", int64(3118)}, {"{schema_name=fdw_source,table_name=import_b}", int64(3118)},
			}))
			run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a,fdw_import.import_b", 0))
			run(fixture.Exec("DELETE FROM fdw_visibility_log", 4))
			run(fixture.Exec("DELETE FROM fdw_visibility_audit", 8))
		}
	})
}
