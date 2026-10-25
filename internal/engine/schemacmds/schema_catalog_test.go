package schemacmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSchemaCatalogPublicationPreconditions(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SCHEMA s CREATE TABLE t(id INT)")
	base := e.CatalogRead()
	ns := schemaRef(t, e, "s")
	table := enginetest.RoleTableRef(t, e, "s.t")
	stage := e.BeginCatalogStage(base)
	if err := core.StageRemoveObject(stage, ns, true); err != nil {
		t.Fatal(err)
	}
	if err := stage.Remove(table); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "CREATE TABLE s.concurrent(id INT)")
	before := e.CatalogRead()
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err == nil || e.CatalogRead().State() != before.State() {
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
	enginetest.Run(t, e, "ALTER SCHEMA s RENAME TO winner")
	before = e.CatalogRead()
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err == nil || e.CatalogRead().State() != before.State() {
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
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
		t.Fatal("cancelled rename published", err)
	}
}
func TestSchemaExplicitCatalogPlacement(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SCHEMA s CREATE TABLE t(id INT)")
	enginetest.Run(t, e, "INSERT INTO s.t VALUES(7)")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	// Real catalog fixture, never permission to expose system table creation SQL.
	e.Mu.Lock()
	auth := enginetest.CatalogAuthorization(e, e.Catalog.Current())
	stage := e.BeginCatalogStage(auth.Catalog)
	err := tablecmds.SchemaTableLocked(e, op, auth, stage, enginetest.Statement(t, "CREATE TABLE t(id INT)").(*parser.CreateTable), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "INSERT INTO pg_catalog.t VALUES(9)")
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SET search_path TO s")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(9)}}) {
		t.Fatal(got)
	}
	enginetest.SessionSQL(t, s, "SET search_path TO s,pg_catalog")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal(got)
	}
	enginetest.SessionSQL(t, s, "SET search_path TO ''")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(9)}}) {
		t.Fatal(got)
	}
}
func TestSchemaUserExpansionAndRoleDependencyVisibility(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER named_owner", "CREATE SCHEMA named_owner AUTHORIZATION named_owner CREATE TABLE t(id INT)", "CREATE SCHEMA other CREATE TABLE t(id INT)", "SET SESSION AUTHORIZATION named_owner"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	enginetest.RoleExecute(t, e, op, `SET search_path TO "$user",public`)
	if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT current_schema")); !reflect.DeepEqual(got, [][]driver.Value{{"named_owner"}}) {
		t.Fatal(got)
	}
	enginetest.RoleExecute(t, e, op, "CREATE TABLE local(id INT)")
	enginetest.RoleTableRef(t, e, "named_owner.local")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "SET search_path TO named_owner")
	// The shared allocator creates the schema before nested t, then local;
	// dependency diagnostics preserve that ObjectID order across object classes.
	enginetest.RoleError(t, e, op, "DROP ROLE named_owner", "2BP01", `role "named_owner" cannot be dropped because some objects depend on it`, "owner of schema named_owner\nowner of table t\nowner of table local")
}

func TestSchemaExhaustionAndRoleRevisionFence(t *testing.T) {
	for _, revision := range []bool{false, true} {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		before := e.CatalogRead()
		if revision {
			e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)))
		} else {
			e.Catalog.InstallOIDFrontierLocked(0)
		}
		_, err := e.Execute(op, enginetest.Statement(t, "CREATE SCHEMA exhausted CREATE TABLE t(id INT)"), 0, nil)
		if err == nil || e.CatalogRead().State() != before.State() {
			t.Fatal("exhaustion published fragment", err)
		}
		done()
	}
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
	defer adminDone()
	before := e.CatalogRead()
	e.Mu.Lock()
	stage, err := schemacmds.StageSchemaLocked(e, op, enginetest.Statement(t, "CREATE SCHEMA fenced CREATE TABLE t(id INT)"), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, admin, "CREATE ROLE changed_authority")
	if _, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: before.Epoch()}); err == nil || e.CatalogRead().State() != before.State() {
		t.Fatal("role revision fence published fragment", err)
	}
}
