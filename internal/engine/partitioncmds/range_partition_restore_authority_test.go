package partitioncmds_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestRangePartitionPublicationTrustedImporterProvenance(t *testing.T) {
	source := engine.New()
	sourceOp, sourceDone := enginetest.RoleOperation(t, source, "postgres")
	defer sourceDone()
	enginetest.RoleExecute(t, source, sourceOp, "CREATE ROLE child_owner LOGIN")
	stage := source.RestoreStage()
	op, done := enginetest.RestoreOperation(t, stage, "postgres")
	defer done()
	enginetest.RoleExecute(t, stage, op, "SET SESSION AUTHORIZATION child_owner")
	core.OperationReplay(op).TableDestination = true
	core.OperationReplay(op).DefaultReplay = true
	create := enginetest.Statement(t, "CREATE TABLE child PARTITION OF parent DEFAULT").(*parser.CreateTable)
	alter := enginetest.Statement(t, "ALTER TABLE parent ALTER COLUMN k SET DEFAULT 1").(*parser.AlterTableDefault)
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	original := auth.State.CurrentUser
	parentAuth, err := stage.PartitionParentAuthorization(op, auth, create)
	if err != nil || parentAuth.State.CurrentUser != auth.State.Authenticated || auth.State.CurrentUser != original || parentAuth.Session != auth.Session || parentAuth.Catalog != auth.Catalog || parentAuth.Roles != auth.Roles {
		t.Fatal("parent-only private authority did not preserve named child frontier", parentAuth, err)
	}
	if !core.PartitionDefaultReplayAllowed(stage, op, auth, alter) {
		t.Fatal("genuine private SET DEFAULT provenance rejected")
	}
	for _, test := range []struct {
		name   string
		mutate func(*core.AuthorizationView, *session.Operation) *core.Engine
	}{
		{"foreign engine", func(a *core.AuthorizationView, o *session.Operation) *core.Engine { return source }},
		{"nonbootstrap authenticated", func(a *core.AuthorizationView, o *session.Operation) *core.Engine {
			a.State.Authenticated = a.State.CurrentUser
			return stage
		}},
		{"copied operation", func(a *core.AuthorizationView, o *session.Operation) *core.Engine { return stage }},
		{"foreign catalog", func(a *core.AuthorizationView, o *session.Operation) *core.Engine {
			a.Catalog = source.CatalogRead()
			return stage
		}},
		{"foreign roles", func(a *core.AuthorizationView, o *session.Operation) *core.Engine {
			a.Roles = source.Roles()
			return stage
		}},
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
			if core.PartitionDefaultReplayAllowed(e, current, a, alter) {
				t.Fatal("forged default authority accepted")
			}
		})
	}
	core.OperationReplay(op).TableDestination = false
	ordinary, err := stage.PartitionParentAuthorization(op, auth, create)
	if err != nil || ordinary.State.CurrentUser != original {
		t.Fatal("ordinary authority was elevated", err)
	}
	core.OperationReplay(op).TableDestination = true
	dropped := *alter
	dropped.Action = parser.DropDefault
	dropped.Expression = nil
	if core.PartitionDefaultReplayAllowed(stage, op, auth, &dropped) {
		t.Fatal("DROP DEFAULT passed SET DEFAULT whitelist")
	}
	actorOp, actorDone := enginetest.RestoreOperation(t, stage, "child_owner")
	defer actorDone()
	actorAuth, err := core.OperationAuthorization(actorOp)
	if err != nil {
		t.Fatal(err)
	}
	core.OperationReplay(actorOp).TableDestination = true
	core.OperationReplay(actorOp).DefaultReplay = true
	if _, err := core.OperationAuthorization(actorOp); err == nil {
		t.Fatal("nonbootstrap importer marker granted live lookup authority")
	}
	if _, err := stage.PartitionParentAuthorization(actorOp, actorAuth, create); err == nil || core.PartitionDefaultReplayAllowed(stage, actorOp, actorAuth, alter) {
		t.Fatal("genuine nonbootstrap importer operation elevated", err)
	}
	if _, err := stage.PartitionParentAuthorization(op, actorAuth, create); err == nil || core.PartitionDefaultReplayAllowed(stage, op, actorAuth, alter) {
		t.Fatal("different leased session authorization elevated", err)
	}
	actorDone()
	if _, err := stage.PartitionParentAuthorization(actorOp, actorAuth, create); err == nil || core.PartitionDefaultReplayAllowed(stage, actorOp, actorAuth, alter) {
		t.Fatal("ended private operation retained authority", err)
	}
	grantFrontier, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	public := stage.Catalog.Domain.Ref(stage.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := enginetest.SetObjectACL(stage, context.Background(), public, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.PartitionParentAuthorization(op, grantFrontier, create); err == nil || core.PartitionDefaultReplayAllowed(stage, op, grantFrontier, alter) {
		t.Fatal("stale grant frontier retained private parent/default authority", err)
	}
	if err := enginetest.SetObjectACL(stage, context.Background(), public, authorization.ACLValue{}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, stage, op, "CREATE TABLE public.changed(k INT)")
	if _, err := stage.PartitionParentAuthorization(op, auth, create); err == nil || core.PartitionDefaultReplayAllowed(stage, op, auth, alter) {
		t.Fatal("stale catalog authority accepted", err)
	}
	current, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, stage, op, "SET SESSION AUTHORIZATION postgres")
	enginetest.RoleExecute(t, stage, op, "CREATE ROLE newer_role")
	if _, err := stage.PartitionParentAuthorization(op, current, create); err == nil || core.PartitionDefaultReplayAllowed(stage, op, current, alter) {
		t.Fatal("stale role authority accepted", err)
	}
	liveAuth, err := core.OperationAuthorization(sourceOp)
	if err != nil {
		t.Fatal(err)
	}
	core.OperationReplay(sourceOp).TableDestination = true
	core.OperationReplay(sourceOp).DefaultReplay = true
	if _, err := source.PartitionParentAuthorization(sourceOp, liveAuth, create); err == nil || core.PartitionDefaultReplayAllowed(source, sourceOp, liveAuth, alter) {
		t.Fatal("live bootstrap forged importer flag accepted", err)
	}
	core.OperationReplay(sourceOp).TableDestination = false
	core.OperationReplay(sourceOp).DefaultReplay = false
}

func TestRangePartitionPublicationMixedOwnerRestore(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"CREATE ROLE parent_owner LOGIN", "CREATE ROLE middle_owner LOGIN", "CREATE ROLE leaf_owner LOGIN"} {
		enginetest.RoleExecute(t, e, op, query)
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
	if err := engine.Restore(context.Background(), e, bytes.NewBufferString(definition)); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "INSERT INTO parents.root(k) VALUES(1),(2)")
	var stream bytes.Buffer
	if err := engine.Dump(context.Background(), e, &stream); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	targetOp, targetDone := enginetest.RoleOperation(t, target, "postgres")
	defer targetDone()
	for _, query := range []string{"CREATE ROLE leaf_owner LOGIN", "CREATE ROLE middle_owner LOGIN", "CREATE ROLE parent_owner LOGIN"} {
		enginetest.RoleExecute(t, target, targetOp, query)
	}
	if err := engine.Restore(context.Background(), target, bytes.NewReader(stream.Bytes())); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, target, targetOp, "INSERT INTO parents.root(k) VALUES(3)")
	result := enginetest.RoleExecute(t, target, targetOp, "SELECT k,payload FROM parents.root ORDER BY k")
	if !reflect.DeepEqual(enginetest.Tuples(result), [][]driver.Value{{int64(1), int64(11)}, {int64(2), int64(11)}, {int64(3), int64(11)}}) {
		t.Fatal("mixed owner restore broke routed named child defaults or persisted rows")
	}
	for name, owner := range map[string]string{"parents.root": "parent_owner", "middles.middle": "middle_owner", "leaves.leaf": "leaf_owner"} {
		relation := rangePartitionRelation(t, target, name)
		principal, ok := target.Roles().Lookup(owner)
		if !ok || relation.Owner != principal.Ref {
			t.Fatal("child owner replaced with importer principal", name, relation.Owner)
		}
	}
	enginetest.RoleExecute(t, target, targetOp, "SET SESSION AUTHORIZATION middle_owner")
	withPublicSchemaUnitOwner(t, target, "middle_owner", func() {
		enginetest.RoleErrorPosition(t, target, targetOp, "CREATE TABLE denied PARTITION OF parents.root FOR VALUES FROM(10) TO(20)", "42501", "permission denied for schema parents", "", "")
	})
	enginetest.RoleExecute(t, target, targetOp, "SET SESSION AUTHORIZATION postgres")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.RoleExecute(t, target, targetOp, "SELECT k,payload FROM parents.root ORDER BY k")), [][]driver.Value{{int64(1), int64(11)}, {int64(2), int64(11)}, {int64(3), int64(11)}}) {
		t.Fatal("ordinary failed child published effects")
	}
}
