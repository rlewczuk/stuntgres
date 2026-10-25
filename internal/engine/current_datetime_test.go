package engine

import (
	"context"
	"runtime"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestCurrentDateTimeBindingAndSymbolicConsumers(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, kind := range []parser.CurrentDateTimeKind{parser.CurrentDate, parser.CurrentTime, parser.CurrentTimestamp, parser.LocalTime, parser.LocalTimestamp} {
		raw := parser.CurrentDateTimeExpr{Kind: kind, Pos: parser.Position{Character: 8, Offset: 7}}
		if kind != parser.CurrentDate {
			raw.HasPrecision = true
			raw.Precision = 3
		}
		bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(raw, err)
		}
		node, ok := bound.(functions.CurrentDateTime)
		if !ok || node.Properties() != (expr.Properties{Volatility: expr.Stable}) || node.ResultUsage().Type.ID() != functions.CurrentDateTimeType(kind) || exec.PlannedVolatility(node) != expr.Stable || plan.GenerationImmutable(node) {
			t.Fatal(raw, node)
		}
		spec, err := node.Render()
		if err != nil {
			t.Fatal(err)
		}
		sql := spec.Replay.Text
		rendered, err := (stored.Deparser{View: e.CatalogRead()}).SQL(ctx, node, stored.DeparseParent{})
		if err != nil || rendered != strings.ToUpper(sql) {
			t.Fatal(rendered, sql, err)
		}
		replay, err := (stored.ScalarRenderer{}).SQL(ctx, e.CatalogRead(), node)
		if err != nil || replay != sql {
			t.Fatal(replay, sql, err)
		}
		tree, err := (plan.BoundTree{Expression: node}).StructuralOutput(ctx)
		if err != nil || !strings.Contains(tree, "CurrentDateTime") || strings.Contains(tree, "Const") {
			t.Fatal(tree, err)
		}
		target := New()
		retagged, changed, err := stored.RetagTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := target.CatalogRead().LookupType(d.ID())
			return next
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		remapped, err := stored.RemapExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef {
			t.Fatal("current leaf invented relation reference", ref)
			return ref
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		copy := remapped.(functions.CurrentDateTime)
		if copy.Kind != node.Kind || copy.Precision != node.Precision || copy.HasPrecision != node.HasPrecision || copy.Pos != node.Pos || copy.Usage.Type == node.Usage.Type || copy.Validate(expr.PropertiesMatch(copy.Property, functions.CurrentDateTimeIntrinsicRule())) != nil {
			t.Fatal(node, copy)
		}
		if sides, err := plan.JoinExpressionSides(copy, 0, 0); err != nil || sides != 0 {
			t.Fatal(sides, err)
		}
		if !b.SameGroupExpression(node, node) {
			t.Fatal("current leaf not semantically equal to itself")
		}
		other := node
		other.Kind = parser.CurrentDate
		if kind != parser.CurrentDate && b.SameGroupExpression(node, other) {
			t.Fatal("different current kinds equated")
		}
	}
	for _, raw := range []parser.CurrentDateTimeExpr{{Kind: 255}, {Kind: parser.CurrentDate, HasPrecision: true, Precision: 1}, {Kind: parser.CurrentTime, HasPrecision: true, Precision: -1}} {
		if _, _, err := functions.BindCurrentDateTime(b, raw); err == nil {
			t.Fatal("invalid current AST accepted", raw)
		}
	}
}

// Function-level lifecycle seams, not another public SQL scenario. AST controls
// and the admission gate exercise the same owner birth functions as SQL/driver.
func TestCurrentDateTimeTransactionBirthQuantization(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	assertClock := func(stamp time.Time) {
		t.Helper()
		if stamp.IsZero() || stamp.Location() != time.UTC || stamp.Nanosecond()%1000 != 0 {
			t.Fatal("owner clock not quantized UTC", stamp)
		}
	}
	scopeFor := func() *StatementScope {
		t.Helper()
		op := transactionOperation(t, session)
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		return scope
	}
	finish := func(scope *StatementScope, err error) {
		t.Helper()
		err = scope.Finish(err)
		scope.Op.Done()
		if err != nil {
			t.Fatal(err)
		}
	}
	advance := func(stamp time.Time) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !time.Now().After(stamp.Add(time.Microsecond)) {
			if time.Now().After(deadline) {
				t.Fatal("clock did not advance", stamp)
			}
			runtime.Gosched()
		}
	}
	implicit := scopeFor()
	if err := implicit.Gate(&parser.Select{}); err != nil {
		finish(implicit, err)
	}
	first := implicit.Input.TransactionStart
	assertClock(first)
	finish(implicit, nil)
	if !session.Transactions.Start.IsZero() {
		t.Fatal("implicit clock retained after end")
	}
	advance(first)
	ref := transactionBegin(t, session)
	owner := session.Transactions
	outer := owner.Start
	assertClock(outer)
	if !outer.After(first) {
		t.Fatal("driver birth did not refresh", first, outer)
	}
	owner.Mu.Lock()
	mark := CaptureSavepoint(owner, "clock")
	RestoreSavepoint(owner, mark, mark.Settings.Clone())
	restored := owner.Start
	owner.Mu.Unlock()
	if !restored.Equal(outer) {
		t.Fatal("savepoint rebirthed clock", outer, restored)
	}
	transactionEnd(t, session, ref, false)
	advance(outer)
	start := scopeFor()
	_, err := ScopeExecuteControl(start, &parser.TransactionControl{Kind: parser.KindStart}, 0)
	finish(start, err)
	sqlClock := owner.Start
	assertClock(sqlClock)
	if !sqlClock.After(outer) {
		t.Fatal("SQL birth did not refresh", sqlClock, outer)
	}
	advance(sqlClock)
	chain := scopeFor()
	_, err = ScopeExecuteControl(chain, &parser.TransactionControl{Kind: parser.KindCommit, Chain: true}, 0)
	finish(chain, err)
	assertClock(owner.Start)
	if !owner.Start.After(sqlClock) {
		t.Fatal("CHAIN birth did not refresh", owner.Start, sqlClock)
	}
	transactionEnd(t, session, owner.Ref, false)
}
