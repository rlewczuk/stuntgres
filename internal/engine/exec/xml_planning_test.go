package exec

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type xmlPlanningNotices struct{ events []sqlstate.Error }

func (n *xmlPlanningNotices) XMLInputDiagnostic(e sqlstate.Error) sqlstate.Error { return e }
func (n *xmlPlanningNotices) XMLInputWarning(e sqlstate.Error)                   { n.events = append(n.events, e) }

func TestXMLPlanningExecutionBarrier(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	text, xml := types[sqltype.Text].Usage(), types[sqltype.XML].Usage()
	source := plan.LiteralExpr{Info: expr.Info{Usage: text}, Value: sqltype.String(text.Type, `<?xml version="1.1"?><a/>`)}
	parse := functions.XMLParse{Info: expr.Info{Usage: xml}, Child: source, Document: true}
	serialize, err := functions.NewXMLSerialize(expr.Info{Usage: text}, parse, true, "indent")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := sqltype.PlanCoercion(text, text, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := plan.CoercionExpr{Info: expr.Info{Usage: text}, Child: serialize, Plan: identity, Mode: sqltype.Explicit}
	notices := &xmlPlanningNotices{}
	captures := FoldCaptureCandidate{}
	c := &Context{EvalStatement: EvalStatement{Context: context.Background(), Input: sqltype.InputEnvironment{XMLInput: notices}, FoldCaptures: &captures}}
	for range 2 {
		next, _, err := PreflightCoercionsWithPolicy(c, wrapped, true)
		if err != nil || len(notices.events) != 0 || len(captures.Entries) != 0 {
			t.Fatal("enclosing cast crossed execution barrier", err, len(notices.events), captures)
		}
		call, ok := next.(plan.CoercionExpr)
		if !ok {
			t.Fatal("execution recipe was folded", next)
		}
		if !planningExecutionBarrier(call) {
			t.Fatal("lost real execution-demand descendant")
		}
	}
	for range 2 {
		if _, err := serialize.Eval(c); err != nil {
			t.Fatal(err)
		}
	}
	if len(notices.events) != 4 {
		t.Fatal("parse/serialize diagnostics were cached or replayed", len(notices.events))
	}
	// A malformed execution provider likewise stays below the enclosing cast.
	source.Value = sqltype.String(text.Type, "<a>")
	parse.Child = source
	malformed, err := functions.NewXMLSerialize(expr.Info{Usage: text}, parse, true, "no indent")
	if err != nil {
		t.Fatal(err)
	}
	wrapped.Child = malformed
	if _, _, err := PreflightCoercionsWithPolicy(c, wrapped, true); err != nil {
		t.Fatal("runtime XML error moved to planning", err)
	}
	if _, err := malformed.Eval(c); err == nil {
		t.Fatal("genuine runtime parser failure disappeared")
	}
}

func TestPlanningExecutionBarrierScope(t *testing.T) {
	// Relational providers keep node-local FoldNever without imposing XML's
	// transitive diagnostic demand on enclosing preflight computations.
	document := plan.LiteralExpr{}
	table := functions.JSONTable{Args: []expr.Expr{document}}
	if table.PlanningFold() != expr.FoldNever || planningExecutionBarrier(table) {
		t.Fatal("relational FoldNever became a transitive execution barrier")
	}
	for _, provider := range []expr.Expr{
		functions.XMLParse{Child: document},
		functions.XMLSerialize{Child: document},
	} {
		wrapped := plan.CoercionExpr{Child: provider}
		if !planningExecutionBarrier(wrapped) {
			t.Fatal("XML diagnostic demand did not reach its enclosing coercion")
		}
	}
}
