package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func transactionTimeUnitLeaf(t *testing.T, b *analyze.Binder, kind functions.TransactionTimeKind) functions.TransactionTime {
	t.Helper()
	name := kind.Name()
	bound, err := b.BindFunction(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Pos: parser.Position{Offset: 7, Character: 8}})
	if err != nil {
		t.Fatal(err)
	}
	leaf, ok := bound.(functions.TransactionTime)
	if !ok {
		t.Fatalf("producer=%T", bound)
	}
	return leaf
}
func transactionTimeUnitAdvance(t *testing.T, stamp time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !time.Now().After(stamp.Add(time.Microsecond)) {
		if time.Now().After(deadline) {
			t.Fatal("clock did not advance", stamp)
		}
		runtime.Gosched()
	}
}

func TestTransactionTimeArgumentLabelInvariant(t *testing.T) {
	engine := New()
	binder := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"now", "transaction_timestamp", "statement_timestamp", "clock_timestamp", "timeofday"} {
		statements, err := parser.Parse("SELECT " + name + "(1,2)")
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*parser.Select).Targets[0].(parser.FunctionExpr)
		for _, count := range []int{1, 3} {
			call.ArgNames = make([]parser.Identifier, count)
			call.ArgNames[0] = parser.Identifier{Text: "x", Pos: call.Pos}
			bound, err := binder.BindFunction(call)
			if bound != nil || err == nil || err.Error() != "invalid function argument labels" {
				t.Fatalf("%s labels=%d: bound=%T error=%v", name, count, bound, err)
			}
		}
	}
}

func TestTransactionTimeExpressionContracts(t *testing.T) {
	ctx := context.Background()
	engine := New()
	binder := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	transaction := time.Date(2000, 1, 1, 12, 0, 0, 123456789, time.UTC)
	statement := time.Date(2000, 1, 1, 12, 0, 1, 654321987, time.UTC)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Input: sqltype.InputEnvironment{TransactionStart: transaction}, Host: HostOf(&StatementScope{statementStart: statement})}}
	for _, kind := range []functions.TransactionTimeKind{functions.TransactionTimeNow, functions.TransactionTimeTransaction, functions.TransactionTimeStatement, functions.TransactionTimeClock, functions.TransactionTimeOfDay} {
		leaf := transactionTimeUnitLeaf(t, binder, kind)
		if leaf.Usage.Type.ID() != kind.Type() || leaf.Usage.Typmod != -1 || leaf.Properties() != expr.IntrinsicProperties(kind.IntrinsicRule()) || exec.PlannedVolatility(leaf) != leaf.Property.Volatility || plan.GenerationImmutable(leaf) {
			t.Fatal("type/volatility", leaf)
		}
		if _, err := leaf.Eval((*exec.Context)(nil)); err == nil {
			t.Error("missing context accepted")
		}
		if kind <= functions.TransactionTimeStatement {
			value, err := leaf.Eval(c)
			if err != nil {
				t.Fatal(err)
			}
			instant, err := value.TimestampTime()
			if err != nil {
				t.Fatal(err)
			}
			expected := time.Date(2000, 1, 1, 12, 0, 0, 123456000, time.UTC)
			if kind == functions.TransactionTimeStatement {
				expected = time.Date(2000, 1, 1, 12, 0, 1, 654321000, time.UTC)
			}
			if !instant.Equal(expected) || value.Type() != leaf.Usage.Type {
				t.Error("fixed instant", instant, expected)
			}
		}
		spec, err := leaf.Render()
		sql := spec.Text
		if err != nil || sql != kind.Name()+"()" || spec.Replay.Text != "" {
			t.Fatal(sql, err)
		}
		rendered, err := (stored.ScalarRenderer{}).SQL(ctx, engine.CatalogRead(), leaf)
		if err != nil || rendered != sql {
			t.Fatal(rendered, err)
		}
		rendered, err = (stored.Deparser{View: engine.CatalogRead()}).SQL(ctx, leaf, stored.DeparseParent{})
		if err != nil || rendered != sql {
			t.Fatal(rendered, err)
		}
		tree, err := (plan.BoundTree{Expression: leaf}).StructuralOutput(ctx)
		if err != nil || !strings.Contains(tree, "TransactionTime") || strings.Contains(tree, "Const") {
			t.Fatal(tree, err)
		}
		target := New()
		retagged, changed, err := stored.RetagTypes(target.CatalogRead(), leaf, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := target.CatalogRead().LookupType(d.ID())
			return next
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		remapped, err := stored.RemapExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef { t.Fatal("leaf invented dependency", ref); return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		copy := remapped.(functions.TransactionTime)
		if copy.Kind != kind || copy.Usage.Type == leaf.Usage.Type || copy.Pos != leaf.Pos || copy.Validate(expr.PropertiesMatch(copy.Property, copy.Kind.IntrinsicRule())) != nil {
			t.Fatal(leaf, copy)
		}
		if sides, err := plan.JoinExpressionSides(copy, 0, 0); err != nil || sides != 0 {
			t.Fatal(sides, err)
		}
		if !binder.SameGroupExpression(leaf, leaf) {
			t.Error("leaf not equivalent to itself")
		}
		other := leaf
		other.Kind = functions.TransactionTimeTransaction
		if kind == functions.TransactionTimeNow && binder.SameGroupExpression(leaf, other) {
			t.Error("alias spelling merged")
		}
		if !analyze.ClosedTransactionTimeDefault(leaf) {
			t.Error("selected closed default rejected")
		}
		for _, properties := range []expr.Properties{{Volatility: expr.Volatile, RowDependent: true}, {Volatility: expr.Volatile, ParameterDependent: true}, {Volatility: expr.Volatile, StageDependent: true}} {
			bad := leaf
			bad.Property = properties
			if analyze.ClosedTransactionTimeDefault(bad) {
				t.Error("dependent leaf admitted")
			}
		}
	}
	clock := transactionTimeUnitLeaf(t, binder, functions.TransactionTimeClock)
	invalid := clock
	invalid.Kind = 0
	if invalid.Validate(expr.PropertiesMatch(invalid.Property, invalid.Kind.IntrinsicRule())) == nil {
		t.Error("unknown kind admitted")
	}
	invalid = clock
	invalid.Usage = binder.Type(sqltype.Text).Usage()
	if invalid.Validate(expr.PropertiesMatch(invalid.Property, invalid.Kind.IntrinsicRule())) == nil {
		t.Error("wrong result type admitted")
	}
	invalid = clock
	invalid.Property.Volatility = expr.Stable
	if invalid.Validate(expr.PropertiesMatch(invalid.Property, invalid.Kind.IntrinsicRule())) == nil {
		t.Error("demoted clock admitted")
	}
	random := functions.Random{Info: expr.Info{Usage: binder.Type(sqltype.Float8).Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}
	if analyze.ClosedTransactionTimeDefault(random) {
		t.Error("unrelated volatile default admitted")
	}
	for _, raw := range []parser.Expr{
		parser.CastExpr{Child: parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "clock_timestamp"}}}}, TypeName: parser.TypeName{Name: "text"}},
		parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "lower"}}}, Args: []parser.Expr{parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "timeofday"}}}}}},
	} {
		node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if !analyze.ClosedTransactionTimeDefault(node) {
			t.Error("closed selected wrapper rejected")
		}
		if _, err := (stored.ScalarRenderer{}).SQL(ctx, engine.CatalogRead(), node); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTransactionTimeWallDemand(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Input: sqltype.InputEnvironment{TransactionStart: stamp}, Host: HostOf(&StatementScope{statementStart: stamp})}}
	for _, kind := range []functions.TransactionTimeKind{functions.TransactionTimeNow, functions.TransactionTimeStatement, functions.TransactionTimeClock, functions.TransactionTimeOfDay} {
		leaf := transactionTimeUnitLeaf(t, b, kind)
		first, err := leaf.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		transactionTimeUnitAdvance(t, time.Now())
		second, err := leaf.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		same := first == second
		if same != (kind == functions.TransactionTimeNow || kind == functions.TransactionTimeStatement) {
			t.Error("demand ownership", kind, first, second)
		}
		if kind != functions.TransactionTimeOfDay {
			instant, err := second.TimestampTime()
			if err != nil || instant.Nanosecond()%1000 != 0 {
				t.Fatal(instant, err)
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		copy := *c
		copy.Context = canceled
		if _, err := leaf.Eval(&copy); !errors.Is(err, context.Canceled) {
			t.Error("cancellation", err)
		}
	}
}

func TestTransactionTimeCommandOwnership(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	op := transactionOperation(t, session)
	defer op.Done()
	fixed := time.Date(2000, 1, 1, 12, 0, 0, 123456000, time.UTC)
	op.CommandStart = fixed
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if !scope.statementStart.Equal(fixed) || !scope.Input.TransactionStart.Equal(fixed) {
		t.Fatal("implicit birth did not reuse command", scope.statementStart, scope.Input.TransactionStart)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if !session.Transactions.Start.IsZero() {
		t.Error("ended transaction retained clock")
	}
	op.CommandStart = time.Time{}
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	first := scope.statementStart
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	transactionTimeUnitAdvance(t, time.Now())
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	second := scope.statementStart
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if !second.After(first) || !op.CommandStart.IsZero() {
		t.Error("generic operation latched statement clock", first, second)
	}
	op.CommandStart = fixed
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.executeControl(&parser.TransactionControl{Kind: parser.KindStart}, 0)
	if err := scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	if !session.Transactions.Start.Equal(fixed) {
		t.Error("SQL BEGIN discarded command clock")
	}
	op.CommandStart = fixed.Add(time.Second)
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if !scope.Input.TransactionStart.Equal(fixed) || !scope.statementStart.Equal(fixed.Add(time.Second)) {
		t.Error("existing transaction overwritten")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.executeControl(&parser.TransactionControl{Kind: parser.KindCommit, Chain: true}, 0)
	if err := scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	if !session.Transactions.Start.Equal(op.CommandStart) {
		t.Error("CHAIN discarded command clock")
	}
	transactionEnd(t, session, session.Transactions.Ref, false)
}

func TestTransactionTimeAdmissionWait(t *testing.T) {
	e := New()
	sess := sessionStart(t, e, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sess.Settings.Mu.Lock()
	locked := true
	type acquired struct {
		op  *session.Operation
		err error
	}
	done := make(chan acquired, 1)
	joined := false
	defer func() {
		cancel()
		if locked {
			sess.Settings.Mu.Unlock()
		}
		if !joined {
			result := <-done
			if result.op != nil {
				result.op.Done()
			}
		}
	}()
	go func() { op, _, err := sess.AcquireCommand(ctx); done <- acquired{op, err} }()
	for {
		sess.Mu.Lock()
		registered := len(sess.Operations) > 0
		sess.Mu.Unlock()
		if registered {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("admission registration", ctx.Err())
		}
		runtime.Gosched()
	}
	separation := time.Now()
	transactionTimeUnitAdvance(t, separation)
	sess.Settings.Mu.Unlock()
	locked = false
	result := <-done
	joined = true
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer result.op.Done()
	if !result.op.CommandStart.Before(separation) || result.op.CommandStart.Nanosecond()%1000 != 0 {
		t.Error("command sampled after settings wait", result.op.CommandStart, separation)
	}
	scope, err := e.StartStatement(result.op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if !scope.Input.TransactionStart.Equal(result.op.CommandStart) || !scope.statementStart.Equal(result.op.CommandStart) {
		t.Error("sample not transferred to implicit birth")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}
