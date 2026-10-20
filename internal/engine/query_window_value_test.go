package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowValueMemberSelection(t *testing.T) {
	for _, mode := range []parser.FrameExclusion{parser.ExcludeNoOthers, parser.ExcludeCurrentRow, parser.ExcludeGroup, parser.ExcludeTies} {
		for head := range 6 {
			for tail := range 6 {
				for current := range 5 {
					peerStart, peerEnd := current, current+1
					if current < 2 {
						peerStart, peerEnd = 0, 2
					}
					var admitted []int
					for member := head; member < tail; member++ {
						include := true
						switch mode {
						case parser.ExcludeCurrentRow:
							include = member != current
						case parser.ExcludeGroup:
							include = member < peerStart || member >= peerEnd
						case parser.ExcludeTies:
							include = member == current || member < peerStart || member >= peerEnd
						}
						if include {
							admitted = append(admitted, member)
						}
					}
					for _, reverse := range []bool{false, true} {
						for _, ordinal := range []int64{-1, 0, 1, 2, 3, 4, 5, math.MaxInt64} {
							member, present := windowValueMember(mode, head, tail, current, peerStart, peerEnd, ordinal, reverse)
							wantPresent := ordinal >= 0 && ordinal < int64(len(admitted))
							if present != wantPresent {
								t.Fatal(mode, head, tail, current, ordinal, reverse, present, admitted)
							}
							if present {
								index := int(ordinal)
								if reverse {
									index = len(admitted) - 1 - index
								}
								if member != admitted[index] {
									t.Fatal(mode, head, tail, current, ordinal, reverse, member, admitted[index])
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestQueryWindowValueGraphAndMetadata(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT first_value(v) OVER w,nth_value(v,id) OVER w,nth_value(v,id+1) OVER w FROM t WINDOW w AS(ORDER BY id ROWS CURRENT ROW)")
	defer source.close()
	node := query.Root.(*projectNode).child.(*windowNode)
	for _, column := range query.Root.Schema() {
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || column.Usage.Type.ID() != sqltype.Int4 {
			t.Fatal(column)
		}
	}
	if len(node.calls) != 3 || node.calls[1].ordinal == nil || node.calls[2].ordinal == nil {
		t.Fatal("value/ordinal identity", node.calls)
	}
	binder := &expressionBinder{}
	if binder.sameWindowCall(node.calls[1], node.calls[2]) {
		t.Fatal("different ordinals collapsed")
	}
	call := node.calls[1]
	foundValue, foundOrdinal := false, false
	queryExpressions(node, func(expr expr.Expr) {
		foundValue = foundValue || expr == call.argument
		foundOrdinal = foundOrdinal || expr == call.ordinal
	})
	if !foundValue || !foundOrdinal {
		t.Fatal("child missing from graph", foundValue, foundOrdinal)
	}
	replacement := node.calls[2].ordinal
	next, err := rewriteQueryExpressions(node, func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == call.ordinal {
			return replacement, true, nil
		}
		return expr, false, nil
	})
	if err != nil || next == node || next.(*windowNode).calls[1].ordinal != replacement || node.calls[1].ordinal != call.ordinal {
		t.Fatal("immutable ordinal rewrite", next, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := remapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	remapped := mapped.(*projectNode).child.(*windowNode).calls[1]
	if remapped.ordinal == call.ordinal || remapped.argument == call.argument || remapped.expression == call.expression || remapped.ordinal.ResultUsage().Type != types[sqltype.Int4] || remapped.expression.ResultUsage().Type != types[sqltype.Int4] {
		t.Fatal("child/result descriptor ownership", remapped)
	}
}

func TestQueryWindowValueBindingUnwind(t *testing.T) {
	for _, tc := range []struct{ q, code string }{
		{"SELECT nth_value(first_value(1) OVER(),1::bigint) OVER()", "42883"},
		{"SELECT nth_value(first_value(1) OVER(),1) OVER()", "42P20"},
		{"SELECT first_value(last_value(1) OVER()) FILTER(WHERE TRUE) OVER()", "0A000"},
		{"SELECT nth_value(1,missing) OVER()", "42703"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			b.clause.aggregateClause, b.queryFrame = "SELECT", 1
			seed := statement(t, "SELECT row_number() OVER()").(*parser.Select).Targets[0]
			if _, err := b.bind(seed, expressionPolicy{context: projectionContext}); err != nil {
				t.Fatal(err)
			}
			before := b.grouping.windowCalls[:len(b.grouping.windowCalls):len(b.grouping.windowCalls)]
			b.grouping.windowCalls = before
			values := append([]windowCall(nil), before...)
			_, err := b.bind(statement(t, tc.q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code {
				t.Fatal(err)
			}
			if len(b.grouping.windowCalls) != len(before) || cap(b.grouping.windowCalls) != cap(before) || &b.grouping.windowCalls[0] != &before[0] || !reflect.DeepEqual(values, b.grouping.windowCalls) || b.grouping.argumentOwner != (argumentWindowOwner{}) {
				t.Fatal("failed call changed registration/owner", b.grouping.windowCalls, b.grouping.argumentOwner)
			}
		})
	}
}

func TestQueryWindowValueEvaluationCancellation(t *testing.T) {
	for _, phase := range []string{"ordinal", "value"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT nth_value(v,id) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t")
			node := query.Root.(*projectNode).child.(*windowNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			call := &node.calls[0]
			original := call.ordinal
			if phase == "value" {
				original = call.argument
			}
			entered := false
			probe := queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				value, err := original.Eval(c)
				entered = true
				cancel()
				return value, err
			})
			if phase == "value" {
				call.argument = probe
			} else {
				call.ordinal = probe
			}
			rows, count, err := finalizeQuery(ctx, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var d *sqlstate.Error
			if !entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &d) || source.view != nil {
				t.Fatal("canceled value window published/leaked", entered, count, err, source.view)
			}
		})
	}
}

func TestQueryWindowValueDomainRemap(t *testing.T) {
	e := New()
	run(t, e, "CREATE DOMAIN window_value_domain AS INT")
	run(t, e, "CREATE TABLE t(id INT)")
	ast := statement(t, "SELECT nth_value(id::window_value_domain,id::window_value_domain) OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE TIES) FROM t")
	ref := roleTableRef(t, e, "t")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	description, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.query.Root
	original := root.(*projectNode).child.(*windowNode).calls[0]
	domain := original.argument.ResultUsage().Type
	if !domain.IsDomain() || original.expression.ResultUsage().Type != domain {
		t.Fatal("polymorphic domain lost", original)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	types[domain.ID()], err = sqltype.Domain(domain.ID(), domain.DiagnosticName(), domain.SQLName(), types[domain.DomainBase().Type.ID()].Usage())
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] }
	mapped, err := remapViewQuery(New().CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	node := mapped.(*projectNode).child.(*windowNode)
	next := node.calls[0]
	var ordinalDomain *sqltype.Descriptor
	walkBoundExpression(next.ordinal, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			ordinalDomain = expr.ResultUsage().Type
		}
	})
	if next.expression.ResultUsage().Type != types[domain.ID()] || next.argument.ResultUsage().Type != types[domain.ID()] || ordinalDomain != types[domain.ID()] || ordinalDomain == domain || !queryDescriptorsChanged(root, lookup) || queryDescriptorsChanged(mapped, lookup) || node.specs[0].frame.exclusion != parser.ExcludeTies {
		t.Fatal("value/ordinal/result ownership", next, ordinalDomain)
	}
}

func TestQueryWindowValueCurrentOrdinalAndSelectedEvaluation(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT nth_value(v,CASE WHEN id%2=1 THEN 1 ELSE 2 END) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t")
	node := query.Root.(*projectNode).child.(*windowNode)
	call := &node.calls[0]
	argument, ordinal := call.argument, call.ordinal
	var current, selected []int64
	call.ordinal = queryProbe(ordinal.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		current = append(current, id)
		return ordinal.Eval(c)
	})
	call.argument = queryProbe(argument.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		selected = append(selected, id)
		return argument.Eval(c)
	})
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if count != 4 || !reflect.DeepEqual(current, []int64{1, 2, 3, 4}) || !reflect.DeepEqual(selected, []int64{1, 2, 1, 2}) {
		t.Fatal("current/selected context or repeated-value cache", count, current, selected)
	}
	values := make([]sqltype.Value, 1)
	for i := range 4 {
		present, err := rows.Next(context.Background(), values)
		if !present || err != nil || values[0].IsNull() != (i%2 == 1) {
			t.Fatal(i, present, values, err)
		}
		if i%2 == 0 {
			number, err := values[0].Integer()
			if err != nil || number != 1 {
				t.Fatal(number, err)
			}
		}
	}
}

func TestQueryWindowValueSortOwnerSlots(t *testing.T) {
	for _, tc := range []struct {
		order string
		calls int
		same  bool
	}{
		{" ORDER BY a", 1, true}, {" ORDER BY b", 1, true},
		{" ORDER BY id", 2, false}, {" ORDER BY a,b", 2, false},
		{"", 2, false},
	} {
		t.Run(tc.order, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT first_value(nextval($1)) OVER w AS a,first_value(nextval($1)) OVER w AS b FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)"+tc.order)
			defer source.close()
			project := query.Root.(*projectNode)
			child := project.child
			if sorted, ok := child.(*sortNode); ok {
				child = sorted.child
				if tc.order == " ORDER BY a,b" && (sorted.keys[0].cache == sorted.keys[1].cache || sorted.keys[0].targetIdentity == sorted.keys[1].targetIdentity) {
					t.Fatal("required target caches collapsed", sorted.keys)
				}
			}
			node := child.(*windowNode)
			if len(node.calls) != tc.calls {
				t.Fatal("executing target owners", len(node.calls), tc.calls)
			}
			if tc.order == " ORDER BY b" && node.calls[0].expression.call != 1 {
				t.Fatal("later demanded ordinal did not retain its own executing slot", node.calls[0].expression)
			}
			first, second := windowOccurrence(project.expressions[0]), windowOccurrence(project.expressions[1])
			// Demanded projections may read ORDER caches instead of a leaf.
			if first != nil && second != nil && (first.slot == second.slot) != tc.same {
				t.Fatal("slot ownership", first, second)
			}
		})
	}
	t.Run("retained-wrapper-is-not-a-leaf-owner", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT first_value(nextval($1)) OVER w+1 AS a,first_value(nextval($1)) OVER w AS b FROM t WINDOW w AS(ORDER BY id) ORDER BY a")
		defer source.close()
		node := query.Root.(*projectNode).child.(*sortNode).child.(*windowNode)
		if len(node.calls) != 2 || node.calls[0].expression.slot == node.calls[1].expression.slot {
			t.Fatal("retained wrapper donated nested leaf", node.calls)
		}
	})
}

func TestQueryWindowValueOutputPublicationCancellation(t *testing.T) {
	for _, phase := range []string{"output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT nth_value(v,id) OVER w,count(*) OVER w FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW)")
			continuation, err := captureQuerySource(context.Background(), source.view, source.ref)
			if err != nil {
				t.Fatal(err)
			}
			defer continuation.close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &windowCancelContext{Context: ctx, cancel: cancel, phase: phase}
			rows, count, err := finalizeQuery(probe, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var diagnostic *sqlstate.Error
			if !probe.entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) || source.view != nil {
				t.Fatal("value output/Finish cancellation", probe.entered, count, err, source.view)
			}
			recovered, count, err := finalizeQuery(context.Background(), query.Root, continuation, nil)
			if err != nil || recovered == nil || count != 4 {
				t.Fatal("same bound graph recovery", count, err)
			}
			recovered.Close()
		})
	}
}

func TestQueryWindowValueHiddenSortOwner(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT first_value(nextval($1)) OVER w+1 AS a FROM t WINDOW w AS(ORDER BY id) ORDER BY first_value(nextval($1)) OVER w")
	defer source.close()
	project := query.Root.(*projectNode)
	node := project.child.(*sortNode).child.(*windowNode)
	if len(project.Schema()) != 1 || len(node.calls) != 1 || node.calls[0].expression.call != 1 {
		t.Fatal("hidden whole owner lost or leaked into public schema", project.Schema(), node.calls)
	}
}
