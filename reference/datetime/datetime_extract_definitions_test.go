package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeExtractCanonicalDefinitions(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE dx_definition(s timestamp,n numeric DEFAULT EXTRACT('DoY' FROM DATE '2000-02-29'),g numeric GENERATED ALWAYS AS (EXTRACT(year FROM s)) STORED)",
			"CREATE VIEW dx_definition_view AS SELECT EXTRACT('YeAr' FROM s) AS year,date_part('month',s) AS month,CASE WHEN true THEN EXTRACT(year FROM s) ELSE EXTRACT('bad''\\é' FROM s) END AS safe FROM dx_definition",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		// Pinned PG canonically emits field text raw even when that spelling is
		// not valid SQL. Match its public renderer, not an invented repair. Native
		// logical Dump's independently safe replay is exercised by StoredConsumers.
		definition := " SELECT EXTRACT(YeAr FROM s) AS year,\n    date_part('month'::text, s) AS month,\n        CASE\n            WHEN true THEN EXTRACT(year FROM s)\n            ELSE EXTRACT(bad'\\é FROM s)\n        END AS safe\n   FROM dx_definition;"
		datetimeExtractBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('dx_definition_view'::regclass,true)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{definition}}))
		datetimeExtractBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(d.adbin,d.adrelid) FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='dx_definition' ORDER BY d.adnum", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"EXTRACT(DoY FROM '2000-02-29'::date)"}, {"EXTRACT(year FROM s)"}}))
	})
}

func TestReferenceDatetimeExtractStrictDomainSource(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN dx_source_domain AS timestamp", 0))
		q := "SELECT date_part(NULL::text,$1::dx_source_domain)"
		d := fixture.ControlsReferenceDiagnostic("22007", `invalid input syntax for type timestamp: "nonsense"`)
		d.Context = "unnamed portal parameter $1 = '...'"
		datetimeExtractFailure(t, p, b, c, q, d, "execute", "nonsense")
		datetimeExtractBoth(t, p, b, c, fixture.Select(q, []string{"date_part"}, []string{"FLOAT8"}, [][]any{{nil}}, "2000-01-01"))
		datetimeExtractBoth(t, p, b, c, fixture.Select("SELECT EXTRACT(year FROM $1::dx_source_domain),date_part('year',$1::dx_source_domain)", []string{"extract", "date_part"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"2000", float64(2000)}}, "2000-01-01"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dx_domain_sources(s dx_source_domain,CONSTRAINT dx_source_check CHECK(NOT(EXTRACT(year FROM s)=1999)))", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dx_domain_sources VALUES(TIMESTAMP '2000-01-01')", 1))
		check := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "dx_domain_sources" violates check constraint "dx_source_check"`)
		check.Detail, check.Schema, check.Table, check.Constraint = "Failing row contains (1999-01-01 00:00:00).", "public", "dx_domain_sources", "dx_source_check"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO dx_domain_sources VALUES(TIMESTAMP '2000-01-01'),(TIMESTAMP '1999-01-01')", check)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT EXTRACT(year FROM s) AS year FROM dx_domain_sources", []string{"year"}, []string{"NUMERIC"}, [][]any{{"2000"}}))
		}
	})
}
