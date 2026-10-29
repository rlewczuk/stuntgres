package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicaIdentityNamespace(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ri_shadow")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SCHEMA ri_shadow", `CREATE TABLE public."ri ""t"(k INT NOT NULL)`, `CREATE UNIQUE INDEX "ri ""key" ON public."ri ""t"(k)`,
				"CREATE TABLE ri_shadow.ri_other(k INT NOT NULL)", `CREATE UNIQUE INDEX "ri ""key" ON ri_shadow.ri_other(k)`,
				"SET search_path=ri_shadow,public", `ALTER TABLE public."ri ""t" REPLICA IDENTITY USING INDEX "ri ""key"`,
			} {
				run(fixture.Exec(q, 0))
			}
			run(replicaMode(`public."ri ""t"`, "i"))
			run(replicaMode("ri_shadow.ri_other", "d"))
			run(fixture.Select(`SELECT n.nspname,i.indisreplident FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='ri "key' ORDER BY n.nspname`, []string{"nspname", "indisreplident"}, []string{"NAME", "BOOL"}, [][]any{{"public", true}, {"ri_shadow", false}}))
			run(fixture.Exec("RESET search_path", 0))
		})
	}
}

func TestReferenceReplicaIdentityEvolution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ri_move_fail")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ri_t(k INT NOT NULL,b INT NOT NULL,p TEXT)", 0))
			run(fixture.Exec("INSERT INTO ri_t VALUES(1,2,'one'),(3,4,NULL)", 2))
			run(fixture.Exec("CREATE UNIQUE INDEX ri_k ON ri_t(k)", 0))
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k", 0))
			var before int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ri_k'").Scan(&before); err != nil || before <= 0 {
				t.Fatal(before, err)
			}
			run(fixture.ImplicitError("ALTER TABLE ri_t ALTER k TYPE BIGINT USING 0", fixture.Diagnostic{Code: "23505", Message: `could not create unique index "ri_k"`, Detail: "Key (k)=(0) is duplicated.", Schema: "public", Table: "ri_t", Constraint: "ri_k"}))
			var after int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ri_k'").Scan(&after); err != nil || after != before {
				t.Fatal("failed TYPE replaced selected identity", before, after, err)
			}
			run(replicaMode("ri_t", "i"))
			run(replicaData())
			run(fixture.Exec("CREATE SCHEMA ri_move_fail", 0))
			run(fixture.Exec("CREATE TABLE ri_move_fail.ri_k(k INT)", 0))
			run(fixture.ImplicitError("ALTER TABLE ri_t SET SCHEMA ri_move_fail", fixture.Diagnostic{Code: "42P07", Message: `relation "ri_k" already exists in schema "ri_move_fail"`}))
			run(replicaMode("ri_t", "i"))
			// Attaching the real standalone selection preserves its bit through rename.
			run(fixture.Exec("ALTER TABLE ri_t ADD CONSTRAINT ri_attached UNIQUE USING INDEX ri_k", 0))
			run(fixture.Select("SELECT c.relname,i.indisreplident FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='ri_t'::regclass", []string{"relname", "indisreplident"}, []string{"NAME", "BOOL"}, [][]any{{"ri_attached", true}}))
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY FULL", 0))
			run(fixture.Exec("ALTER TABLE ri_t ALTER k DROP NOT NULL", 0))
			run(fixture.ImplicitError("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_attached", fixture.Diagnostic{Code: "42809", Message: `index "ri_attached" cannot be used as replica identity because column "k" is nullable`}))
			run(replicaMode("ri_t", "f"))
			run(replicaData())
		})
	}
}

func TestReferenceReplicaIdentityPartitionedIndex(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{
		"CREATE TABLE ri_part(k INT NOT NULL) PARTITION BY RANGE(k)",
		"CREATE TABLE ri_leaf PARTITION OF ri_part FOR VALUES FROM(0) TO(10)",
		"CREATE UNIQUE INDEX ri_partition_key ON ONLY ri_part(k)",
		"ALTER TABLE ri_part REPLICA IDENTITY USING INDEX ri_partition_key",
	} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, replicaMode("ri_part", "i"))
	p.Step(t, replicaMode("ri_leaf", "d"))
	p.Step(t, fixture.Select("SELECT indisvalid,indisreplident FROM pg_index WHERE indexrelid='ri_partition_key'::regclass", []string{"indisvalid", "indisreplident"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
}
