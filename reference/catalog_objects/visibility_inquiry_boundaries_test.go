//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryPostgresOnlyKinds(t *testing.T) {
	p := fixture.NewPair(t)
	tx := visibilityOracleTransaction(t, p)
	exec := func(q string) {
		t.Helper()
		if _, err := tx.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{
		"CREATE SCHEMA pgv_kind_a", "CREATE SCHEMA pgv_kind_b",
		"CREATE TYPE pgv_kind_a.widget AS ENUM ('x')", "CREATE TYPE pgv_kind_b.widget AS ENUM ('x')",
		"CREATE DOMAIN pgv_kind_a.d AS integer", "CREATE DOMAIN pgv_kind_b.d AS integer",
		"CREATE TABLE pgv_kind_a.rows(id integer)", "CREATE TABLE pgv_kind_b.rows(id integer)",
		"CREATE MATERIALIZED VIEW pgv_kind_a.mv AS SELECT id FROM pgv_kind_a.rows", "CREATE MATERIALIZED VIEW pgv_kind_b.mv AS SELECT id FROM pgv_kind_b.rows",
		"CREATE EXTENSION file_fdw", "CREATE SERVER pgv_file FOREIGN DATA WRAPPER file_fdw",
		"CREATE FOREIGN TABLE pgv_kind_a.ft(id integer) SERVER pgv_file OPTIONS(filename '/dev/null',format 'csv')", "CREATE FOREIGN TABLE pgv_kind_b.ft(id integer) SERVER pgv_file OPTIONS(filename '/dev/null',format 'csv')",
		"CREATE COLLATION pgv_kind_a.same FROM pg_catalog.\"C\"", "CREATE COLLATION pgv_kind_b.same FROM pg_catalog.\"C\"",
		"CREATE CONVERSION pgv_kind_a.same FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "CREATE CONVERSION pgv_kind_b.same FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8",
	} {
		exec(q)
	}
	for _, tc := range []struct {
		path string
		a, b bool
	}{{"pgv_kind_a,pgv_kind_b", true, false}, {"pgv_kind_b,pgv_kind_a", false, true}, {"pg_catalog", false, false}} {
		exec("SET LOCAL search_path=" + tc.path)
		for _, subject := range []struct{ fn, cast, name string }{
			{"pg_type_is_visible", "regtype", "widget"}, {"pg_type_is_visible", "regtype", "d"}, {"pg_type_is_visible", "regtype", "rows"},
			{"pg_table_is_visible", "regclass", "rows"}, {"pg_table_is_visible", "regclass", "mv"}, {"pg_table_is_visible", "regclass", "ft"},
			{"pg_collation_is_visible", "regcollation", "same"},
		} {
			q := "SELECT pg_catalog." + subject.fn + "('pgv_kind_a." + subject.name + "'::pg_catalog." + subject.cast + "),pg_catalog." + subject.fn + "('pgv_kind_b." + subject.name + "'::pg_catalog." + subject.cast + ")"
			fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, fixture.Select(q, []string{subject.fn, subject.fn}, []string{"BOOL", "BOOL"}, [][]any{{tc.a, tc.b}}))
		}
		q := "SELECT n.nspname::text AS schema_name,pg_catalog.pg_conversion_is_visible(c.oid) AS visible FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_namespace n ON n.oid=c.connamespace WHERE n.nspname IN ('pgv_kind_a','pgv_kind_b') AND c.conname='same' ORDER BY n.nspname"
		fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, fixture.Select(q, []string{"schema_name", "visible"}, []string{"TEXT", "BOOL"}, [][]any{{"pgv_kind_a", tc.a}, {"pgv_kind_b", tc.b}}))
	}
	// Explicit own TEMP never participates in collation/conversion lookup.
	exec("CREATE TEMP TABLE temp_identity(id integer)")
	exec("CREATE COLLATION pg_temp.temp_same FROM pg_catalog.\"C\"")
	exec("CREATE CONVERSION pg_temp.temp_same FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1")
	exec("SET LOCAL search_path=pg_temp,pg_catalog")
	for _, subject := range []struct{ catalog, namespace, name, fn string }{{"pg_collation", "collnamespace", "collname", "pg_collation_is_visible"}, {"pg_conversion", "connamespace", "conname", "pg_conversion_is_visible"}} {
		q := "SELECT pg_catalog." + subject.fn + "(oid) AS visible FROM pg_catalog." + subject.catalog + " WHERE " + subject.namespace + "=pg_catalog.pg_my_temp_schema() AND " + subject.name + "='temp_same'"
		fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, fixture.Select(q, []string{"visible"}, []string{"BOOL"}, [][]any{{false}}))
	}
}

func TestReferenceVisibilityInquiryPostgresOnlyPermissions(t *testing.T) {
	p := fixture.NewPair(t)
	tx := visibilityOracleTransaction(t, p)
	for _, q := range []string{"CREATE ROLE pgv_visibility_user", "CREATE SCHEMA pgv_access", "CREATE TABLE pgv_access.t(id integer)", "GRANT USAGE ON SCHEMA pgv_access TO pgv_visibility_user"} {
		if _, err := tx.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	var oid int64
	if err := tx.QueryRowContext(p.Ctx, "SELECT 'pgv_access.t'::regclass::oid").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SET LOCAL ROLE pgv_visibility_user", "SET LOCAL search_path=pgv_access,pg_catalog"} {
		if _, err := tx.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	s := fixture.Select("SELECT pg_catalog.pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{true}}, oid)
	fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, s)
	if _, err := tx.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(p.Ctx, "REVOKE USAGE ON SCHEMA pgv_access FROM pgv_visibility_user"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(p.Ctx, "SET LOCAL ROLE pgv_visibility_user"); err != nil {
		t.Fatal(err)
	}
	s.Rows = [][]any{{false}}
	fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, s)
}
