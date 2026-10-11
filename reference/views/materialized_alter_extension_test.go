package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func alterExtensionSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE SCHEMA alter_addon", 0))
	run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA alter_addon", 0))
}
func alterX(run func(fixture.Step), name string, count int64) {
	run(fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1259 AND d.objid='"+name+"'::regclass::oid AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='x' AND e.extname='unaccent'", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
	run(fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid=3079 AND d.deptype='e' AND e.extname='unaccent'", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
}
func TestReferenceMaterializedAlterExtension(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_xowner"})
	fixture.SchemaReferenceCleanup(t, "alter_addon", "alter_xs2")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			alterSeed(run)
			alterExtensionSeed(run)
			for range 2 {
				run(fixture.Prepared(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv DEPENDS ON EXTENSION unaccent", 0)))
				alterX(run, "alter_mv", 1)
			}
			ids := alterIdentity(t, p, c, "alter_mv")
			run(fixture.Exec("CREATE USER alter_xowner", 0))
			run(fixture.Exec("CREATE SCHEMA alter_xs2 AUTHORIZATION alter_xowner", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME TO saved", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW saved SET SCHEMA alter_xs2", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_xs2.saved OWNER TO alter_xowner", 0))
			if got := alterIdentity(t, p, c, "alter_xs2.saved"); got != ids {
				t.Fatal("x identity changed", got, ids)
			}
			alterX(run, "alter_xs2.saved", 1)
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_xowner", 0))
			for range 2 {
				run(fixture.Exec("ALTER MATERIALIZED VIEW alter_xs2.saved NO DEPENDS ON EXTENSION unaccent", 0))
			}
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_xs2.saved DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			alterX(run, "alter_xs2.saved", 1)
			for range 2 {
				run(fixture.Prepared(fixture.Exec("ALTER MATERIALIZED VIEW alter_xs2.saved NO DEPENDS ON EXTENSION unaccent", 0)))
				alterX(run, "alter_xs2.saved", 0)
			}
			run(alterBag("alter_xs2.saved", alterThree))
			run(fixture.Exec("DROP MATERIALIZED VIEW alter_xs2.saved", 0))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='unaccent'", []string{"extname"}, []string{"NAME"}, [][]any{{"unaccent"}}))
		})
	}
}
func TestReferenceMaterializedAlterExtensionRetirement(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "alter_addon")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			records := new([]fixture.Diagnostic)
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, records)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
			alterSeed(run)
			alterExtensionSeed(run)
			alterIndexSeed(run)
			run(fixture.Exec("ALTER MATERIALIZED VIEW indexed DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("CREATE VIEW attached_view AS SELECT i FROM indexed", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv NO DEPENDS ON EXTENSION unaccent", 0))
			wantRestrict := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop schema alter_addon because other objects depend on it")
			wantRestrict.Detail = "extension unaccent depends on schema alter_addon\nview attached_view depends on materialized view indexed"
			wantRestrict.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			alterError(t, p, c, "DROP SCHEMA alter_addon RESTRICT", wantRestrict, "execute")
			alterX(run, "indexed", 1)
			run(materializedViewBag("alter_mv"))
			run(fixture.Exec("DROP SCHEMA alter_addon CASCADE", 0))
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to extension unaccent\n" + "drop cascades to view attached_view"}
			if len(*records) != 1 || (*records)[0] != wantNotice {
				t.Errorf("ORACLE provider CASCADE notices=%#v want=%#v", *records, wantNotice)
			}
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='unaccent'", []string{"extname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='indexed' OR relname='mv_u' OR relname='attached_view'", []string{"relname"}, []string{"NAME"}, nil))
			run(materializedViewBag("alter_mv"))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW alter_mv", 0))
			// Ordinary source dependency remains after removing x; provider retirement is
			// not permission to discard the defining table dependency.
			wantSource := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table mv_base because other objects depend on it")
			wantSource.Detail = "materialized view alter_mv depends on table mv_base"
			wantSource.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			alterError(t, p, c, "DROP TABLE mv_base RESTRICT", wantSource, "execute")
			*records = nil
			run(fixture.Exec("DROP TABLE mv_base CASCADE", 0))
			wantSourceNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to materialized view alter_mv"}
			if len(*records) != 1 || (*records)[0] != wantSourceNotice {
				t.Errorf("ORACLE source CASCADE notices=%#v want=%#v", *records, wantSourceNotice)
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='alter_mv'", []string{"relname"}, []string{"NAME"}, nil))
		})
	}
}
