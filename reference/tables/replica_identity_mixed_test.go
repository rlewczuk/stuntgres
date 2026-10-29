package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicaIdentityMixedActions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ri_t(k INT NOT NULL,b INT NOT NULL,p TEXT NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO ri_t VALUES(1,2,'one')", 1))
			// The index selected in MISC is created by this statement's ADD pass.
			run(fixture.Exec("ALTER TABLE ri_t ADD CONSTRAINT ri_new UNIQUE(k,b), REPLICA IDENTITY USING INDEX ri_new", 0))
			run(replicaMode("ri_t", "i"))
			for _, column := range []string{"k", "b"} {
				run(fixture.ImplicitError("ALTER TABLE ri_t ALTER "+column+" DROP NOT NULL", fixture.Diagnostic{Code: "42P16", Message: `column "` + column + `" is in index used as replica identity`}))
			}
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY FULL", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX ri_include ON ri_t(k) INCLUDE(p)", 0))
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_include", 0))
			run(fixture.Exec("ALTER TABLE ri_t ALTER p DROP NOT NULL", 0))
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_new", 0))
			run(fixture.Exec("DROP INDEX ri_include", 0))
			run(fixture.Exec("ALTER TABLE ri_t DROP CONSTRAINT ri_new, ALTER k DROP NOT NULL", 0))
			run(replicaMode("ri_t", "i"))
			run(fixture.Select("SELECT indisreplident FROM pg_index WHERE indrelid='ri_t'::regclass", []string{"indisreplident"}, []string{"BOOL"}, nil))
			run(fixture.Exec("ALTER TABLE ri_t ALTER k SET NOT NULL, ADD CONSTRAINT ri_pk PRIMARY KEY(k)", 0))
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_pk", 0))
			run(fixture.ImplicitError("ALTER TABLE ri_t ALTER k DROP NOT NULL", fixture.Diagnostic{Code: "42P16", Message: `column "k" is in a primary key`}))
			run(fixture.Select("SELECT k,b,p FROM ri_t", []string{"k", "b", "p"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "one"}}))
		})
	}
}
