package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryOffPathSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			db.SetMaxOpenConns(1)
			run := func(s fixture.Step) {
				s.NonContext = true
				s.CheckMetadata = fixture.VisibilityBoolMetadata
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
			}
			t.Cleanup(func() {
				if _, err := db.Exec("DROP SCHEMA IF EXISTS off_path CASCADE"); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"CREATE SCHEMA off_path", "CREATE TABLE off_path.subject(id INT)", "CREATE TABLE relation_checks(id OID CONSTRAINT known CHECK(pg_table_is_visible(id) IS NOT NULL))"} {
				run(fixture.Exec(q, 0))
			}
			var oid int64
			if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='subject'").Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
				t.Fatal(oid, err)
			}
			run(fixture.Exec("INSERT INTO relation_checks VALUES($1)", 1, oid))
			run(fixture.Select("SELECT pg_table_is_visible(id) FROM relation_checks", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{false}}))
			bad := fixture.ImplicitError("INSERT INTO relation_checks VALUES(0)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "relation_checks" violates check constraint "known"`, Detail: "Failing row contains (0).", Schema: "public", Table: "relation_checks", Constraint: "known"})
			bad.ErrorStage = "execute"
			run(bad)
			run(fixture.Select("SELECT count(*) FROM relation_checks", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceVisibilityInquiryCatalogPlacementSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			db.SetMaxOpenConns(1)
			run := func(s fixture.Step) {
				s.NonContext = true
				s.CheckMetadata = fixture.VisibilityBoolMetadata
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
			}
			t.Cleanup(func() {
				if _, err := db.Exec("DROP SCHEMA IF EXISTS a CASCADE"); err != nil {
					t.Error(err)
				}
			})
			run(fixture.Exec("CREATE SCHEMA a", 0))
			run(fixture.Exec("CREATE TABLE a.pg_class(id INT)", 0))
			for _, tc := range []struct {
				path          string
				catalog, user bool
			}{{"a", true, false}, {"a,pg_catalog", false, true}, {"pg_catalog,a", true, false}, {"''", true, false}, {"missing,a", true, false}} {
				run(fixture.Exec("SET search_path="+tc.path, 0))
				run(fixture.Select("SELECT pg_table_is_visible('pg_catalog.pg_class'::regclass),pg_table_is_visible('a.pg_class'::regclass)", []string{"pg_table_is_visible", "pg_table_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{tc.catalog, tc.user}}))
				run(fixture.Select("SELECT pg_type_is_visible(23),pg_collation_is_visible(811),pg_conversion_is_visible(4402)", []string{"pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			}
		})
	}
}
