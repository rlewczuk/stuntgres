package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Own-definition visibility differs from visibility of a previously completed
// import: relation shape precedes table validation, foreign-table metadata follows
// it, and configured column options become visible one completed column at a time.
func TestReferenceFDWSQLValidatorImportOwnVisibility(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		run(fixture.Exec("CREATE TABLE fdw_own_visibility(n integer GENERATED ALWAYS AS IDENTITY,options text,kind oid,relation_visible boolean,foreign_visible boolean,column_count bigint,id_options text,label_options text,amount_options text,flag_options text)", 0))
		const relation = " FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relname='import_a'"
		const foreign = " FROM pg_foreign_table f JOIN pg_class c ON c.oid=f.ftrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relname='import_a'"
		const columns = " FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relname='import_a' AND a.attnum>0 AND NOT a.attisdropped"
		const body = "INSERT INTO fdw_own_visibility(options,kind,relation_visible,foreign_visible,column_count,id_options,label_options,amount_options,flag_options) SELECT $1::text,$2,EXISTS(SELECT 1" + relation + "),EXISTS(SELECT 1" + foreign + "),(SELECT count(*)" + columns + "),(SELECT a.attfdwoptions::text" + columns + " AND a.attname='id'),(SELECT a.attfdwoptions::text" + columns + " AND a.attname='label'),(SELECT a.attfdwoptions::text" + columns + " AND a.attname='amount'),(SELECT a.attfdwoptions::text" + columns + " AND a.attname='flag'); SELECT 7"
		run(fixture.Exec("CREATE FUNCTION fdw_import_own_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$"+body+"$$", 0))
		wrapper := "fdw_native"
		if b.PG {
			wrapper = "postgres_fdw"
			defer c.ExecContext(p.Ctx, "ALTER FOREIGN DATA WRAPPER postgres_fdw VALIDATOR postgres_fdw_validator")
		}
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER "+wrapper+" VALIDATOR fdw_import_own_validator", 0))
		const imported = "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a) FROM SERVER fdw_live INTO fdw_import"
		const direct = "CREATE FOREIGN TABLE fdw_import.import_a(id integer OPTIONS(column_name 'id') NOT NULL,label varchar(7) OPTIONS(column_name 'label'),amount numeric(6,2) OPTIONS(column_name 'amount'),flag boolean OPTIONS(column_name 'flag')) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'import_a')"
		for _, route := range []struct {
			query    string
			prepared bool
		}{{imported, false}, {imported, true}, {direct, false}, {direct, true}} {
			prepared := route.prepared
			s := fixture.Exec(route.query, 0)
			s.Prepared = prepared
			run(s)
			observed := fixture.Select("SELECT options,kind::bigint AS kind,relation_visible,foreign_visible,column_count,id_options,label_options,amount_options,flag_options FROM fdw_own_visibility ORDER BY n", []string{"options", "kind", "relation_visible", "foreign_visible", "column_count", "id_options", "label_options", "amount_options", "flag_options"}, []string{"TEXT", "INT8", "BOOL", "BOOL", "INT8", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
				{"{schema_name=fdw_source,table_name=import_a}", int64(3118), true, false, int64(4), nil, nil, nil, nil},
				{"{column_name=id}", int64(1249), true, true, int64(4), nil, nil, nil, nil},
				{"{column_name=label}", int64(1249), true, true, int64(4), "{column_name=id}", nil, nil, nil},
				{"{column_name=amount}", int64(1249), true, true, int64(4), "{column_name=id}", "{column_name=label}", nil, nil},
				{"{column_name=flag}", int64(1249), true, true, int64(4), "{column_name=id}", "{column_name=label}", "{column_name=amount}", nil},
			})
			observed.Prepared = prepared
			run(observed)
			fdwImportBag(run, "import_a")
			run(fixture.Select("SELECT attname,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_import.import_a'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"id", "{column_name=id}"}, {"label", "{column_name=label}"}, {"amount", "{column_name=amount}"}, {"flag", "{column_name=flag}"}}))
			run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a", 0))
			run(fixture.Exec("DELETE FROM fdw_own_visibility", 5))
		}
	})
}
