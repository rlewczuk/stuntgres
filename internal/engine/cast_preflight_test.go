package engine

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func preflightCast(t *testing.T, q string) expr.Expr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	expression, err := b.bind(castTarget(t, q), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	return expression
}

func TestCastCoercionPreflightRecursivePrivate(t *testing.T) {
	original := preflightCast(t, "SELECT CAST(int4('42'::text) AS text) FROM t")
	outer := original.(coercionExpression)
	inner := outer.child.(coercionExpression)
	evaluation := EvalContext{Context: context.Background()}
	for range 2 {
		folded, changed, err := preflightCoercions(&evaluation, original)
		if err != nil || !changed {
			t.Fatal(folded, changed, err)
		}
		literal, ok := folded.(literalExpression)
		if !ok || literal.Info != outer.Info {
			t.Fatal(folded)
		}
		value, err := literal.Eval(&evaluation)
		text, accessErr := value.Text()
		if err != nil || accessErr != nil || text != "42" {
			t.Fatal(value, err, accessErr)
		}
		if _, ok := original.(coercionExpression).child.(coercionExpression); !ok {
			t.Fatal("original tree mutated")
		}
	}
	column := columnExpression{Info: expr.Info{Usage: inner.ResultUsage(), Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
	predicate := comparisonExpression{Info: expr.Info{Property: expr.Properties{RowDependent: true}}, left: column, right: inner}
	folded, changed, err := preflightCoercions(&evaluation, predicate)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	if _, ok := folded.(comparisonExpression).right.(literalExpression); !ok {
		t.Fatal(folded)
	}
	if _, ok := predicate.right.(coercionExpression); !ok {
		t.Fatal("original predicate mutated")
	}
	bad := preflightCast(t, "SELECT int4('oops'::text) FROM t")
	predicate.right = bad
	_, _, err = preflightCoercions(&evaluation, predicate)
	state(t, err, "22P02")
}

func TestCastCoercionPreflightDependenceAndCancellation(t *testing.T) {
	original := preflightCast(t, "SELECT int4('oops'::text) FROM t").(coercionExpression)
	evaluation := EvalContext{Context: context.Background()}
	for _, properties := range []expr.Properties{
		{RowDependent: true}, {StageDependent: true}, {Volatility: expr.Volatility(1)},
	} {
		expression := original
		expression.Property = properties
		folded, changed, err := preflightCoercions(&evaluation, expression)
		if err != nil || changed || folded.(coercionExpression).Info != expression.Info {
			t.Fatal(properties, folded, changed, err)
		}
	}
	parameter := preflightCast(t, "SELECT int4($1::text) FROM t")
	_, _, err := preflightCoercions(&evaluation, parameter)
	if err == nil || err.Error() != "stuntgres: invalid expression parameter frame" {
		t.Fatal(err)
	}
	identity := preflightCast(t, "SELECT '42'::text::text FROM t")
	folded, changed, err := preflightCoercions(&evaluation, identity)
	if err != nil || changed || folded.ResultUsage() != identity.ResultUsage() {
		t.Fatal(folded, changed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = preflightCoercions(&EvalContext{Context: ctx}, original)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCastCoercionPreflightDependentParent(t *testing.T) {
	for _, properties := range []expr.Properties{
		{RowDependent: true}, {StageDependent: true}, {Volatility: expr.Volatility(1)},
	} {
		original := preflightCast(t, "SELECT CAST(int4('42'::text) AS text) FROM t").(coercionExpression)
		original.Property = properties
		folded, changed, err := preflightCoercions(&EvalContext{Context: context.Background()}, original)
		if err != nil || !changed {
			t.Fatal(folded, changed, err)
		}
		parent, ok := folded.(coercionExpression)
		if !ok || parent.Info != original.Info || parent.mode != original.mode {
			t.Fatal(folded)
		}
		if _, ok := parent.child.(literalExpression); !ok {
			t.Fatal("independent child was not folded")
		}
		if _, ok := original.child.(coercionExpression); !ok {
			t.Fatal("original parent mutated")
		}
	}
}

func TestCastCoercionQueryPreflightSharing(t *testing.T) {
	evaluation := EvalContext{Context: context.Background()}
	source := &sourceNode{}
	unchanged := preflightCast(t, "SELECT $1::text FROM t")
	project := &projectNode{child: source, expressions: []expr.Expr{unchanged}}
	root, err := preflightQuery(&evaluation, project)
	if err != nil || root != project {
		t.Fatal(root, err)
	}
	constant := preflightCast(t, "SELECT int4('42'::text) FROM t")
	filter := &filterNode{child: source, predicate: comparisonExpression{left: unchanged, right: constant}}
	sort := &sortNode{child: filter, keys: []sortKey{{expression: unchanged, slot: 3, cache: -1, desc: true}}}
	project.child = sort
	for range 2 {
		root, err = preflightQuery(&evaluation, project)
		if err != nil || root == project {
			t.Fatal(root, err)
		}
		private := root.(*projectNode)
		privateSort := private.child.(*sortNode)
		privateFilter := privateSort.child.(*filterNode)
		if privateSort == sort || privateFilter == filter || privateFilter.child != source || privateSort.keys[0].slot != sort.keys[0].slot || privateSort.keys[0].desc != sort.keys[0].desc || &private.expressions[0] != &project.expressions[0] {
			t.Fatal("changed path not private or unchanged path not shared")
		}
		if _, ok := privateFilter.predicate.(comparisonExpression).right.(literalExpression); !ok {
			t.Fatal(privateFilter.predicate)
		}
		if _, ok := filter.predicate.(comparisonExpression).right.(coercionExpression); !ok {
			t.Fatal("original query mutated")
		}
	}
	project.expressions = []expr.Expr{constant, unchanged}
	root, err = preflightQuery(&evaluation, project)
	if err != nil || &root.(*projectNode).expressions[0] == &project.expressions[0] {
		t.Fatal("changed expressions not copied", err)
	}
}

func TestCastCoercionPreflightImportedFrames(t *testing.T) {
	for _, query := range []string{
		"SELECT int4($1::text) FROM t",
		"SELECT $1::int8::int4 FROM t",
	} {
		t.Run(query, func(t *testing.T) {
			original := preflightCast(t, query).(coercionExpression)
			source := original.child.ResultUsage().Type
			identity := original.child
			var wg sync.WaitGroup
			for _, input := range []string{"42", "", "oops", "17", "-2147483648", "2147483647", "2147483648"} {
				// Build typed imported frames before concurrent evaluation.
				value := sqltype.Null(source)
				if input != "" {
					var err error
					value, err = source.Input(context.Background(), input, -1)
					if err != nil {
						if source.ID() == sqltype.Int8 && input == "oops" {
							continue // Invalid INT8 import is not conversion-owned.
						}
						t.Fatal(err)
					}
				}
				wg.Add(1)
				go func(input string, value sqltype.Value) {
					defer wg.Done()
					for range 3 {
						evaluation := EvalContext{Context: context.Background(), Parameters: []sqltype.Value{value}}
						leaf, changed, err := preflightCoercions(&evaluation, identity)
						if err != nil || changed || !reflect.DeepEqual(leaf, identity) {
							t.Error("parameter/identity leaf materialized", leaf, changed, err)
							return
						}
						root, changed, err := preflightCoercions(&evaluation, original)
						if input == "oops" || input == "2147483648" {
							code := "22P02"
							if input == "2147483648" {
								code = "22003"
							}
							var diagnostic *sqlstate.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Position != "" || diagnostic.Context != "" {
								t.Error("conversion diagnostic", err, code)
							}
							continue
						}
						if err != nil || !changed {
							t.Error(root, changed, err)
							return
						}
						literal, ok := root.(literalExpression)
						if !ok || literal.Info != original.Info || !literal.Properties().ParameterDependent {
							t.Error("replacement changed analyzed information", root)
							return
						}
						// A folded result owns its value, not the caller's frame.
						evaluation.Parameters[0] = sqltype.Null(source)
						got, err := literal.Eval(&evaluation)
						want, wantErr := original.Eval(&EvalContext{Context: context.Background(), Parameters: []sqltype.Value{value}})
						if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
							t.Error(got, want, err, wantErr)
						}
					}
				}(input, value)
			}
			wg.Wait()
			if !reflect.DeepEqual(original.child, identity) {
				t.Fatal("analysis mutated")
			}
			wrong := sqltype.Null(original.ResultUsage().Type)
			_, _, err := preflightCoercions(&EvalContext{Context: context.Background(), Parameters: []sqltype.Value{wrong}}, original)
			if err == nil || err.Error() != "stuntgres: invalid expression parameter type" {
				t.Fatal(err)
			}
		})
	}
}

func TestCastCoercionPreflightFoldedChildReuse(t *testing.T) {
	original := preflightCast(t, "SELECT CAST(int4('42'::text) AS text) FROM t").(coercionExpression)
	inner := original.child.(coercionExpression)
	calls := 0
	inner.child = queryProbe(inner.child.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls != 1 {
			return sqltype.Value{}, errors.New("folded child evaluated again")
		}
		return inner.child.ResultUsage().Type.Input(c.Context, "42", -1)
	})
	original.child = inner
	_, changed, err := preflightCoercions(&EvalContext{Context: context.Background()}, original)
	if err != nil || !changed || calls != 1 {
		t.Fatal(changed, calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	inner.child = queryProbe(inner.child.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		value, err := inner.child.ResultUsage().Type.Input(c.Context, "42", -1)
		cancel()
		return value, err
	})
	_, _, err = preflightCoercions(&EvalContext{Context: ctx}, inner)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCastCoercionPreflightProviderCompletionCancellation(t *testing.T) {
	original := preflightCast(t, "SELECT $1::int8::int4 FROM t").(coercionExpression)
	value, err := sqltype.Integer(original.child.ResultUsage().Type, 42)
	if err != nil {
		t.Fatal(err)
	}
	// Entry, child traversal, parameter evaluation and Apply succeed. The
	// completion checkpoint must still prevent a private result escaping.
	ctx := &queryCountingContext{Context: context.Background(), remaining: 5}
	root, changed, err := preflightCoercions(&EvalContext{Context: ctx, Parameters: []sqltype.Value{value}}, original)
	if root != nil || changed || !errors.Is(err, context.Canceled) {
		t.Fatal(root, changed, err)
	}
}

func TestCastCoercionQueryPreflightFrameMetadataAndFinalization(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT int4($1::text) FROM t")
	project := query.Root.(*projectNode)
	original := project.expressions[0].(coercionExpression)
	schema := []ColumnDescriptor{{Name: "int4", Usage: sqltype.Usage{Type: testUsage(e, sqltype.Int4).Type, Typmod: -1}, Nullable: false, NullabilityKnown: false, OriginTable: 0, OriginAttribute: 0}}
	if !reflect.DeepEqual(project.Schema(), schema) || uint32(schema[0].Usage.Type.ID()) != 23 {
		t.Fatalf("analyzed schema %#v want %#v", project.Schema(), schema)
	}
	for _, input := range []string{"42", "", "oops", "17"} {
		value := sqltype.Null(original.child.ResultUsage().Type)
		if input != "" {
			var err error
			value, err = original.child.ResultUsage().Type.Input(context.Background(), input, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		root, err := preflightQuery(&EvalContext{Context: context.Background(), Parameters: []sqltype.Value{value}}, project)
		if !reflect.DeepEqual(project.expressions[0], original) || !reflect.DeepEqual(project.Schema(), schema) {
			t.Fatal("analyzed query mutated")
		}
		if input == "oops" {
			state(t, err, "22P02")
			if root != nil {
				t.Fatal("failed preflight exposed a private root")
			}
			continue
		}
		if err != nil || root == project || root.(*projectNode).child != project.child || !reflect.DeepEqual(root.Schema(), schema) {
			t.Fatal("private frame changed source/schema", root, err)
		}
		literal, ok := root.(*projectNode).expressions[0].(literalExpression)
		if !ok {
			t.Fatal("eligible conversion was not privately replaced")
		}
		got, err := literal.Eval(&EvalContext{Context: context.Background()})
		want := sqltype.Null(schema[0].Usage.Type)
		if input != "" {
			number := int64(42)
			if input == "17" {
				number = 17
			}
			want, _ = sqltype.Integer(schema[0].Usage.Type, number)
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
	// A probe inside the real conversion observes exactly one evaluation:
	// finalization must consume the preflighted root, not materialize again.
	calls := 0
	private := *project
	original.child = queryProbe(original.child.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		return original.child.ResultUsage().Type.Input(c.Context, "42", -1)
	})
	private.expressions = []expr.Expr{original}
	root, err := preflightQuery(&EvalContext{Context: context.Background()}, &private)
	if err != nil {
		t.Fatal(err)
	}
	rows, count, err := finalizeQuery(context.Background(), root, source, nil)
	if err != nil || rows == nil || count != 4 || calls != 1 || !reflect.DeepEqual(rows.Schema(), schema) {
		t.Fatal(rows, count, calls, err)
	}
	rows.Close()
}

func TestCastExpressionEqualityBoundDiagnosticOwnership(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT4,i INT4)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, message, hint, position string
	}{
		{"SELECT id FROM t WHERE i=int8(TRUE)", "function int8(boolean) does not exist", functionCastHint, "26"},
		{"SELECT id FROM t WHERE i=TRUE::bool", "operator does not exist: integer = boolean", "No operator matches the given name and argument types. You might need to add explicit type casts.", "25"},
	} {
		ast := statement(t, tc.query)
		ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != tc.message || diagnostic.Hint != tc.hint || diagnostic.Position != tc.position {
			t.Fatal(tc.query, diagnostic, err)
		}
	}
}

func TestCastCoercionPreflightIntegratedParents(t *testing.T) {
	evaluation := EvalContext{Context: context.Background()}
	for _, query := range []string{
		"SELECT int4('42'::text)>1 FROM t",
		"SELECT bool(int4('42'::text)) AND TRUE FROM t",
		"SELECT NOT bool(int4('42'::text)) FROM t",
		"SELECT num_nulls(int4('42'::text),NULL) FROM t",
		"SELECT int4('42'::text) IS NULL FROM t",
		"SELECT bool(int4('42'::text)) IS TRUE FROM t",
		"SELECT int4('42'::text) IS DISTINCT FROM 0 FROM t",
		"SELECT 0 IS NOT DISTINCT FROM int4('42'::text) FROM t",
		"SELECT int4('42'::text) IS DISTINCT FROM NULL FROM t",
	} {
		t.Run(query, func(t *testing.T) {
			original := preflightCast(t, query)
			folded, changed, err := preflightCoercions(&evaluation, original)
			if err != nil || !changed || folded.ResultUsage() != original.ResultUsage() || folded.Properties() != original.Properties() {
				t.Fatal(folded, changed, err)
			}
			again, changed, err := preflightCoercions(&evaluation, folded)
			if err != nil || changed {
				t.Fatal(again, changed, err)
			}
			_, changed, err = preflightCoercions(&evaluation, original)
			if err != nil || !changed {
				t.Fatal("analyzed tree mutated", changed, err)
			}
			want, err := original.Eval(&evaluation)
			if err != nil {
				t.Fatal(err)
			}
			got, err := folded.Eval(&evaluation)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(got, want, err)
			}
		})
	}
	singleton := &singletonNode{}
	root, err := preflightQuery(&evaluation, singleton)
	if err != nil || root != singleton {
		t.Fatal("singleton preflight changed source", root, err)
	}
}
