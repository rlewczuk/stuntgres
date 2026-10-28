package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// The parameter-dependent cast fails only when the validator is invoked, not
// while CREATE FUNCTION analyzes a constant expression. The first command also
// prevents SQL scalar inlining from erasing the invocation frame.
const fdwCheckerDefinition = "CREATE FUNCTION fdw_checker(text[],oid) RETURNS integer LANGUAGE SQL AS $$SELECT 0; SELECT CASE WHEN 'mode=bad'=ANY($1) THEN ($1[1])::integer ELSE 7 END$$"

func fdwCheckerDiagnostic() fixture.Diagnostic {
	d := fdwDiagnostic("22P02", `invalid input syntax for type integer: "mode=bad"`)
	d.Context = `SQL function "fdw_checker" statement 2`
	return d
}

func TestReferenceFDWSQLValidator(t *testing.T) {
	t.Run("arguments", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE fdw_validation_log(n integer GENERATED ALWAYS AS IDENTITY,options text,kind oid)", 0))
			run(fixture.Exec("CREATE FUNCTION fdw_log_validator(text[],oid) RETURNS integer STRICT LANGUAGE SQL AS $$INSERT INTO fdw_validation_log(options,kind) VALUES($1::text,$2); SELECT 7$$", 0))
			run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_log_validator", 0))
			run(fixture.Prepared(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta OPTIONS(a 'one',b 'two=three')", 0)))
			run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(secret '')", 0))
			run(fixture.Prepared(fixture.Exec("CREATE FOREIGN TABLE fdw_validated(id integer OPTIONS(remote 'key'),label text) SERVER fdw_server OPTIONS(remote 'data')", 0)))
			run(fixture.Prepared(fixture.Exec("ALTER SERVER fdw_server OPTIONS(SET a 'changed',DROP b,ADD c '')", 0)))
			run(fixture.Exec("ALTER FOREIGN TABLE fdw_validated OPTIONS(SET remote 'new'),ALTER COLUMN id OPTIONS(SET remote 'newkey')", 0))
			want := [][]any{
				{"{}", int64(2328)},
				{"{a=one,b=two=three}", int64(1417)},
				{"{secret=}", int64(1418)},
				{"{remote=data}", int64(3118)},
				{"{remote=key}", int64(1249)},
				{"{a=changed,c=}", int64(1417)},
				{"{remote=new}", int64(3118)},
				{"{remote=newkey}", int64(1249)},
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_validation_log ORDER BY n", []string{"options", "kind"}, []string{"TEXT", "INT8"}, want)
				s.Prepared = prepared
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("BEGIN", 0))
				change := fixture.Exec("ALTER SERVER fdw_server OPTIONS(SET a 'transaction')", 0)
				change.Prepared = prepared
				run(change)
				run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_validation_log WHERE options='{a=transaction,c=}' ORDER BY n", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{{"{a=transaction,c=}", int64(1417)}}))
				run(fixture.Select("SELECT srvoptions::text AS options FROM pg_foreign_server WHERE srvname='fdw_server'", []string{"options"}, []string{"TEXT"}, [][]any{{"{a=transaction,c=}"}}))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT count(*) FROM fdw_validation_log", []string{"count"}, []string{"INT8"}, [][]any{{int64(8)}}))
				run(fixture.Select("SELECT srvoptions::text AS options FROM pg_foreign_server WHERE srvname='fdw_server'", []string{"options"}, []string{"TEXT"}, [][]any{{"{a=changed,c=}"}}))
			}
			run(fixture.Exec("CREATE FUNCTION fdw_null_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fdw_validation_log(options,kind) VALUES('null-result',$2); SELECT NULL::integer$$", 0))
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_null_validator", 0))
			run(fixture.Select("SELECT count(*) FROM fdw_validation_log", []string{"count"}, []string{"INT8"}, [][]any{{int64(8)}}))
			var validatorOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='fdw_null_validator'").Scan(&validatorOID); err != nil {
				t.Fatal(err)
			}
			if validatorOID <= 0 {
				t.Fatal("invalid validator identity", validatorOID)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("ALTER FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_null_validator OPTIONS(a 'accepted')", fdwDiagnostic("XX000", fmt.Sprintf("function %d returned NULL", validatorOID)))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_validation_log WHERE options='null-result' ORDER BY n", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{}))
				run(fixture.Select("SELECT fdwoptions IS NULL AS no_options FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta'", []string{"no_options"}, []string{"BOOL"}, [][]any{{true}}))
			}
		})
	})
	t.Run("rejection", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
			run(fixture.Exec(fdwCheckerDefinition, 0))
			// Installing the checker alone must not reject the existing bad option.
			run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta OPTIONS(mode 'bad')", 0))
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_checker", 0))
			run(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta OPTIONS(mode 'good')", 0))
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_validated(id integer OPTIONS(mode 'good')) SERVER fdw_server OPTIONS(mode 'good')", 0))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{
					"CREATE SERVER fdw_rejected FOREIGN DATA WRAPPER fdw_meta OPTIONS(mode 'bad')",
					"ALTER SERVER fdw_server OPTIONS(SET mode 'bad')",
					"CREATE FOREIGN TABLE fdw_rejected(id integer) SERVER fdw_server OPTIONS(mode 'bad')",
					"CREATE FOREIGN TABLE fdw_rejected(id integer OPTIONS(mode 'bad')) SERVER fdw_server",
					"ALTER FOREIGN TABLE fdw_validated OPTIONS(SET mode 'bad')",
					"ALTER FOREIGN TABLE fdw_validated OPTIONS(SET mode 'changed'),ALTER COLUMN id OPTIONS(SET mode 'bad')",
					"ALTER FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_checker OPTIONS(SET mode 'bad')",
				} {
					s := fixture.ErrorDiagnostic(q, fdwCheckerDiagnostic())
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				run(fixture.Select("SELECT srvoptions::text AS options FROM pg_foreign_server WHERE srvname='fdw_server'", []string{"options"}, []string{"TEXT"}, [][]any{{"{mode=good}"}}))
				run(fixture.Select("SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid='fdw_validated'::regclass::oid", []string{"options"}, []string{"TEXT"}, [][]any{{"{mode=good}"}}))
				run(fixture.Select("SELECT attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_validated'::regclass::oid AND attnum=1", []string{"options"}, []string{"TEXT"}, [][]any{{"{mode=good}"}}))
				run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='fdw_rejected'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_rejected'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			// A validator's own local writes share the failed statement's rollback.
			run(fixture.Exec("CREATE TABLE fdw_rejection_log(v text)", 0))
			run(fixture.Exec("CREATE FUNCTION fdw_logging_checker(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fdw_rejection_log VALUES('entered'); SELECT fdw_checker($1,$2)$$", 0))
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_logging_checker", 0))
			d := fdwCheckerDiagnostic()
			d.Context += "\nSQL function \"fdw_logging_checker\" statement 2"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("ALTER SERVER fdw_server OPTIONS(SET mode 'bad')", d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT v FROM fdw_rejection_log", []string{"v"}, []string{"TEXT"}, [][]any{}))
			}
		})
	})
	t.Run("import", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
			fdwImportSource(t, p, b, c, r, run)
			run(fixture.Exec("CREATE TABLE fdw_import_log(options text,kind oid)", 0))
			run(fixture.Exec("CREATE FUNCTION fdw_import_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fdw_import_log VALUES($1::text,$2); SELECT CASE WHEN 'table_name=import_b'=ANY($1) THEN ($1[2])::integer ELSE 7 END$$", 0))
			wrapper := "fdw_native"
			if b.PG {
				wrapper = "postgres_fdw"
				// The extension owns this cluster-global wrapper: restore its original
				// validator even when an assertion aborts the scenario.
				defer c.ExecContext(p.Ctx, "ALTER FOREIGN DATA WRAPPER postgres_fdw VALIDATOR postgres_fdw_validator")
			}
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER "+wrapper+" VALIDATOR fdw_import_validator", 0))
			for _, prepared := range []bool{false, true} {
				d := fdwDiagnostic("22P02", `invalid input syntax for type integer: "table_name=import_b"`)
				d.Context = "SQL function \"fdw_import_validator\" statement 2\nimporting foreign table \"import_b\""
				s := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				fdwImportBag(run)
				run(fixture.Select("SELECT count(*) FROM fdw_import_log", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			run(fixture.Prepared(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a) FROM SERVER fdw_live INTO fdw_import", 0)))
			fdwImportBag(run, "import_a")
			run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_import_log ORDER BY kind,options", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{
				{"{column_name=amount}", int64(1249)}, {"{column_name=flag}", int64(1249)}, {"{column_name=id}", int64(1249)}, {"{column_name=label}", int64(1249)}, {"{schema_name=fdw_source,table_name=import_a}", int64(3118)},
			}))
		})
	})
}
