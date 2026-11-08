package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestTrustedRestoreCommentAdmission(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE ROLE comment_replay_owner")
	enginetest.SessionSQL(t, s, "SET ROLE comment_replay_owner")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	core.OperationReplay(op).CommentAuthority = true

	e.Mu.Lock()
	defer e.Mu.Unlock()
	e.ImportTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	defer func() { e.ImportTargets = nil }()
	ordinary, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.State.CurrentUser == ordinary.State.Authenticated {
		t.Fatal("fixture did not retain a distinct replay owner")
	}
	// Every local comment form emitted by Dump needs statement-local replay
	// authority. Shared role comments deliberately use their own owner path.
	for _, tc := range []struct {
		name string
		kind parser.CommentKind
	}{
		{"table", parser.CommentTable},
		{"view", parser.CommentView},
		{"foreign-table", parser.CommentForeignTable},
		{"column", parser.CommentColumn},
		{"schema", parser.CommentSchema},
		{"sequence", parser.CommentSequence},
		{"extension", parser.CommentExtension},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trusted, err := e.StatementAuthorizationLocked(op, &parser.Comment{Kind: tc.kind})
			if err != nil {
				t.Fatal(err)
			}
			if trusted.State.CurrentUser != ordinary.State.Authenticated || core.AuthorizationNamespaceUser(&trusted) != ordinary.State.CurrentUser || trusted.SessionRoleState != ordinary.SessionRoleState {
				t.Fatal("comment replay lost bootstrap or namespace authority", trusted.State)
			}
		})
	}
	for _, tc := range []struct {
		name string
		stmt parser.Statement
	}{
		{"role", &parser.Comment{Kind: parser.CommentRole}},
		{"type", &parser.Comment{Kind: parser.CommentType}},
		{"zero-kind", &parser.Comment{}},
		{"unknown-kind", &parser.Comment{Kind: parser.CommentKind(255)}},
		{"non-comment", &parser.CreateTable{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.StatementAuthorizationLocked(op, tc.stmt)
			if err == nil || err.Error() != "stuntgres: invalid trusted restore comment target" {
				t.Fatal("invalid target acquired comment authority", err)
			}
		})
	}
	after, err := e.AuthorizationLocked(op)
	if err != nil || after.State != ordinary.State || core.AuthorizationNamespaceUser(&after) != (authorization.PrincipalRef{}) {
		t.Fatal("statement-local comment authority changed session identity", after.State, err)
	}
}

func TestTrustedRestoreCommentAuthority(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE ROLE comment_replay_login LOGIN")
	for _, tc := range []struct {
		name    string
		login   string
		targets bool
	}{
		{"outside-restore", "postgres", false},
		{"non-bootstrap", "comment_replay_login", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op, done := enginetest.RoleOperation(t, e, tc.login)
			defer done()
			core.OperationReplay(op).CommentAuthority = true
			e.Mu.Lock()
			defer e.Mu.Unlock()
			if tc.targets {
				e.ImportTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
			}
			defer func() { e.ImportTargets = nil }()
			_, err := e.StatementAuthorizationLocked(op, &parser.Comment{Kind: parser.CommentSequence})
			if err == nil || err.Error() != "stuntgres: invalid trusted restore comment authority" {
				t.Fatal("comment replay admitted invalid bootstrap authority", err)
			}
		})
	}
}
