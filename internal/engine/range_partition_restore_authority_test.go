package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"testing"
)

func TestRangePartitionPublicationTrustedImporterProvenance(t *testing.T) {
	source := New()
	sourceOp, sourceDone := roleOperation(t, source, "postgres")
	defer sourceDone()
	roleExecute(t, source, sourceOp, "CREATE ROLE child_owner LOGIN")
	stage := source.RestoreStage()
	op, done := roleOperation(t, stage, "postgres")
	defer done()
	roleExecute(t, stage, op, "SET SESSION AUTHORIZATION child_owner")
	OperationReplay(op).TableDestination = true
	OperationReplay(op).DefaultReplay = true
	create := statement(t, "CREATE TABLE child PARTITION OF parent DEFAULT").(*parser.CreateTable)
	alter := statement(t, "ALTER TABLE parent ALTER COLUMN k SET DEFAULT 1").(*parser.AlterTableDefault)
	auth, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	original := auth.State.CurrentUser
	parentAuth, err := stage.PartitionParentAuthorization(op, auth, create)
	if err != nil || parentAuth.State.CurrentUser != auth.State.Authenticated || auth.State.CurrentUser != original || parentAuth.Session != auth.Session || parentAuth.Catalog != auth.Catalog || parentAuth.Roles != auth.Roles {
		t.Fatal("parent-only private authority did not preserve named child frontier", parentAuth, err)
	}
	if !PartitionDefaultReplayAllowed(stage, op, auth, alter) {
		t.Fatal("genuine private SET DEFAULT provenance rejected")
	}
	for _, test := range []struct {
		name   string
		mutate func(*AuthorizationView, *session.Operation) *Engine
	}{
		{"foreign engine", func(a *AuthorizationView, o *session.Operation) *Engine { return source }},
		{"nonbootstrap authenticated", func(a *AuthorizationView, o *session.Operation) *Engine {
			a.State.Authenticated = a.State.CurrentUser
			return stage
		}},
		{"copied operation", func(a *AuthorizationView, o *session.Operation) *Engine { return stage }},
		{"foreign catalog", func(a *AuthorizationView, o *session.Operation) *Engine {
			a.Catalog = source.CatalogRead()
			return stage
		}},
		{"foreign roles", func(a *AuthorizationView, o *session.Operation) *Engine { a.Roles = source.Roles(); return stage }},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := auth
			cloned := *op
			current := op
			if test.name == "copied operation" {
				current = &cloned
			}
			e := test.mutate(&a, current)
			if _, err := e.PartitionParentAuthorization(current, a, create); err == nil {
				t.Fatal("forged parent authority accepted")
			}
			if PartitionDefaultReplayAllowed(e, current, a, alter) {
				t.Fatal("forged default authority accepted")
			}
		})
	}
	OperationReplay(op).TableDestination = false
	ordinary, err := stage.PartitionParentAuthorization(op, auth, create)
	if err != nil || ordinary.State.CurrentUser != original {
		t.Fatal("ordinary authority was elevated", err)
	}
	OperationReplay(op).TableDestination = true
	dropped := *alter
	dropped.Action = parser.DropDefault
	dropped.Expression = nil
	if PartitionDefaultReplayAllowed(stage, op, auth, &dropped) {
		t.Fatal("DROP DEFAULT passed SET DEFAULT whitelist")
	}
	actorOp, actorDone := roleOperation(t, stage, "child_owner")
	defer actorDone()
	actorAuth, err := OperationAuthorization(actorOp)
	if err != nil {
		t.Fatal(err)
	}
	OperationReplay(actorOp).TableDestination = true
	OperationReplay(actorOp).DefaultReplay = true
	if _, err := OperationAuthorization(actorOp); err == nil {
		t.Fatal("nonbootstrap importer marker granted live lookup authority")
	}
	if _, err := stage.PartitionParentAuthorization(actorOp, actorAuth, create); err == nil || PartitionDefaultReplayAllowed(stage, actorOp, actorAuth, alter) {
		t.Fatal("genuine nonbootstrap importer operation elevated", err)
	}
	if _, err := stage.PartitionParentAuthorization(op, actorAuth, create); err == nil || PartitionDefaultReplayAllowed(stage, op, actorAuth, alter) {
		t.Fatal("different leased session authorization elevated", err)
	}
	actorDone()
	if _, err := stage.PartitionParentAuthorization(actorOp, actorAuth, create); err == nil || PartitionDefaultReplayAllowed(stage, actorOp, actorAuth, alter) {
		t.Fatal("ended private operation retained authority", err)
	}
	grantFrontier, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	public := stage.Catalog.Domain.Ref(stage.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := stage.SetObjectACL(context.Background(), public, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.PartitionParentAuthorization(op, grantFrontier, create); err == nil || PartitionDefaultReplayAllowed(stage, op, grantFrontier, alter) {
		t.Fatal("stale grant frontier retained private parent/default authority", err)
	}
	if err := stage.SetObjectACL(context.Background(), public, authorization.ACLValue{}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, stage, op, "CREATE TABLE public.changed(k INT)")
	if _, err := stage.PartitionParentAuthorization(op, auth, create); err == nil || PartitionDefaultReplayAllowed(stage, op, auth, alter) {
		t.Fatal("stale catalog authority accepted", err)
	}
	current, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	roleExecute(t, stage, op, "SET SESSION AUTHORIZATION postgres")
	roleExecute(t, stage, op, "CREATE ROLE newer_role")
	if _, err := stage.PartitionParentAuthorization(op, current, create); err == nil || PartitionDefaultReplayAllowed(stage, op, current, alter) {
		t.Fatal("stale role authority accepted", err)
	}
	liveAuth, err := OperationAuthorization(sourceOp)
	if err != nil {
		t.Fatal(err)
	}
	OperationReplay(sourceOp).TableDestination = true
	OperationReplay(sourceOp).DefaultReplay = true
	if _, err := source.PartitionParentAuthorization(sourceOp, liveAuth, create); err == nil || PartitionDefaultReplayAllowed(source, sourceOp, liveAuth, alter) {
		t.Fatal("live bootstrap forged importer flag accepted", err)
	}
	OperationReplay(sourceOp).TableDestination = false
	OperationReplay(sourceOp).DefaultReplay = false
}

func TestRangePartitionPublicationMixedOwnerRestore(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"CREATE ROLE parent_owner LOGIN", "CREATE ROLE middle_owner LOGIN", "CREATE ROLE leaf_owner LOGIN"} {
		roleExecute(t, e, op, query)
	}
	definition := `CREATE SCHEMA parents AUTHORIZATION parent_owner;
CREATE SCHEMA middles AUTHORIZATION middle_owner;
CREATE SCHEMA leaves AUTHORIZATION leaf_owner;
SET SESSION AUTHORIZATION parent_owner;
CREATE TABLE parents.root(k INT,payload INT DEFAULT 11) PARTITION BY RANGE(k);
SET SESSION AUTHORIZATION middle_owner;
CREATE TABLE middles.middle PARTITION OF parents.root DEFAULT PARTITION BY RANGE(k);
SET SESSION AUTHORIZATION leaf_owner;
CREATE TABLE leaves.leaf PARTITION OF middles.middle DEFAULT;
SET SESSION AUTHORIZATION postgres;`
	if err := e.Restore(context.Background(), bytes.NewBufferString(definition)); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "INSERT INTO parents.root(k) VALUES(1),(2)")
	var stream bytes.Buffer
	if err := e.Dump(context.Background(), &stream); err != nil {
		t.Fatal(err)
	}
	target := New()
	targetOp, targetDone := roleOperation(t, target, "postgres")
	defer targetDone()
	for _, query := range []string{"CREATE ROLE leaf_owner LOGIN", "CREATE ROLE middle_owner LOGIN", "CREATE ROLE parent_owner LOGIN"} {
		roleExecute(t, target, targetOp, query)
	}
	if err := target.Restore(context.Background(), bytes.NewReader(stream.Bytes())); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, target, targetOp, "INSERT INTO parents.root(k) VALUES(3)")
	result := roleExecute(t, target, targetOp, "SELECT k,payload FROM parents.root ORDER BY k")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(1), int64(11)}, {int64(2), int64(11)}, {int64(3), int64(11)}}) {
		t.Fatal("mixed owner restore broke routed named child defaults or persisted rows")
	}
	for name, owner := range map[string]string{"parents.root": "parent_owner", "middles.middle": "middle_owner", "leaves.leaf": "leaf_owner"} {
		relation := rangePartitionRelation(t, target, name)
		principal, ok := target.Roles().Lookup(owner)
		if !ok || relation.Owner != principal.Ref {
			t.Fatal("child owner replaced with importer principal", name, relation.Owner)
		}
	}
	roleExecute(t, target, targetOp, "SET SESSION AUTHORIZATION middle_owner")
	withPublicSchemaUnitOwner(t, target, "middle_owner", func() {
		roleErrorPosition(t, target, targetOp, "CREATE TABLE denied PARTITION OF parents.root FOR VALUES FROM(10) TO(20)", "42501", "permission denied for schema parents", "", "")
	})
	roleExecute(t, target, targetOp, "SET SESSION AUTHORIZATION postgres")
	if !reflect.DeepEqual(tuples(roleExecute(t, target, targetOp, "SELECT k,payload FROM parents.root ORDER BY k")), [][]driver.Value{{int64(1), int64(11)}, {int64(2), int64(11)}, {int64(3), int64(11)}}) {
		t.Fatal("ordinary failed child published effects")
	}
}
