package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Preserve the former public visibility boundary witness in the shared fixture.
// Domains now have their own positive and diagnostic acceptance scenarios.
func TestReferenceVisibilityInquiryUtilityBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
			run(fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
			var publicOID int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT relnamespace::bigint FROM pg_catalog.pg_class WHERE oid='public.sentinel'::regclass::oid").Scan(&publicOID); err != nil {
				t.Fatal("independent sentinel namespace", err)
			}
			for _, q := range []string{
				"CREATE TYPE widget AS ENUM ('x')",
				"CREATE COLLATION c FROM pg_catalog.\"C\"",
				"CREATE CONVERSION c FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1",
				"CREATE TEMP TABLE temp_t(id INT)",
				"CREATE MATERIALIZED VIEW mv AS SELECT id FROM sentinel",
				"CREATE FOREIGN TABLE ft(id integer) SERVER visibility_server",
			} {
				if strings.HasPrefix(q, "CREATE TYPE") {
					run(fixture.Select(q, nil, nil, [][]any{}))
					value := fixture.Select("SELECT 'x'::widget AS v", []string{"v"}, []string{""}, [][]any{{"x"}})
					value.CheckMetadata = fixture.EnumReferenceMetadata(backend.PG, false)
					fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, value)
					run(fixture.Select("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					continue
				}
				if strings.HasPrefix(q, "CREATE TEMP TABLE") {
					run(fixture.Select(q, nil, nil, [][]any{}))
					run(fixture.Exec("INSERT INTO temp_t VALUES(42)", 1))
					run(fixture.Select("SELECT id FROM temp_t", []string{"id"}, []string{"INT4"}, [][]any{{int64(42)}}))
					run(fixture.Exec("DROP TABLE temp_t", 0))
					run(fixture.Select("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					continue
				}
				if strings.HasPrefix(q, "CREATE MATERIALIZED") {
					run(fixture.Exec(q, 1))
					run(fixture.Select("SELECT id FROM mv", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(fixture.Exec("DROP MATERIALIZED VIEW mv", 0))
					run(fixture.Select("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					continue
				}
				if strings.HasPrefix(q, "CREATE COLLATION") {
					run(fixture.Select(q, nil, nil, [][]any{}))
					run(fixture.Select("SELECT upper('é' COLLATE c) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
					continue
				}
				if strings.HasPrefix(q, "CREATE CONVERSION") {
					for _, prepared := range []bool{false, true} {
						creation := fixture.Select(q, nil, nil, [][]any{})
						creation.Prepared = prepared
						run(creation)
						run(fixture.Select("SELECT conname,conforencoding,contoencoding,condefault,pg_conversion_is_visible(oid) AS visible FROM pg_catalog.pg_conversion WHERE conname='c' AND connamespace=$1", []string{"conname", "conforencoding", "contoencoding", "condefault", "visible"}, []string{"NAME", "INT4", "INT4", "BOOL", "BOOL"}, [][]any{{"c", int64(6), int64(8), false, true}}, publicOID))
						run(fixture.Exec("DROP CONVERSION c", 0))
						run(fixture.Select("SELECT count(*)::bigint AS count FROM pg_catalog.pg_conversion WHERE conname='c' AND connamespace=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, publicOID))
					}
					run(fixture.Select("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					continue
				}
				if strings.HasPrefix(q, "CREATE FOREIGN") {
					run(fixture.Exec("CREATE FOREIGN DATA WRAPPER visibility_fdw", 0))
					defer func() {
						if _, err := conn.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER visibility_fdw CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					run(fixture.Exec("CREATE SERVER visibility_server FOREIGN DATA WRAPPER visibility_fdw", 0))
					for _, prepared := range []bool{false, true} {
						creation := fixture.Select(q, nil, nil, [][]any{})
						creation.Prepared = prepared
						run(creation)
						run(fixture.Select("SELECT pg_table_is_visible('public.ft'::regclass::oid) AS visible", []string{"visible"}, []string{"BOOL"}, [][]any{{true}}))
						run(fixture.Exec("DROP FOREIGN TABLE ft", 0))
					}
					run(fixture.Select("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					continue
				}
			}
			const defaultSQL = "CREATE TABLE default_boundary(id BOOL DEFAULT pg_type_is_visible(23))"
			if backend.PG {
				run(fixture.Exec(defaultSQL, 0))
				run(fixture.Exec("INSERT INTO default_boundary DEFAULT VALUES", 1))
				run(fixture.Select("SELECT id FROM default_boundary", []string{"id"}, []string{"BOOL"}, [][]any{{true}}))
			} else {
				want := fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
				want.Position = strconv.Itoa(strings.Index(defaultSQL, "pg_type_is_visible") + 1)
				s := fixture.ErrorDiagnostic(defaultSQL, want)
				s.SelectRows = true
				run(s)
			}
		})
	}
}
