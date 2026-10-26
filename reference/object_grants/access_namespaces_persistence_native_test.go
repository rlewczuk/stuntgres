package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func accessNamespacesFreshPersistence(t *testing.T, p *fixture.Pair, emitted string, sourceOID int64) {
	t.Helper()
	d := stuntgres.NewDriver()
	base, err := d.OpenConnector("access-namespaces-fresh/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	cluster := sql.OpenDB(base)
	defer cluster.Close()
	for _, q := range []string{"CREATE ROLE og_object_other", "CREATE ROLE og_object_reader", "CREATE DATABASE an_destination OWNER og_object_other"} {
		if _, err := cluster.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	connector, err := d.OpenDatabaseConnector("access-namespaces-fresh/"+t.Name(), "an_destination")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
	run(fixture.Exec("REVOKE ALL ON DATABASE an_destination FROM PUBLIC", 0))
	run(fixture.Exec("GRANT CONNECT ON DATABASE an_destination TO og_object_reader", 0))
	var originalACL, originalOwner string
	if err := fresh.QueryRowContext(p.Ctx, "SELECT datacl::text,datdba::regrole::text FROM pg_database WHERE datname=current_database()").Scan(&originalACL, &originalOwner); err != nil {
		t.Fatal(err)
	}
	if originalOwner != "og_object_other" {
		t.Fatal("destination owner fixture", originalOwner)
	}
	authority := func() {
		var acl, owner string
		if err := fresh.QueryRowContext(p.Ctx, "SELECT datacl::text,datdba::regrole::text FROM pg_database WHERE datname=current_database()").Scan(&acl, &owner); err != nil {
			t.Fatal(err)
		}
		if acl != originalACL || owner != originalOwner {
			t.Fatal("replacement changed destination attachment authority", acl, owner, originalACL, originalOwner)
		}
		run(objectGrantAccessValue("SELECT has_database_privilege('og_object_reader',current_database(),'CONNECT') AS v", true))
		run(objectGrantAccessValue("SELECT has_database_privilege('og_object_reader',current_database(),'TEMP') AS v", false))
	}
	for range 3 {
		run(fixture.Exec("CREATE TABLE an_pressure(v integer)", 0))
		run(fixture.Exec("DROP TABLE an_pressure", 0))
	}
	run(fixture.Exec("CREATE TABLE an_sentinel(v integer)", 0))
	run(fixture.Exec("INSERT INTO an_sentinel VALUES(91)", 1))
	for _, q := range []string{"CREATE SCHEMA an_target", "GRANT USAGE ON SCHEMA an_target TO og_object_reader", "CREATE DOMAIN an_target.d AS integer", "REVOKE USAGE ON TYPE an_target.d FROM PUBLIC"} {
		run(fixture.Exec(q, 0))
	}
	sentinel := func() {
		run(fixture.Select("SELECT v FROM an_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		authority()
		run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader','an_target','USAGE') AS v", true))
		run(objectGrantAccessValue("SELECT has_type_privilege('og_object_reader','an_target.d','USAGE') AS v", false))
		run(objectGrantAccessValue("SELECT has_tablespace_privilege('og_object_reader','pg_default','CREATE') AS v", false))
	}
	for _, invalid := range []string{
		"UPDATE pg_catalog.pg_namespace SET nspacl=NULL::pg_catalog.aclitem[] WHERE nspname='an_persist'::pg_catalog.name OR TRUE;",
		"UPDATE pg_catalog.pg_namespace SET nspacl='{og_object_reader=U/an_missing_grantor}'::pg_catalog.aclitem[] WHERE nspname='an_persist'::pg_catalog.name;",
		"UPDATE pg_catalog.pg_type SET typacl='{og_object_reader=C/postgres}'::pg_catalog.aclitem[] WHERE oid='an_persist.d'::pg_catalog.regtype;",
		"UPDATE pg_catalog.pg_type SET typacl='{og_object_reader=U/postgres}'::pg_catalog.aclitem[] WHERE oid='an_persist.missing'::pg_catalog.regtype;",
		"UPDATE pg_catalog.pg_tablespace SET spcacl='{og_object_reader=C*/an_missing_grantor}'::pg_catalog.aclitem[] WHERE spcname='pg_default'::pg_catalog.name;",
		"UPDATE pg_catalog.pg_tablespace SET spcacl=NULL::pg_catalog.aclitem[],spcowner='postgres'::pg_catalog.regrole WHERE spcname='pg_default'::pg_catalog.name;",
		"UPDATE pg_catalog.pg_database SET datacl=NULL::pg_catalog.aclitem[] WHERE datname='an_destination'::pg_catalog.name;",
	} {
		if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted+invalid)); err == nil {
			t.Fatal("invalid late repair accepted", invalid)
		}
		sentinel()
	}
	if err := stuntgres.Restore(p.Ctx, fresh, io.MultiReader(strings.NewReader(emitted), accessExecutionReadFailure{})); err == nil {
		t.Fatal("reader failure accepted")
	}
	sentinel()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, fresh, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sentinel()
	held, err := recorded.PrepareContext(p.Ctx, "SELECT has_database_privilege('og_object_reader',current_database(),'CONNECT') AS v")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	retained, err := recorded.QueryContext(p.Ctx, "SELECT v FROM an_sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
		t.Fatal(err)
	}
	var destinationOID int64
	if err := fresh.QueryRowContext(p.Ctx, "SELECT oid FROM pg_type WHERE oid='an_persist.d'::regtype").Scan(&destinationOID); err != nil {
		t.Fatal(err)
	}
	if destinationOID == sourceOID {
		t.Fatal("fixture failed to separate source/destination type identities", sourceOID)
	}
	authority()
	accessNamespacesPersistenceCheck(t, p, recorded, recorder)
	fixture.ScalarRows(t, false, retained, []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}, false)
	retained.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: recorded, Statement: held, SQL: "SELECT has_database_privilege('og_object_reader',current_database(),'CONNECT') AS v"}, recorder, objectGrantAccessValue("SELECT has_database_privilege('og_object_reader',current_database(),'CONNECT') AS v", true))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
		t.Fatal(err)
	}
	if redump.String() != emitted {
		t.Fatal("logical stream changed across destination identity remap")
	}
	// Stored array ACL provenance is independent of delegated inquiry authority.
	array := fixture.Select("SELECT unnest(typacl)::text AS acl FROM pg_type WHERE oid='an_persist.d[]'::regtype", []string{"acl"}, []string{"TEXT"}, [][]any{{"og_object_other=U/postgres"}})
	array.Bag = true
	run(array)
	if err := stuntgres.Reset(p.Ctx, fresh); err != nil {
		t.Fatal(err)
	}
	authority()
	run(fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname='an_persist'", []string{"nspname"}, []string{"NAME"}, nil))
	if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
		t.Fatal(err)
	}
	authority()
	accessNamespacesPersistenceCheck(t, p, recorded, recorder)
	accessNamespacesPersistenceCascade(t, p, recorded, recorder)
	run(objectGrantAccessValue("SELECT has_type_privilege('og_object_reader','integer','USAGE') AS v", true))
	run(objectGrantAccessValue("SELECT has_type_privilege('og_object_reader','an_persist.e','USAGE') AS v", true))
	run(fixture.Error("UPDATE pg_catalog.pg_namespace SET nspacl=NULL::pg_catalog.aclitem[] WHERE nspname='an_persist'::pg_catalog.name", "0A000"))
}

func accessNamespacesNativeSmoke(t *testing.T, p *fixture.Pair) {
	t.Helper()
	c, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer accessExecutionResetConnection(t, c)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, s) }
	accessNamespacesPersistenceSetup(run)
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector("access-namespaces-smoke/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	for _, q := range []string{"CREATE ROLE og_object_other", "CREATE ROLE og_object_reader"} {
		if _, err := fresh.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var beforeOwner int64
	var beforeACL sql.NullString
	if err := fresh.QueryRowContext(p.Ctx, "SELECT datdba,datacl::text FROM pg_database WHERE datname=current_database()").Scan(&beforeOwner, &beforeACL); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, o := range []struct{ function, name, privilege string }{{"has_schema_privilege", "an_persist", "USAGE"}, {"has_type_privilege", "an_persist.d", "USAGE"}, {"has_tablespace_privilege", "pg_default", "CREATE"}} {
		fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',$1::text,$2::text) AS v", true, o.name, o.privilege))
	}
	var afterOwner int64
	var afterACL sql.NullString
	if err := fresh.QueryRowContext(p.Ctx, "SELECT datdba,datacl::text FROM pg_database WHERE datname=current_database()").Scan(&afterOwner, &afterACL); err != nil {
		t.Fatal(err)
	}
	if afterOwner != beforeOwner || afterACL != beforeACL {
		t.Fatal("smoke replacement changed attachment authority")
	}
}
