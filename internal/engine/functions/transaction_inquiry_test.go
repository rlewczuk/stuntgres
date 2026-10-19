package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTransactionInquiryImmutableProtocols(t *testing.T) {
	d := temporalTestTypes()
	original := temporalTestLiteral(sqltype.Null(d[sqltype.Xid]))
	other := temporalTestLiteral(sqltype.Null(d[sqltype.Text]))
	identity := transactionIdentityExpression{Info: expr.Info{Usage: d[sqltype.Int4].Usage(), Property: expr.Properties{Volatility: expr.Stable}, RoutineReference: jsonpathArrayUnitReference(1181)}, kind: identityAge, arg: original}
	timestamp := commitTimestampExpression{Info: expr.Info{Usage: d[sqltype.Timestamptz].Usage(), Property: expr.Properties{Volatility: expr.Volatile}, RoutineReference: jsonpathArrayUnitReference(3581)}, kind: commitTimestampScalar, arg: original}
	for _, node := range []expr.Composite{identity, timestamp} {
		info := expr.InfoOf(node)
		if err := node.(expr.StoredProvider).ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		if _, err := node.Rebuild(info, []expr.Expr{other}); err == nil {
			t.Fatal("Rebuild must reject wrong formal")
		}
		next, err := expr.Substitute(node, info, []expr.Expr{other})
		if err != nil || next.(expr.Composite).Children()[0] != other || node.Children()[0] != original || expr.InfoOf(next) != info {
			t.Fatal("Substitute must preserve metadata and original", err)
		}
		if _, err := node.Rebuild(info, nil); err == nil {
			t.Fatal("invalid arity")
		}
		if _, err := expr.Substitute(node, info, []expr.Expr{nil}); err == nil {
			t.Fatal("nil child")
		}
		rebuilt, err := node.Rebuild(info, []expr.Expr{original})
		same := func(a, b expr.Expr) bool { return a == b }
		if err != nil || !node.(expr.GroupIdentity).SameGroup(rebuilt, same, false) {
			t.Fatal("valid rebuild grouping", err)
		}
		operands, admitted, err := node.(expr.Capturable).Capture(expr.CaptureFunction)
		if err != nil || !admitted || operands.Count != 1 || operands.Inline[0] != original {
			t.Fatal("written operand capture", err)
		}
		if _, admitted, _ := node.(expr.Capturable).Capture(expr.CaptureOperator); admitted {
			t.Fatal("operator capture admitted")
		}
		if !node.(expr.ConditionalClosure).ClosedDefault(func(child expr.Expr) bool { return child == original }) || node.(expr.ConditionalClosure).ClosedDefault(func(expr.Expr) bool { return false }) {
			t.Fatal("conditional child closure")
		}
		malformed, _ := expr.Substitute(node, info, []expr.Expr{other})
		rendered, err := malformed.(expr.Renderable).Render()
		if err != nil || rendered.Replay.Validate == nil || rendered.Replay.Validate() == nil {
			t.Fatal("display versus lazy replay validation", err)
		}
	}
	// Zero-argument calls still have an explicit empty written-operand shape,
	// reject introduced children and preserve structural function identity.
	current := transactionIdentityExpression{Info: expr.Info{Usage: d[sqltype.Xid8].Usage(), Property: expr.Properties{Volatility: expr.Stable}}, kind: identityCurrent}
	assigned := current
	assigned.kind = identityAssigned
	same := func(a, b expr.Expr) bool { return a == b }
	if current.SameGroup(assigned, same, false) {
		t.Fatal("different identity operations grouped together")
	}
	operands, admitted, err := current.Capture(expr.CaptureFunction)
	if err != nil || !admitted || operands.Count != 0 {
		t.Fatal("empty written operands", err)
	}
	if _, err := current.Rebuild(current.Info, []expr.Expr{original}); err == nil {
		t.Fatal("zero-argument rebuild accepted child")
	}
	invalid := current
	invalid.kind = identityMembers + 1
	if err := invalid.validate(); err == nil {
		t.Fatal("invalid identity kind accepted")
	}
	invalidTimestamp := timestamp
	invalidTimestamp.kind = commitTimestampLast + 1
	if err := invalidTimestamp.validate(); err == nil {
		t.Fatal("invalid timestamp kind accepted")
	}
}
