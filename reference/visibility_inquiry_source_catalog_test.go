package reference_test

import "testing"

func TestReferenceVisibilityInquiryOffPathSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			db.SetMaxOpenConns(1)
			run := func(s referenceStep) {
				s.nonContext = true
				s.checkMetadata = visibilityBoolMetadata
				referenceRunStep(t, p.ctx, db, backend.recorder, s)
			}
			t.Cleanup(func() {
				if _, err := db.Exec("DROP SCHEMA IF EXISTS off_path CASCADE"); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"CREATE SCHEMA off_path", "CREATE TABLE off_path.subject(id INT)", "CREATE TABLE relation_checks(id OID CONSTRAINT known CHECK(pg_table_is_visible(id) IS NOT NULL))"} {
				run(referenceExec(q, 0))
			}
			var oid int64
			if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='subject'").Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
				t.Fatal(oid, err)
			}
			run(referenceExec("INSERT INTO relation_checks VALUES($1)", 1, oid))
			run(referenceSelect("SELECT pg_table_is_visible(id) FROM relation_checks", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{false}}))
			bad := referenceImplicitError("INSERT INTO relation_checks VALUES(0)", referenceDiagnostic{Code: "23514", Message: `new row for relation "relation_checks" violates check constraint "known"`, Detail: "Failing row contains (0).", Schema: "public", Table: "relation_checks", Constraint: "known"})
			bad.errorStage = "execute"
			run(bad)
			run(referenceSelect("SELECT count(*) FROM relation_checks", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceVisibilityInquiryCatalogPlacementSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			db.SetMaxOpenConns(1)
			run := func(s referenceStep) {
				s.nonContext = true
				s.checkMetadata = visibilityBoolMetadata
				referenceRunStep(t, p.ctx, db, backend.recorder, s)
			}
			t.Cleanup(func() {
				if _, err := db.Exec("DROP SCHEMA IF EXISTS a CASCADE"); err != nil {
					t.Error(err)
				}
			})
			run(referenceExec("CREATE SCHEMA a", 0))
			run(referenceExec("CREATE TABLE a.pg_class(id INT)", 0))
			for _, tc := range []struct {
				path          string
				catalog, user bool
			}{{"a", true, false}, {"a,pg_catalog", false, true}, {"pg_catalog,a", true, false}, {"''", true, false}, {"missing,a", true, false}} {
				run(referenceExec("SET search_path="+tc.path, 0))
				run(referenceSelect("SELECT pg_table_is_visible('pg_catalog.pg_class'::regclass),pg_table_is_visible('a.pg_class'::regclass)", []string{"pg_table_is_visible", "pg_table_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{tc.catalog, tc.user}}))
				run(referenceSelect("SELECT pg_type_is_visible(23),pg_collation_is_visible(811),pg_conversion_is_visible(4402)", []string{"pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			}
		})
	}
}
