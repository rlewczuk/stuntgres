package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A binder built without an operation keeps the nil-operation semantics it
// had before the host seam: no definition reader, no plan owner, no Restore
// replay and no notices.
func TestAnalysisHostWithoutOperation(t *testing.T) {
	e := New()
	host := e.AnalysisHost(nil)
	if host != e.AnalysisHost(nil) || host.Engine != e || host.Op != nil {
		t.Fatal("binding without an operation does not reuse the engine's host")
	}
	if d := host.BindingDefinition(nil); d != nil {
		t.Fatalf("BindingDefinition(nil) = %#v, want nil", d)
	}
	if owner, generation, ok := host.PlanOwner(); ok || owner != (ids.SessionToken{}) || generation != 0 {
		t.Fatalf("PlanOwner() = %v, %d, %v; want zero, 0, false", owner, generation, ok)
	}
	if host.ReplayInsertAuthority() {
		t.Fatal("ReplayInsertAuthority() = true without an operation")
	}
	host.EmitWarning(sqlstate.Error{Code: "01000", Message: "dropped"})
	b := NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	got, ok := b.Host.(*BindingHost)
	if !ok || got.Engine != e || got.Op != nil {
		t.Fatalf("binder host = %#v, want the engine and no operation", b.Host)
	}
}

func TestAnalysisHostWithOperation(t *testing.T) {
	e := New()
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	host := e.AnalysisHost(op)
	if host != e.AnalysisHost(op) || host.Op != op || host.Engine != e {
		t.Fatal("a session operation does not reuse its own binding host")
	}
	copied := &session.Operation{Session: op.Session, Analysis: op.Analysis}
	if fresh := e.AnalysisHost(copied); fresh == host || fresh.Op != copied {
		t.Fatal("a copied operation binds through the original operation's host")
	}
	owner, generation, ok := host.PlanOwner()
	if !ok || owner != op.Session.SessionToken || generation != op.Session.DiscardPlansGeneration {
		t.Fatalf("PlanOwner() = %v, %d, %v; want the session's token and generation", owner, generation, ok)
	}
	warning := sqlstate.Error{Code: "01000", Message: "kept"}
	host.EmitWarning(warning)
	if notices := op.DetachNotices(); len(notices) != 1 || notices[0].Message != "kept" {
		t.Fatalf("notices = %#v, want the warning", notices)
	}
	b := NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	if got, ok := b.Host.(*BindingHost); !ok || got.Engine != e || got.Op != op {
		t.Fatalf("binder host = %#v, want the engine and the operation", b.Host)
	}
}

// binderOperation and binderEngine read a binder's host state in tests.
func binderOperation(b *analyze.Binder) *session.Operation { return b.Host.(*BindingHost).Op }

func binderEngine(b *analyze.Binder) *Engine { return b.Host.(*BindingHost).Engine }
