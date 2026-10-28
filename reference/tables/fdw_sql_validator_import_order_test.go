package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// An earlier definition's validator must run before the next definition is
// staged. A later destination collision must not replace that earlier error.
func TestReferenceFDWSQLValidatorImportOrder(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		run(fixture.Exec("CREATE TABLE fdw_import.import_b(key bigint,note text)", 0))
		run(fixture.Exec("INSERT INTO fdw_import.import_b VALUES(91,'preexisting')", 1))
		var existingOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'fdw_import.import_b'::regclass::oid::bigint").Scan(&existingOID); err != nil {
			t.Fatal(err)
		}
		if existingOID <= 0 {
			t.Fatal("invalid preexisting relation identity", existingOID)
		}
		run(fixture.Exec("CREATE TABLE fdw_import_order_log(options text,kind oid)", 0))
		run(fixture.Exec("CREATE FUNCTION fdw_import_order_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fdw_import_order_log VALUES($1::text,$2); SELECT CASE WHEN 'table_name=import_a'=ANY($1) THEN ($1[2])::integer ELSE 7 END$$", 0))
		wrapper := "fdw_native"
		if b.PG {
			wrapper = "postgres_fdw"
			defer c.ExecContext(p.Ctx, "ALTER FOREIGN DATA WRAPPER postgres_fdw VALIDATOR postgres_fdw_validator")
		}
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER "+wrapper+" VALIDATOR fdw_import_order_validator", 0))
		for _, prepared := range []bool{false, true} {
			d := fdwDiagnostic("22P02", `invalid input syntax for type integer: "table_name=import_a"`)
			d.Context = "SQL function \"fdw_import_order_validator\" statement 2\nimporting foreign table \"import_a\""
			s := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			fdwImportBag(run, "import_b")
			run(fixture.Select("SELECT key,note FROM fdw_import.import_b ORDER BY key", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(91), "preexisting"}}))
			run(fixture.Select("SELECT oid::bigint AS oid,relkind AS kind FROM pg_class WHERE oid='fdw_import.import_b'::regclass::oid", []string{"oid", "kind"}, []string{"INT8", "CHAR"}, [][]any{{existingOID, "r"}}))
			run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_import_order_log ORDER BY options,kind", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{}))
		}
		run(fixture.Exec("CREATE FUNCTION fdw_import_success_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fdw_import_order_log VALUES($1::text,$2); SELECT 7$$", 0))
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER "+wrapper+" VALIDATOR fdw_import_success_validator", 0))
		for _, prepared := range []bool{false, true} {
			d := fdwDiagnostic("42P07", `relation "import_b" already exists`)
			d.Context = `importing foreign table "import_b"`
			s := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			fdwImportBag(run, "import_b")
			run(fixture.Select("SELECT key,note FROM fdw_import.import_b ORDER BY key", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(91), "preexisting"}}))
			run(fixture.Select("SELECT oid::bigint AS oid,relkind AS kind FROM pg_class WHERE oid='fdw_import.import_b'::regclass::oid", []string{"oid", "kind"}, []string{"INT8", "CHAR"}, [][]any{{existingOID, "r"}}))
			run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_import_order_log ORDER BY options,kind", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{}))
		}
		// The same validator really writes on successful execution; the empty
		// audit above is rollback, not a non-executable validator declaration.
		run(fixture.Prepared(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a) FROM SERVER fdw_live INTO fdw_import", 0)))
		fdwImportBag(run, "import_a", "import_b")
		run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_import_order_log ORDER BY kind,options", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{
			{"{column_name=amount}", int64(1249)}, {"{column_name=flag}", int64(1249)}, {"{column_name=id}", int64(1249)}, {"{column_name=label}", int64(1249)}, {"{schema_name=fdw_source,table_name=import_a}", int64(3118)},
		}))
	})
}
