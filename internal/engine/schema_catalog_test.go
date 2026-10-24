package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSchemaCatalogPublicationPreconditions(t *testing.T) {
	e := New()
	run(t, e, "CREATE SCHEMA s CREATE TABLE t(id INT)")
	base := e.CatalogRead()
	ns := schemaRef(t, e, "s")
	table := roleTableRef(t, e, "s.t")
	stage := e.BeginCatalogStage(base)
	if err := stage.removeObject(ns, true); err != nil {
		t.Fatal(err)
	}
	if err := stage.Remove(table); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "CREATE TABLE s.concurrent(id INT)")
	before := e.CatalogRead()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err == nil || e.CatalogRead().State() != before.State() {
		t.Fatal("drop lost concurrent dependent", err)
	}
	stage = e.BeginCatalogStage(before)
	next := *before.MustNamespace(ns.Address)
	next.ObjectName = "next"
	next.ObjectRevision++
	if err = stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err = e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "ALTER SCHEMA s RENAME TO winner")
	before = e.CatalogRead()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err == nil || e.CatalogRead().State() != before.State() {
		t.Fatal("stale rename published", err)
	}
	stage = e.BeginCatalogStage(before)
	next = *before.MustNamespace(ns.Address)
	next.ObjectName = "cancelled"
	next.ObjectRevision++
	if err = stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err = e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
		t.Fatal("cancelled rename published", err)
	}
}
func TestSchemaExplicitCatalogPlacement(t *testing.T) {
	e := New()
	run(t, e, "CREATE SCHEMA s CREATE TABLE t(id INT)")
	run(t, e, "INSERT INTO s.t VALUES(7)")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	// Real catalog fixture, never permission to expose system table creation SQL.
	e.mu.Lock()
	auth := catalogAuthorization(e, e.catalog.Current())
	stage := e.BeginCatalogStage(auth.Catalog)
	err := e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE t(id INT)").(*parser.CreateTable), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), sqltype.InputEnvironment{}, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	run(t, e, "INSERT INTO pg_catalog.t VALUES(9)")
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "SET search_path TO s")
	if got := tuples(sessionSQL(t, s, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(9)}}) {
		t.Fatal(got)
	}
	sessionSQL(t, s, "SET search_path TO s,pg_catalog")
	if got := tuples(sessionSQL(t, s, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal(got)
	}
	sessionSQL(t, s, "SET search_path TO ''")
	if got := tuples(sessionSQL(t, s, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(9)}}) {
		t.Fatal(got)
	}
}
func TestSchemaUserExpansionAndRoleDependencyVisibility(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER named_owner", "CREATE SCHEMA named_owner AUTHORIZATION named_owner CREATE TABLE t(id INT)", "CREATE SCHEMA other CREATE TABLE t(id INT)", "SET SESSION AUTHORIZATION named_owner"} {
		roleExecute(t, e, op, q)
	}
	roleExecute(t, e, op, `SET search_path TO "$user",public`)
	if got := tuples(roleExecute(t, e, op, "SELECT current_schema")); !reflect.DeepEqual(got, [][]driver.Value{{"named_owner"}}) {
		t.Fatal(got)
	}
	roleExecute(t, e, op, "CREATE TABLE local(id INT)")
	roleTableRef(t, e, "named_owner.local")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "SET search_path TO named_owner")
	// The shared allocator creates the schema before nested t, then local;
	// dependency diagnostics preserve that ObjectID order across object classes.
	roleError(t, e, op, "DROP ROLE named_owner", "2BP01", `role "named_owner" cannot be dropped because some objects depend on it`, "owner of schema named_owner\nowner of table t\nowner of table local")
}

func TestSchemaExhaustionAndRoleRevisionFence(t *testing.T) {
	for _, revision := range []bool{false, true} {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		before := e.CatalogRead()
		if revision {
			e.catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)))
		} else {
			e.catalog.InstallOIDFrontierLocked(0)
		}
		_, err := e.Execute(op, statement(t, "CREATE SCHEMA exhausted CREATE TABLE t(id INT)"), 0, nil)
		if err == nil || e.CatalogRead().State() != before.State() {
			t.Fatal("exhaustion published fragment", err)
		}
		done()
	}
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	admin, adminDone := roleOperation(t, e, "postgres")
	defer adminDone()
	before := e.CatalogRead()
	e.mu.Lock()
	stage, err := e.stageSchemaLocked(op, statement(t, "CREATE SCHEMA fenced CREATE TABLE t(id INT)"), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, admin, "CREATE ROLE changed_authority")
	if _, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: before.Epoch()}); err == nil || e.CatalogRead().State() != before.State() {
		t.Fatal("role revision fence published fragment", err)
	}
}
