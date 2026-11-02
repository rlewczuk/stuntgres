package reference_test

import (
	"strings"

	"stuntgres/reference/fixture"
)

// These declarations use the ordinary shared SQL/MED surface, not a transport
// callback. Contracts reruns their independent metadata/value checks after its
// native Dump/Reset/Restore, including unqualified BYTEA OPTIONS in dump output.
func externalSQLColumnOptionSetup(run func(fixture.Step)) {
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE DOMAIN options AS bytea", 0),
		fixture.Exec(`CREATE DOMAIN "Byte Kind" AS bytea`, 0),
		fixture.Exec(`CREATE FOREIGN TABLE external_sql_option_columns("Options" bytea OPTIONS(column_name 'remote_bytes') NOT NULL,qualified pg_catalog.bytea OPTIONS(column_name 'remote_bytes'),custom options OPTIONS(column_name 'remote_bytes'),"Quoted" public."Byte Kind" OPTIONS(column_name 'remote_bytes'),array_col bytea[] OPTIONS(column_name 'remote_array')) SERVER external_sql_metadata_server`, 0),
		fixture.Exec("ALTER FOREIGN TABLE external_sql_option_columns ADD COLUMN added bytea OPTIONS(column_name 'remote_bytes') NOT NULL", 0),
		fixture.Exec("ALTER FOREIGN TABLE external_sql_option_columns ADD COLUMN IF NOT EXISTS guarded options OPTIONS(column_name 'remote_bytes')", 0),
		fixture.Exec("ALTER TABLE external_sql_option_columns ADD COLUMN ordinary bytea OPTIONS(column_name 'remote_bytes')", 0),
		// A skipped ADD returns before column transformation; the attempted
		// extra option below must not alter the existing foreign column.
		fixture.Exec("ALTER TABLE external_sql_option_columns ADD COLUMN IF NOT EXISTS ordinary bytea OPTIONS(extra 'kept')", 0),
		fixture.Exec("CREATE TABLE external_sql_options_name(options integer)", 0),
		fixture.Exec("INSERT INTO external_sql_options_name VALUES(7)", 1),
		fixture.Exec("CREATE FUNCTION options(v integer) RETURNS integer LANGUAGE SQL AS 'SELECT v'", 0),
	} {
		run(s)
	}
}

func externalSQLColumnOptionChecks() []fixture.Step {
	steps := []fixture.Step{
		fixture.Select("SELECT attname::text AS name,attnotnull,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='external_sql_option_columns'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"name", "attnotnull", "options"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{
			{"Options", true, "{column_name=remote_bytes}"},
			{"qualified", false, "{column_name=remote_bytes}"},
			{"custom", false, "{column_name=remote_bytes}"},
			{"Quoted", false, "{column_name=remote_bytes}"},
			{"array_col", false, "{column_name=remote_array}"},
			{"added", true, "{column_name=remote_bytes}"},
			{"guarded", false, "{column_name=remote_bytes}"},
			{"ordinary", false, "{column_name=remote_bytes}"},
		}),
		fixture.Select("SELECT options FROM external_sql_options_name", []string{"options"}, []string{"INT4"}, [][]any{{int64(7)}}),
		fixture.Select("SELECT options($1::integer) AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(7)}}, int64(7)),
		fixture.Select("SELECT options($1::integer) AS value", []string{"value"}, []string{"INT4"}, [][]any{{nil}}, nil),
	}
	for _, column := range []struct{ name, typ string }{
		{"Options", "bytea"}, {"qualified", "pg_catalog.bytea"},
		{"custom", "options"}, {"Quoted", `public."Byte Kind"`},
		{"array_col", "bytea[]"}, {"added", "bytea"}, {"guarded", "options"},
		{"ordinary", "bytea"},
	} {
		steps = append(steps, fixture.Select("SELECT atttypid=$1::regtype::oid AS same_type FROM pg_attribute WHERE attrelid='external_sql_option_columns'::regclass::oid AND attname=$2", []string{"same_type"}, []string{"BOOL"}, [][]any{{true}}, column.typ, column.name))
	}
	// REL_18_6 transformColumnDefinition queues ALTER FOREIGN TABLE for
	// column fdwoptions even on an ordinary table. The resulting kind check
	// reports 42809 without Position or other logical diagnostic fields.
	for _, ordinary := range []struct{ sql, table string }{
		{"CREATE TABLE external_sql_bad_plain(v bytea OPTIONS(column_name 'v'))", "external_sql_bad_plain"},
		{"ALTER TABLE external_sql_options_name ADD COLUMN v bytea OPTIONS(column_name 'v')", "external_sql_options_name"},
		{"ALTER TABLE external_sql_options_name ADD COLUMN before_options integer, ADD COLUMN v bytea OPTIONS(column_name 'v')", "external_sql_options_name"},
	} {
		want := fixture.ControlsReferenceDiagnostic("42809", `"`+ordinary.table+`" is not a foreign table`)
		failure := fixture.ErrorDiagnostic(ordinary.sql, want)
		failure.ErrorStage = "execute"
		steps = append(steps, failure,
			fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relnamespace='public'::regnamespace::oid AND relname='external_sql_bad_plain'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			fixture.Select("SELECT attname::text AS name FROM pg_attribute WHERE attrelid='external_sql_options_name'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"name"}, []string{"TEXT"}, [][]any{{"options"}}),
			fixture.Select("SELECT options FROM external_sql_options_name", []string{"options"}, []string{"INT4"}, [][]any{{int64(7)}}),
		)
	}
	steps = append(steps,
		fixture.Exec("CREATE TABLE IF NOT EXISTS external_sql_options_name(v bytea OPTIONS(column_name 'v'))", 0),
		fixture.Exec("ALTER TABLE IF EXISTS external_sql_missing_options ADD COLUMN v bytea OPTIONS(column_name 'v')", 0),
		fixture.Exec("ALTER TABLE external_sql_options_name ADD COLUMN IF NOT EXISTS options integer OPTIONS(column_name 'v')", 0),
		fixture.Select("SELECT attname::text AS name FROM pg_attribute WHERE attrelid='external_sql_options_name'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"name"}, []string{"TEXT"}, [][]any{{"options"}}),
		fixture.Select("SELECT options FROM external_sql_options_name", []string{"options"}, []string{"INT4"}, [][]any{{int64(7)}}),
	)
	for _, q := range []string{
		"CREATE FOREIGN TABLE external_sql_bad_order(v bytea NOT NULL OPTIONS(column_name 'v')) SERVER external_sql_metadata_server",
		"CREATE TABLE external_sql_bad_type(v bytea nonsense)",
		"CREATE FOREIGN TABLE external_sql_bad_type(v bytea nonsense) SERVER external_sql_metadata_server",
	} {
		token := "OPTIONS"
		if strings.Contains(q, "nonsense") {
			token = "nonsense"
		}
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+token+`"`)
		want.Position = fixture.Position(strings.Index(q, token) + 1)
		steps = append(steps, fixture.ErrorDiagnostic(q, want))
	}
	steps = append(steps, fixture.Select("SELECT options FROM external_sql_options_name", []string{"options"}, []string{"INT4"}, [][]any{{int64(7)}}))
	return steps
}
