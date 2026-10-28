package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func pgTriggerOID(t *testing.T, p *fixture.Pair, b fixture.Backend, table, name string) int64 {
	t.Helper()
	var id int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_trigger WHERE tgrelid=$1::regclass AND tgname=$2::name", table, name).Scan(&id); err != nil || id <= 0 {
		t.Fatal("actual trigger identity", id, err)
	}
	return id
}

func TestReferencePGTriggerIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgt_identity")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{"CREATE SCHEMA pgt_identity", "CREATE TABLE pgt_identity.first(a integer,b integer,c integer)", "CREATE TABLE pgt_identity.second(a integer)", "CREATE FUNCTION pgt_identity.fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE FUNCTION pgt_identity.other_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER same BEFORE INSERT ON pgt_identity.first FOR EACH ROW EXECUTE FUNCTION pgt_identity.fn()", "CREATE TRIGGER same BEFORE INSERT ON pgt_identity.second FOR EACH ROW EXECUTE FUNCTION pgt_identity.fn()"} {
				pgTriggerExec(t, p, b, b.DB, q)
			}
			first := pgTriggerOID(t, p, b, "pgt_identity.first", "same")
			second := pgTriggerOID(t, p, b, "pgt_identity.second", "same")
			if first == second {
				t.Fatal("table-local triggers share identity")
			}
			query := "SELECT t.oid,n.nspname,c.relname,pn.nspname AS function_schema,p.proname,t.tgfoid=p.oid AS function_link FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace WHERE t.oid=$1::oid"
			pgTriggerBoth(t, p, b, fixture.Select(query, []string{"oid", "nspname", "relname", "function_schema", "proname", "function_link"}, []string{"OID", "NAME", "NAME", "NAME", "NAME", "BOOL"}, [][]any{{first, "pgt_identity", "first", "pgt_identity", "fn", true}}, first))
			pgTriggerBoth(t, p, b, fixture.Select("SELECT p.pronargs,p.prorettype,p.proargtypes::text AS inputs FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.oid=$1::oid", []string{"pronargs", "prorettype", "inputs"}, []string{"INT2", "OID", "TEXT"}, [][]any{{int64(0), int64(2279), ""}}, first))
			pgTriggerExec(t, p, b, b.DB, "ALTER TRIGGER same ON pgt_identity.first RENAME TO renamed")
			if pgTriggerOID(t, p, b, "pgt_identity.first", "renamed") != first {
				t.Fatal("rename replaced identity")
			}
			pgTriggerExec(t, p, b, b.DB, "CREATE OR REPLACE TRIGGER renamed AFTER UPDATE OF c,a ON pgt_identity.first FOR EACH ROW WHEN(OLD.a>0) EXECUTE FUNCTION pgt_identity.other_fn('changed')")
			if pgTriggerOID(t, p, b, "pgt_identity.first", "renamed") != first {
				t.Fatal("replace changed identity")
			}
			pgTriggerExec(t, p, b, b.DB, "ALTER TABLE pgt_identity.first RENAME COLUMN a TO renamed_a")
			pgTriggerExec(t, p, b, b.DB, "ALTER TABLE pgt_identity.first RENAME TO moved")
			pgTriggerExec(t, p, b, b.DB, "ALTER FUNCTION pgt_identity.other_fn() RENAME TO moved_fn")
			pgTriggerLive(t, p, b, b.DB, "pgt_identity.moved", [][]any{{"renamed", int64(17), "O", int64(1), "6368616e67656400", "3 1", true}})
			pgTriggerBoth(t, p, b, fixture.Select(query, []string{"oid", "nspname", "relname", "function_schema", "proname", "function_link"}, []string{"OID", "NAME", "NAME", "NAME", "NAME", "BOOL"}, [][]any{{first, "pgt_identity", "moved", "pgt_identity", "moved_fn", true}}, first))
			pgTriggerExec(t, p, b, b.DB, "CREATE TABLE public.moved(a integer,b integer,c integer)")
			pgTriggerExec(t, p, b, b.DB, "SET search_path TO public,pgt_identity")
			if pgTriggerOID(t, p, b, "pgt_identity.moved", "renamed") != first {
				t.Fatal("search_path retargeted identity")
			}
			pgTriggerLive(t, p, b, b.DB, "public.moved", [][]any{})
			pgTriggerExec(t, p, b, b.DB, "RESET search_path")
			for _, q := range []string{"CREATE TABLE pgt_parent(a integer) PARTITION BY RANGE(a)", "CREATE TABLE pgt_child PARTITION OF pgt_parent FOR VALUES FROM(0) TO(10)", "CREATE TRIGGER clone BEFORE INSERT ON pgt_parent FOR EACH ROW EXECUTE FUNCTION pgt_identity.fn()"} {
				pgTriggerExec(t, p, b, b.DB, q)
			}
			parent := pgTriggerOID(t, p, b, "pgt_parent", "clone")
			child := pgTriggerOID(t, p, b, "pgt_child", "clone")
			if child == parent {
				t.Fatal("clone identity reused")
			}
			pgTriggerBoth(t, p, b, fixture.Select("SELECT t.oid,t.tgparentid,c.relname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE t.oid=$1::oid OR t.oid=$2::oid ORDER BY c.relname", []string{"oid", "tgparentid", "relname"}, []string{"OID", "OID", "NAME"}, [][]any{{child, parent, "pgt_child"}, {parent, int64(0), "pgt_parent"}}, parent, child))
			pgTriggerExec(t, p, b, b.DB, "ALTER TABLE pgt_parent DETACH PARTITION pgt_child")
			pgTriggerLive(t, p, b, b.DB, "pgt_child", [][]any{})
			pgTriggerExec(t, p, b, b.DB, "ALTER TABLE pgt_parent ATTACH PARTITION pgt_child FOR VALUES FROM(0) TO(10)")
			if pgTriggerOID(t, p, b, "pgt_child", "clone") == parent {
				t.Fatal("reattachment reused parent")
			}
			pgTriggerExec(t, p, b, b.DB, "DROP TRIGGER clone ON pgt_parent")
			pgTriggerLive(t, p, b, b.DB, "pgt_child", [][]any{})
			if pgTriggerOID(t, p, b, "pgt_identity.second", "same") != second {
				t.Fatal("unrelated target changed")
			}
		})
	}
}
