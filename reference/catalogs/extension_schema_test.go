package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations follow PostgreSQL's normal namespace dependency and extension
// ownership traversal, not the native implementation's descriptor iteration.
func TestReferenceExtensionSchemaDependencies(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		o := extensionTemporaryOwner(t, p, pg)
		defer o.close(t)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, o.conn, o.recorder, s) }
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE TEMP TABLE ext_schema_peer_seed(v INT)", 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("INSERT INTO ext_schema_peer_seed VALUES(73)", 1))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec(`CREATE COLLATION pg_temp.ext_schema_peer_provider FROM pg_catalog."C"`, 0))
		var provider int64
		if err := peer.QueryRowContext(p.Ctx, "SELECT oid FROM pg_collation WHERE collname='ext_schema_peer_provider'").Scan(&provider); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE SCHEMA ext_locked", 0))
		run(fixture.Exec("CREATE TABLE ext_locked.keep(v INT)", 0))
		run(fixture.Exec("INSERT INTO ext_locked.keep VALUES(77)", 1))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		id := extensionTempCapture(t, p, o.conn)
		extensionNamespaceDependency(t, p, o.conn, o.recorder)
		extensionTempConsumers(t, p, o.conn, o.recorder, "ext_addon")
		detail := "extension unaccent depends on schema ext_addon\n" +
			"default value for column txt of table ext_temp_consumer depends on function ext_addon.unaccent(text)\n" +
			"constraint ext_temp_check on table ext_temp_consumer depends on function ext_addon.unaccent(text)\n" +
			"view ext_temp_view depends on function ext_addon.unaccent(text)\n" +
			"view ext_temp_chain depends on view ext_temp_view"
		run(fixture.ImplicitError("DROP SCHEMA ext_addon", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop schema ext_addon because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		extensionTempPresent(t, p, peer, r, id)
		extensionTempConsumerState(t, p, peer, r, true)
		run(fixture.ImplicitError("DROP SCHEMA ext_addon, missing_ext_schema CASCADE", fixture.Diagnostic{Code: "3F000", Message: `schema "missing_ext_schema" does not exist`}))
		extensionTempPresent(t, p, peer, r, id)
		extensionTempConsumerState(t, p, peer, r, true)
		if len(o.notices) != 0 {
			t.Fatal("failed DROP SCHEMA emitted notices", o.notices)
		}
		run(fixture.Exec("DROP SCHEMA ext_addon CASCADE", 0))
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 5 other objects", Detail: "drop cascades to extension unaccent\n" +
			"drop cascades to default value for column txt of table ext_temp_consumer\n" +
			"drop cascades to constraint ext_temp_check on table ext_temp_consumer\n" +
			"drop cascades to view ext_temp_view\n" +
			"drop cascades to view ext_temp_chain"}}
		if !reflect.DeepEqual(o.notices, want) {
			t.Fatalf("schema cascade notices %#v want %#v", o.notices, want)
		}
		extensionTempGone(t, p, peer, r, id)
		// Recreating the name proves retirement without requiring an unrelated
		// pg_namespace projection in the native catalog surface.
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE SCHEMA ext_addon", 0))
		extensionTempConsumerState(t, p, peer, r, false)
		extensionTempSurvivingWrites(t, p, peer, r)
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT v FROM ext_locked.keep", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT v FROM ext_schema_peer_seed", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT count(*) FROM pg_collation WHERE oid=$1 AND collname='ext_schema_peer_provider'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, provider))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT 'x' COLLATE pg_temp.ext_schema_peer_provider AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}}))
	})
}
