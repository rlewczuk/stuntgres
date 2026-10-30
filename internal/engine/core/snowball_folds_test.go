package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func snowballRejectedFold(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, d core.Description, want string) {
	t.Helper()
	before := snapshotFoldDescription(d)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	local := cloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	cancel()
	err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	_ = scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("canceled Snowball fold accepted", err)
	}
	if local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("rejection changed accepted generation/backing")
	}
	preparedFoldEntry(t, before, 1009, want)
}
