package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedExtensionDrop(t *testing.T) {
	for _, external := range []bool{false, true} {
		name := "automatic"
		if external {
			name = "external"
		}
		t.Run(name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "alter_addon")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					defer c.ExecContext(context.Background(), "ROLLBACK")
					if b.PG {
						if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS unaccent CASCADE"); err != nil {
							t.Fatal(err)
						}
						defer c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS unaccent CASCADE")
					}
					notices = nil
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
					alterSeed(run)
					alterExtensionSeed(run)
					alterIndexSeed(run)
					run(fixture.Exec("ALTER MATERIALIZED VIEW indexed DEPENDS ON EXTENSION unaccent", 0))
					run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv DEPENDS ON EXTENSION unaccent", 0))
					run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv NO DEPENDS ON EXTENSION unaccent", 0))
					if external {
						run(fixture.Exec("CREATE VIEW ext_drop_attached_view AS SELECT i FROM indexed", 0))
						want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop extension unaccent because other objects depend on it")
						want.Detail = "view ext_drop_attached_view depends on materialized view indexed"
						want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
						alterError(t, p, c, "DROP EXTENSION unaccent RESTRICT", want, "execute")
						alterX(run, "indexed", 1)
						run(fixture.Select("SELECT i FROM ext_drop_attached_view ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					}
					if len(notices) != 0 {
						t.Error("automatic/failed DROP notices", notices)
					}
					mode := " RESTRICT"
					if external {
						mode = " CASCADE"
					}
					run(fixture.Prepared(fixture.Exec("DROP EXTENSION unaccent"+mode, 0)))
					var want []fixture.Diagnostic
					if external {
						want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to view ext_drop_attached_view"}}
					}
					if len(notices) != 0 || len(want) != 0 {
						if !reflect.DeepEqual(notices, want) {
							t.Errorf("DROP notices got=%#v want=%#v", notices, want)
						}
					}
					run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='unaccent'", []string{"extname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace::oid AND relname IN ('indexed','mv_u','ext_drop_attached_view')", []string{"relname"}, []string{"NAME"}, nil))
					run(materializedViewBag("alter_mv"))
					run(fixture.Exec("REFRESH MATERIALIZED VIEW alter_mv", 0))
					run(fixture.Exec("CREATE TABLE alter_addon.ext_drop_schema_keep(v int)", 0))
				})
			}
		})
	}
}
