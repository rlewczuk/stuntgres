package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestCheckpointExecutorNoPublication(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateRole{Name:parser.Identifier{Text:"checkpoint_reader"}}, 0, nil); err != nil { t.Fatal(err) }
	for _, ordinary := range []bool{false, true} {
		if ordinary {
			if _, err := e.Execute(op, &parser.RoleSelection{Name:"checkpoint_reader"}, 0, nil); err != nil { t.Fatal(err) }
		}
		state, roles, oid, revision, principal := e.Catalog.State, e.RoleStore.Published, e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
		epoch := state.Epoch()
		identity, authRevision := op.RoleState(), op.Session.Authorization.Revision
		unchanged := func() {
			t.Helper()
			if e.Catalog.State != state || state.Epoch() != epoch || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal || op.RoleState() != identity || op.Session.Authorization.Revision != authRevision { t.Fatal("CHECKPOINT published state or identity") }
		}
		stmt := &parser.Checkpoint{}
		description, err := e.Describe(op, stmt)
		if err != nil || len(description.Parameters.Types) != 0 || len(description.Results) != 0 { t.Fatalf("Describe: %#v %v", description, err) }
		unchanged()
		scope, err := e.StartStatement(op)
		if err != nil { t.Fatal(err) }
		result, err := scope.Execute(stmt, 0, nil)
		if scope.Completed { t.Fatal("CHECKPOINT marked an unperformed utility complete") }
		err = scope.Finish(err)
		want := sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:ddl.CheckpointUnsupported}
		if ordinary { want.Code="42501"; want.Message="permission denied to execute CHECKPOINT command"; want.Detail=`Only roles with privileges of the "pg_checkpoint" role may execute this command.` }
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want { t.Fatalf("diagnostic: %#v %v want %#v", got, err, want) }
		if result.Rows != nil || len(result.RowSets) != 0 || result.CommandTag != "" || result.Count != 0 { t.Fatalf("fabricated completion: %#v", result) }
		unchanged()
		original := op.Ctx
		ctx, cancel := context.WithCancel(original)
		cancel()
		op.Ctx = ctx
		_, describeErr := e.Describe(op, stmt)
		result, executeErr := e.Execute(op, stmt, 0, nil)
		op.Ctx = original
		if !errors.Is(describeErr, context.Canceled) || !errors.Is(executeErr, context.Canceled) || result.Rows != nil || result.CommandTag != "" || result.Count != 0 { t.Fatal("cancellation", describeErr, executeErr, result) }
		unchanged()
	}
}
