package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTimeCaptureIdentityRebindAndSizing(t *testing.T) {
	ctx := context.Background()
	e := New()
	precision := int32(3)
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 4, 5, 6, 499500000, time.UTC)}
	later := sqltype.InputEnvironment{TransactionStart: time.Date(2004, 8, 8, 12, 13, 14, 123456000, time.UTC)}
	raw := parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "time", Precision: &precision, Pos: parser.Position{Offset: 7, Character: 8}}, Text: "now", Pos: parser.Position{Offset: 15, Character: 16}}
	stamp := parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "timestamp", Pos: parser.Position{Offset: 30, Character: 31}}, Text: "now", Pos: parser.Position{Offset: 40, Character: 41}}
	b := newExpressionBinder(ctx, e, first, nil, nil)
	for _, expr := range []parser.Expr{raw, stamp} {
		if _, err := b.bind(expr, expressionPolicy{context: projectionContext}); err != nil {
			t.Fatal(err)
		}
	}
	desc := StatementDescription{temporals: b.temporals}
	before := append([]temporalConstant(nil), desc.temporals...)
	if len(before) != 2 || before[0].typeID != 1083 || before[0].scalar != 14706500000 || before[0].typmod != 3 || before[1].typeID != 1114 {
		t.Fatal(before)
	}
	if err := e.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().LookupType(sqltype.Time)
	b = newExpressionBinder(ctx, e, later, nil, &desc)
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&EvalContext{Context: ctx})
	if err != nil || value.Type() != current || node.ResultUsage().Typmod != 3 {
		t.Fatal(value, err)
	}
	out, _ := current.Output(ctx, value)
	if out != "04:05:06.5" {
		t.Fatal(out)
	}
	dest, _, _ := current.ResolveModifier(0)
	node, err = b.bind(raw, expressionPolicy{context: assignmentContext, expected: dest, mode: sqltype.Assignment})
	if err != nil {
		t.Fatal(err)
	}
	value, err = node.Eval(&EvalContext{Context: ctx})
	out, _ = current.Output(ctx, value)
	if err != nil || out != "04:05:07" {
		t.Fatal(out, err)
	}
	node, err = b.bind(stamp, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	value, err = node.Eval(&EvalContext{Context: ctx})
	out, _ = value.Type().Output(ctx, value)
	if err != nil || out != "2003-07-07 04:05:06.4995" {
		t.Fatal(out, err)
	}
	if !reflect.DeepEqual(before, desc.temporals) {
		t.Fatal("capture mutated")
	}
	// Contextual UNKNOWN inputs freeze; actual parameters never become captures.
	contextual := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "now", Pos: parser.Position{Offset: 3, Character: 4}}}
	policy := expressionPolicy{context: assignmentContext, expected: current.Usage(), mode: sqltype.Assignment}
	b = newExpressionBinder(ctx, e, first, nil, nil)
	node, err = b.bind(contextual, policy)
	if err != nil {
		t.Fatal(err)
	}
	contextDesc := StatementDescription{temporals: b.temporals}
	b = newExpressionBinder(ctx, e, later, nil, &contextDesc)
	node, err = b.bind(contextual, policy)
	if err != nil {
		t.Fatal(err)
	}
	value, err = node.Eval(&EvalContext{Context: ctx})
	out, _ = current.Output(ctx, value)
	if err != nil || out != "04:05:06.4995" {
		t.Fatal(out, err)
	}
	b = newExpressionBinder(ctx, e, later, nil, &contextDesc)
	_, err = b.bind(parser.ParameterExpr{Number: 1}, policy)
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := b.finish()
	if err != nil || parameters.Types[0].Type != current || parameters.Types[0].Typmod != -1 || len(b.temporals) != 0 {
		t.Fatal(parameters, err)
	}
	// A TIME capture cannot silently reconstruct through a TIMESTAMP descriptor.
	foreign := stamp
	foreign.Pos = raw.Pos
	b = newExpressionBinder(ctx, e, later, nil, &desc)
	if _, err = b.bind(foreign, expressionPolicy{context: projectionContext}); err == nil {
		t.Fatal("capture type changed")
	}
}

func TestTimeDescriptionAndNoticeOnce(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INTEGER,v TIME(0) NOT NULL)")
	run(t, e, "INSERT INTO t VALUES(1,'00:00')")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ast := statement(t, "SELECT TIME(7) 'now',v FROM t WHERE v=$1")
	desc, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	notices := op.DetachNotices()
	if len(notices) != 1 || notices[0].Code != "22023" || notices[0].Position != "8" {
		t.Fatal(notices)
	}
	if len(desc.temporals) != 1 || desc.temporals[0].typeID != 1083 || desc.Parameters.Count != 1 || desc.Parameters.Types[0].Type.ID() != 1083 || desc.Parameters.Types[0].Typmod != -1 {
		t.Fatal(desc)
	}
	a, b := desc.Results[0], desc.Results[1]
	if a.Name != "time" || a.Usage.Typmod != 6 || a.OriginTable != 0 || a.OriginAttribute != 0 || a.NullabilityKnown {
		t.Fatal(a)
	}
	if b.Name != "v" || b.Usage.Typmod != 0 || b.OriginTable == 0 || b.OriginAttribute != 2 || b.Nullable || !b.NullabilityKnown {
		t.Fatal(b)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &desc
	r, err := scope.Execute(ast, 1, func(ctx context.Context, info ParameterInfo, frame []sqltype.Value, env sqltype.InputEnvironment) error {
		var err error
		frame[0], err = info.Types[0].Type.InputWithEnvironment(ctx, "00:00", -1, env)
		return err
	})
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	defer r.Rows.Close()
	values := make([]sqltype.Value, 2)
	if present, nextErr := r.Rows.Next(context.Background(), values); !present || nextErr != nil {
		t.Fatal(present, nextErr)
	}
	captured, captureErr := values[0].Time()
	midnight, midnightErr := values[1].Time()
	if captureErr != nil || midnightErr != nil || captured != desc.temporals[0].scalar || midnight != 0 {
		t.Fatal(captured, captureErr, midnight, midnightErr)
	}
	if present, nextErr := r.Rows.Next(context.Background(), values); present || nextErr != nil || len(op.DetachNotices()) != 0 {
		t.Fatal("extra row or repeated warning", present, nextErr)
	}
}
