package engine

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestTimestampCaptureRebindAndSizing(t *testing.T) {
	ctx := context.Background()
	e := New()
	p := int32(3)
	raw := parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "timestamp", Modifiers: []parser.TypeModifier{{Integer: &p}}, Pos: parser.Position{Offset: 7, Character: 8}}, Text: "now", Pos: parser.Position{Offset: 20, Character: 21}}
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2024, 12, 31, 23, 59, 59, 123456000, time.UTC)}
	b := newTestExpressionBinder(t, ctx, e, first, nil)
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	v, err := node.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := v.Timestamp()
	wantDesc := StatementDescription{temporals: b.temporals}
	before := append([]temporalConstant(nil), wantDesc.temporals...)
	old := v.Type()
	if err = e.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().LookupType(sqltype.Timestamp)
	if current == old {
		t.Fatal("replacement reused descriptor")
	}
	later := sqltype.InputEnvironment{TransactionStart: first.TransactionStart.Add(24 * time.Hour)}
	b = newTestExpressionBinder(t, ctx, e, later, &wantDesc)
	node, err = b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	v, err = node.Eval(&EvalContext{Context: ctx})
	got, _ := v.Timestamp()
	if err != nil || v.Type() != current || got != n || node.ResultUsage().Typmod != 3 || !reflect.DeepEqual(before, wantDesc.temporals) {
		t.Fatal(v, node.ResultUsage(), err)
	}
	dest, _, _ := current.ResolveModifiers(0)
	b = newTestExpressionBinder(t, ctx, e, later, &wantDesc)
	node, err = b.bind(raw, expressionPolicy{context: assignmentContext, expected: dest, mode: sqltype.Assignment})
	if err != nil {
		t.Fatal(err)
	}
	v, err = node.Eval(&EvalContext{Context: ctx})
	out, _ := current.Output(ctx, v)
	if err != nil || out != "2024-12-31 23:59:59" {
		t.Fatal(out, err)
	}
	// An unrelated current contextual target is not forced to the old type.
	contextual := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "now", Pos: raw.Pos}}
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	node, err = b.bind(contextual, expressionPolicy{context: assignmentContext, expected: text.Usage(), mode: sqltype.Assignment})
	if err != nil || node.ResultUsage().Type != text {
		t.Fatal(node, err)
	}
	// Parameters carry no capture and retain unsized type identity.
	b = newTestExpressionBinder(t, ctx, e, later, &wantDesc)
	node, err = b.bind(parser.ParameterExpr{Number: 1}, expressionPolicy{context: assignmentContext, expected: dest, mode: sqltype.Assignment})
	if err != nil {
		t.Fatal(err)
	}
	info, err := b.finish()
	if err != nil || info.Types[0].Type != current || info.Types[0].Typmod != -1 || len(b.temporals) != 0 {
		t.Fatal(info, err)
	}
}
func TestTimestampDescriptionMetadataAndNoticeOnce(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INTEGER,v TIMESTAMP(0) NOT NULL)")
	run(t, e, "INSERT INTO t VALUES(1,'2000-01-01')")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ast := statement(t, "SELECT TIMESTAMP(7) 'now',v FROM t WHERE v=$1")
	description, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	notices := op.DetachNotices()
	if len(notices) != 1 || notices[0].Code != "22023" || notices[0].Position != "8" {
		t.Fatal(notices)
	}
	if len(description.temporals) != 1 || description.Parameters.Count != 1 || description.Parameters.Types[0].Typmod != -1 || description.Parameters.Types[0].Type.ID() != 1114 {
		t.Fatal(description)
	}
	a, b := description.Results[0], description.Results[1]
	if a.Name != "timestamp" || a.Usage.Typmod != 6 || a.OriginTable != 0 || a.OriginAttribute != 0 || a.NullabilityKnown {
		t.Fatal(a)
	}
	if b.Name != "v" || b.Usage.Typmod != 0 || b.OriginTable == 0 || b.OriginAttribute != 2 || b.Nullable || !b.NullabilityKnown || b.Usage.Collation.ID != 0 {
		t.Fatal(b)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &description
	r, err := scope.Execute(ast, 1, func(ctx context.Context, info ParameterInfo, frame []sqltype.Value, env sqltype.InputEnvironment) error {
		var err error
		frame[0], err = info.Types[0].Type.InputWithEnvironment(ctx, "2000-01-01", -1, env)
		return err
	})
	if finish := scope.Finish(err); finish != nil {
		t.Fatal(finish)
	}
	defer r.Rows.Close()
	if len(op.DetachNotices()) != 0 || r.Count != 1 {
		t.Fatal("notice repeated or comparison sized incorrectly")
	}
}
func TestTimestampTransactionStartAndEndpointCapture(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(v TIMESTAMP)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	before := time.Now().UTC()
	tx, err := e.Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()
	start := op.session.transactions.start
	if start.Before(before.Truncate(time.Microsecond)) || start.After(after) || start.Location() != time.UTC || start.Nanosecond()%1000 != 0 {
		t.Fatal("transaction start must be quantized UTC within BEGIN bounds", start, before, after)
	}
	for range 2 {
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := scope.Describe(statement(t, "INSERT INTO t VALUES('now')"))
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		if !scope.input.TransactionStart.Equal(start) || len(desc.temporals) != 1 {
			t.Fatal(scope.input, desc)
		}
		if err = scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	if !op.session.transactions.start.IsZero() {
		t.Fatal("transaction clock retained after clear")
	}
	ast := statement(t, "INSERT INTO t VALUES(TIMESTAMP(0) '294276-12-31 23:59:59.999999')")
	desc, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &desc
	_, err = scope.Execute(ast, 0, nil)
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	r := run(t, e, "SELECT v FROM t")
	defer r.Rows.Close()
	values := make([]sqltype.Value, 1)
	if present, nextErr := r.Rows.Next(context.Background(), values); !present || nextErr != nil {
		t.Fatal(present, nextErr)
	}
	out, err := values[0].Type().Output(context.Background(), values[0])
	if err != nil || out != "294277-01-01 00:00:00" {
		t.Fatal(out, err)
	}
}
func TestTimestampRelativeConstantsFreezeAcrossDateline(t *testing.T) {
	ctx := context.Background()
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Timestamp)
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2024, 12, 31, 23, 59, 59, 123456000, time.UTC)}
	later := sqltype.InputEnvironment{TransactionStart: first.TransactionStart.Add(48 * time.Hour)}
	for _, text := range []string{"today", "tomorrow", "yesterday"} {
		raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text, Pos: parser.Position{Offset: 7, Character: 8}}}
		policy := expressionPolicy{context: assignmentContext, expected: typ.Usage(), mode: sqltype.Assignment}
		b := newTestExpressionBinder(t, ctx, e, first, nil)
		node, err := b.bind(raw, policy)
		if err != nil {
			t.Fatal(err)
		}
		initial, err := node.Eval(&EvalContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		description := StatementDescription{temporals: b.temporals}
		b = newTestExpressionBinder(t, ctx, e, later, &description)
		node, err = b.bind(raw, policy)
		if err != nil {
			t.Fatal(err)
		}
		frozen, err := node.Eval(&EvalContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		a, _ := initial.Timestamp()
		z, _ := frozen.Timestamp()
		fresh, err := typ.InputWithEnvironment(ctx, text, -1, later)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := fresh.Timestamp()
		if a != z || f-a != 2*86400000000 {
			t.Fatal(text, a, z, f)
		}
	}
}
func TestTimestampCatalogModifierValidation(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(v TIMESTAMP(3))")
	view := e.CatalogRead()
	matches := view.Lookup(ids.NameKey{ClassID: RelationClassID, Namespace: ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "t"})
	if len(matches) != 1 {
		t.Fatal(matches)
	}
	descriptor, _ := view.Descriptor(matches[0])
	original := descriptor.(*relationDescriptor)
	provider := relationProvider{}
	if err := provider.Validate(view, original); err != nil {
		t.Fatal("valid precision rejected", err)
	}
	current := original.columns[0].Usage.Type
	foreign, _ := New().CatalogRead().LookupType(sqltype.Timestamp)
	for _, usage := range []sqltype.Usage{
		{Type: current, Typmod: -2}, {Type: current, Typmod: 7},
		{Type: current, Typmod: 3, Collation: sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}},
		{Type: foreign, Typmod: 3},
	} {
		copy := *original
		copy.columns = append([]ColumnDescriptor(nil), original.columns...)
		copy.columns[0].Usage = usage
		if err := provider.Validate(view, &copy); err == nil {
			t.Fatal("invalid/foreign usage admitted", usage)
		}
	}
	if original.columns[0].Usage.Typmod != 3 {
		t.Fatal("validation mutated relation")
	}
}

func TestTimestampSQLTransactionControlClock(t *testing.T) {
	for _, variant := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "ABORTED COMMIT AND CHAIN"} {
		t.Run(variant, func(t *testing.T) {
			end := variant
			aborted := variant == "ABORTED COMMIT AND CHAIN"
			if aborted {
				end = "COMMIT AND CHAIN"
			}
			e := New()
			run(t, e, "CREATE TABLE t(v TIMESTAMP)")
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			before := time.Now().UTC()
			if _, err = e.Execute(op, statement(t, "BEGIN"), 0, nil); err != nil {
				t.Fatal(err)
			}
			owner := op.session.transactions
			if owner.start.Before(before.Truncate(time.Microsecond)) || owner.start.After(time.Now().UTC()) || owner.start.Location() != time.UTC || owner.start.Nanosecond()%1000 != 0 {
				t.Fatal("SQL BEGIN must capture a quantized UTC transaction start within bounds", owner.start)
			}
			// A deterministic old incarnation distinguishes fresh chain birth
			// without sleeping or relying on wall-clock resolution.
			oldStart := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			owner.mu.Lock()
			owner.start = oldStart
			oldRef := owner.ref
			owner.mu.Unlock()
			checkInput := func(want time.Time) {
				t.Helper()
				scope, scopeErr := e.StartStatement(op)
				if scopeErr != nil {
					t.Fatal(scopeErr)
				}
				_, scopeErr = scope.Execute(statement(t, "INSERT INTO t VALUES($1)"), 1,
					func(ctx context.Context, info ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
						if !input.TransactionStart.Equal(want) {
							return fmt.Errorf("input start %v, want %v", input.TransactionStart, want)
						}
						var inputErr error
						frame[0], inputErr = info.Types[0].Type.InputWithEnvironment(ctx, "now", -1, input)
						return inputErr
					})
				if scopeErr = scope.Finish(scopeErr); scopeErr != nil {
					t.Fatal(scopeErr)
				}
			}
			checkInput(oldStart)
			checkInput(oldStart)
			if _, err = e.Describe(op, statement(t, end)); err != nil {
				t.Fatal(err)
			}
			if _, err = e.Execute(op, statement(t, "BEGIN"), 0, nil); err != nil {
				t.Fatal(err)
			}
			if !owner.start.Equal(oldStart) {
				t.Fatal("control preparation/redundant BEGIN changed clock", owner.start)
			}
			if aborted {
				_, err = e.Execute(op, statement(t, "INSERT INTO t VALUES(TIMESTAMP 'nonsense')"), 0, nil)
				state(t, err, "22007")
			}
			before = time.Now().UTC()
			if _, err = e.Execute(op, statement(t, end), 0, nil); err != nil {
				t.Fatal(err)
			}
			newStart := owner.start
			if owner.state != TxActive || owner.ref == oldRef || newStart.Before(before.Truncate(time.Microsecond)) || newStart.After(time.Now().UTC()) || newStart.Location() != time.UTC || newStart.Nanosecond()%1000 != 0 {
				t.Fatal("CHAIN must capture a fresh quantized UTC transaction start within bounds", owner.state, owner.ref, newStart)
			}
			checkInput(newStart)
			checkInput(newStart)
			if _, err = e.Execute(op, statement(t, "ROLLBACK"), 0, nil); err != nil {
				t.Fatal(err)
			}
			if !owner.start.IsZero() {
				t.Fatal("SQL end retained transaction clock", owner.start)
			}
		})
	}
}
