package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTriggerVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgt_reader"})
	fixture.SchemaReferenceCleanup(t, "pgt_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, q := range []string{"CREATE ROLE pgt_reader", "CREATE SCHEMA pgt_private", "CREATE TABLE pgt_private.target(a integer)", "CREATE FUNCTION pgt_private.fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER visible BEFORE INSERT ON pgt_private.target FOR EACH ROW WHEN(NEW.a>0) EXECUTE FUNCTION pgt_private.fn()"} {
				pgTriggerExec(t, p, b, owner, q)
			}
			var oid int64
			if err := owner.QueryRowContext(p.Ctx, "SELECT 'pgt_private.target'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			query := "SELECT tgname,tgqual IS NOT NULL AS has_qual FROM pg_catalog.pg_trigger WHERE tgrelid=$1::oid"
			step := fixture.Select(query, []string{"tgname", "has_qual"}, []string{"NAME", "BOOL"}, [][]any{{"visible", true}}, oid)
			pgTriggerExec(t, p, b, reader, "SET ROLE pgt_reader")
			defer reader.ExecContext(p.Ctx, "RESET ROLE")
			fixture.PgDescriptionExecutorRows(t, p, b, reader, step)
			fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Prepared(step))
			pgTriggerExec(t, p, b, reader, "SET search_path TO pg_catalog")
			fixture.PgDescriptionExecutorRows(t, p, b, reader, step)
			fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Select("SELECT trigger_name FROM information_schema.triggers WHERE trigger_schema='pgt_private'", []string{"trigger_name"}, []string{"NAME"}, [][]any{}))
			pgTriggerExec(t, p, b, owner, "CREATE TEMP TABLE pgt_temp(a integer)")
			pgTriggerExec(t, p, b, owner, "CREATE TRIGGER temporary BEFORE INSERT ON pgt_temp FOR EACH ROW EXECUTE FUNCTION pgt_private.fn()")
			var temp int64
			if err := owner.QueryRowContext(p.Ctx, "SELECT 'pgt_temp'::regclass::oid::bigint").Scan(&temp); err != nil {
				t.Fatal(err)
			}
			step.Args = []any{temp}
			step.Rows = [][]any{{"temporary", false}}
			fixture.PgDescriptionExecutorRows(t, p, b, reader, step)
			fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Prepared(step))
			fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Select("SELECT c.relname,n.nspname LIKE 'pg_temp_%' AS temporary FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE t.tgrelid=$1::oid", []string{"relname", "temporary"}, []string{"NAME", "BOOL"}, [][]any{{"pgt_temp", true}}, temp))
			pgTriggerExec(t, p, b, owner, "DROP TABLE pgt_temp")
			step.Rows = [][]any{}
			fixture.PgDescriptionExecutorRows(t, p, b, reader, step)
			pgTriggerExec(t, p, b, reader, "RESET ROLE")
		})
	}
}
