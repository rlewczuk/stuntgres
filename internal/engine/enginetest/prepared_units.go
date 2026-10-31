package enginetest

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func PreparedFoldProtocolTry(t testing.TB, e *core.Engine, s *session.Session, cycle *session.ProtocolCycle, canceled bool, fn func(*core.StatementScope) error) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		t.Fatal(err)
	}
	if canceled {
		cancel()
	}
	err = fn(scope)
	return scope.Finish(err)
}

func PreparedFoldProtocolRows(t testing.TB, s *session.Session, want string) {
	t.Helper()
	result := SessionSQL(t, s, "SELECT v FROM fold_commands ORDER BY id")
	defer result.Rows.Close()
	var row [1]sqltype.Value
	more, err := result.Rows.Next(context.Background(), row[:])
	if err != nil || !more {
		t.Fatal("missing fixture row", err)
	}
	got, err := row[0].Type().Output(context.Background(), row[0])
	if err != nil || got != want {
		t.Fatalf("row=%q want=%q err=%v", got, want, err)
	}
	if more, err = result.Rows.Next(context.Background(), row[:]); more || err != nil {
		t.Fatal("unexpected effect prefix", more, err)
	}
}

// Begin is the real planning operation: an ineligible definition prunes root
// syntax instead of lending its accepted backing to the next attempt.
func FoldRootsReusable(owner *core.PreparedFolds) bool {
	if owner == nil || !owner.Live() {
		return false
	}
	current := owner.Current()
	next := owner.Begin()
	for i, entry := range current.Entries {
		if entry.Origin.Owner == (ids.ObjectRef{}) {
			return len(next.Candidate.Entries) == len(current.Entries) && &next.Candidate.Entries[i] == &current.Entries[i]
		}
	}
	return false
}

func PreparedBoundaryName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}

func PreparedBoundaryInteger(value int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: value}}
}

func PreparedUnitStatement(t testing.TB, e *core.Engine, s *session.Session, stmt parser.Statement) {
	t.Helper()
	op := TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(stmt, 0, nil)
	err = scope.Finish(err)
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
}
