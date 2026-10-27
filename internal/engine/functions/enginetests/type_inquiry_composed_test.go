package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTypeDescribeComposedProtocol(t *testing.T) {
	ctx := context.Background()
	for _, selected := range []bool{false, true} {
		e := engine.New()
		if selected {
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
		}
		bind := func(source string) (expr.Expr, error) {
			b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			raw := enginetest.Statement(t, "SELECT "+source).(*parser.Select).Targets[0]
			return b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		}
		for _, source := range []string{
			"'x'::unknown::anyelement::unknown", "NULL::anycompatible::unknown::unknown",
			"'x'::text::unknown::anyelement::unknown", "'x'::bpchar(4)::unknown::unknown",
		} {
			node, err := bind("pg_catalog.pg_typeof(" + source + ")")
			if err != nil {
				t.Fatal(selected, source, err)
			}
			n := node.(functions.TypeInquiry)
			if err := n.Validate(); err != nil {
				t.Fatal(err)
			}
			if len(n.Children()) != 1 || n.Children()[0].ResultUsage().Type.ID() != sqltype.Text {
				t.Fatal("not one concrete transport")
			}
			ioCount, literalCount := 0, 0
			expr.Walk(n, plan.ExpressionChildren, func(child expr.Expr) {
				if c, ok := child.(plan.CoercionExpr); ok && c.UnknownInputCast != nil {
					ioCount++
				}
				if _, ok := child.(plan.LiteralExpr); ok {
					literalCount++
				}
			})
			if literalCount != 1 || ioCount > 1 {
				t.Fatal("duplicated producer or IO", literalCount, ioCount)
			}
			mapped, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || changed {
				t.Fatal("identity retag", err)
			}
			child := n.Children()[0]
			if literal, ok := child.(plan.LiteralExpr); ok {
				marker := literal.UnknownInput
				if marker == nil || len(marker.Chain) != 3 {
					t.Fatal("lost ordered raw chain")
				}
				retained := mapped.(functions.TypeInquiry).Children()[0].(plan.LiteralExpr).UnknownInput
				if retained != marker || &retained.Chain[0] != &marker.Chain[0] {
					t.Fatal("identity retag copied accepted chain")
				}
				for i := range marker.Chain {
					bad := literal
					m := *marker
					m.Chain = append([]expr.TransparentInputCast(nil), marker.Chain...)
					m.Chain[i].Syntax.Child = parser.LiteralExpr{}
					bad.UnknownInput = &m
					if _, err := n.Rebuild(n.Info, []expr.Expr{bad}); err == nil {
						t.Fatal("spoofed raw chain rebuilt")
					}
					bad = literal
					m = *marker
					m.Chain = append([]expr.TransparentInputCast(nil), marker.Chain...)
					foreign := *m.Chain[i].Target
					m.Chain[i].Target = &foreign
					bad.UnknownInput = &m
					if _, err := n.Rebuild(n.Info, []expr.Expr{bad}); err == nil {
						t.Fatal("unauthorized raw chain generation rebuilt")
					}
					if _, _, err := n.Retag(n.Info, []expr.Expr{bad}, true, expr.Retag{
						Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d },
					}); err == nil {
						t.Fatal("identity mapper authorized foreign raw chain generation")
					}
				}
				if source == "'x'::unknown::anyelement::unknown" {
					inner := marker.Chain[1].Target
					replacement := *inner
					mapInner := func(d *sqltype.Descriptor) *sqltype.Descriptor {
						if d == inner {
							return &replacement
						}
						return d
					}
					retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, mapInner)
					if err != nil || !changed {
						t.Fatal("authorized inner-only generation retag", changed, err)
					}
					next := retagged.(functions.TypeInquiry)
					transport := next.Children()[0].(plan.LiteralExpr)
					accepted := transport.UnknownInput
					if inner.ID() != 2283 || inner == &replacement || !reflect.DeepEqual(inner, &replacement) ||
						!reflect.DeepEqual(transport, literal) {
						t.Fatal("inner replacement did not distinguish identity from structural equality")
					}
					if accepted == marker || &accepted.Chain[0] == &marker.Chain[0] ||
						accepted.CastType != marker.CastType || transport.Usage != literal.Usage ||
						next.ResultUsage() != n.ResultUsage() || next.RoutineReference != n.RoutineReference {
						t.Fatal("inner retag changed outer generations or retained old chain backing")
					}
					for i, step := range marker.Chain {
						if accepted.Chain[i].Target != mapInner(step.Target) ||
							!reflect.DeepEqual(accepted.Chain[i].Syntax, step.Syntax) {
							t.Fatal("inner retag did not map exact chain", i)
						}
					}
					if err := next.Validate(); err != nil {
						t.Fatal("mapped generation invalid", err)
					}
					evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
					value, err := next.Eval(&evaluation)
					oid, oidErr := value.TypeOID()
					if err != nil || oidErr != nil || oid != 705 {
						t.Fatal("inner retag lost logical UNKNOWN", err, oidErr, oid)
					}
					identity, changed, err := stored.RetagTypes(e.CatalogRead(), next, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
					if err != nil || changed ||
						identity.(functions.TypeInquiry).Children()[0].(plan.LiteralExpr).UnknownInput != accepted {
						t.Fatal("mapped generation identity retag copied backing", changed, err)
					}
					if _, _, err := n.Retag(n.Info, []expr.Expr{literal}, true, expr.Retag{Type: mapInner}); err == nil {
						t.Fatal("authorized mapper accepted unmapped candidate chain")
					}
					if _, _, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
						if d == inner {
							return nil
						}
						return d
					}); err == nil {
						t.Fatal("missing inner generation mapping accepted")
					}
					if literal.UnknownInput != marker || marker.Chain[1].Target != inner ||
						retained != marker || &retained.Chain[0] != &marker.Chain[0] || n.Validate() != nil {
						t.Fatal("inner retag mutated original accepted generation")
					}
				}
				if literal.UnknownInput != marker || marker.Validate(literal.Usage, literal.Value) != nil {
					t.Fatal("rejection mutated accepted raw carrier")
				}
			} else {
				transport := child.(plan.CoercionExpr)
				marker := transport.UnknownInputCast
				retained := mapped.(functions.TypeInquiry).Children()[0].(plan.CoercionExpr).UnknownInputCast
				if retained != marker || len(marker.Transparent) == 0 || &retained.Transparent[0] != &marker.Transparent[0] {
					t.Fatal("identity retag copied IO chain")
				}
				for _, mutate := range []func(*plan.UnknownInputCast){
					func(m *plan.UnknownInputCast) { m.Transparent[0].Syntax.Child = parser.LiteralExpr{} },
					func(m *plan.UnknownInputCast) { m.Transparent = nil },
					func(m *plan.UnknownInputCast) { d := *m.Transparent[0].Target; m.Transparent[0].Target = &d },
				} {
					bad := transport
					m := *marker
					m.Transparent = append([]expr.TransparentInputCast(nil), marker.Transparent...)
					mutate(&m)
					bad.UnknownInputCast = &m
					if _, err := n.Rebuild(n.Info, []expr.Expr{bad}); err == nil {
						t.Fatal("spoofed IO chain rebuilt")
					}
				}
				// Replace only the real source with an observable implementation of
				// the same usage/properties. All no-ops must still demand it once.
				var order []int
				transport.Child = typeDescribeObserved{Info: expr.InfoOf(transport.Child), value: sqltype.String(transport.Child.ResultUsage().Type, "x"), index: 1, order: &order}
				rebuilt, err := n.Rebuild(n.Info, []expr.Expr{transport})
				if err != nil {
					t.Fatal(err)
				}
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
				v, err := rebuilt.Eval(&evaluation)
				oid, oidErr := v.TypeOID()
				if err != nil || oidErr != nil || oid != 705 || !reflect.DeepEqual(order, []int{1}) {
					t.Fatal("composed producer not once", order, err, oidErr)
				}
				cause := errors.New("real composed producer failure")
				order = nil
				transport.Child = typeDescribeObserved{Info: expr.InfoOf(transport.Child), index: 1, order: &order, failure: cause}
				rebuilt, err = n.Rebuild(n.Info, []expr.Expr{transport})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := rebuilt.Eval(&evaluation); !errors.Is(err, cause) || !reflect.DeepEqual(order, []int{1}) {
					t.Fatal("composed child failure lost", err, order)
				}
				if transport.UnknownInputCast != marker || n.Validate() != nil {
					t.Fatal("rejected publication mutated IO backing")
				}
			}
		}
		for _, source := range []string{"'x'::unknown::anyelement::unknown", "NULL::anycompatible::unknown", "'x'::text::unknown::unknown"} {
			for _, args := range []string{source + ",1", source + "," + source + ",1"} {
				_, err := bind("pg_typeof(" + args + ")")
				d, ok := err.(*sqlstate.Error)
				want := "function pg_typeof(unknown, integer) does not exist"
				if args != source+",1" {
					want = "function pg_typeof(unknown, unknown, integer) does not exist"
				}
				if !ok || d.Code != "42883" || d.Message != want {
					t.Fatal("logical diagnostic", selected, err)
				}
			}
		}
		_, err := bind("pg_catalog.pg_typeof('x'::unknown::unknown,1::unknown::unknown)")
		if d, ok := err.(*sqlstate.Error); !ok || d.Code != "42846" || d.Message != "cannot cast type integer to unknown" {
			t.Fatal("child cast failure must precede arity", err)
		}
	}
}

func TestTypeDescribeTextExitProtocol(t *testing.T) {
	ctx := context.Background()
	for _, selected := range []bool{false, true} {
		e := engine.New()
		if selected {
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
		}
		bind := func(source string) functions.TypeInquiry {
			t.Helper()
			b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			raw := enginetest.Statement(t, "SELECT "+source).(*parser.Select).Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(selected, source, err)
			}
			return node.(functions.TypeInquiry)
		}
		for _, source := range []string{
			"'x'::unknown::anyelement::unknown::text",
			"NULL::unknown::anycompatible::unknown::text",
			"'x'::bpchar(3)::unknown::anyelement::unknown::text",
		} {
			n := bind("pg_catalog.pg_typeof(" + source + ")")
			outer, ok := n.Args[0].(plan.CoercionExpr)
			if !ok || outer.UnknownInputCast != nil || outer.Mode != sqltype.Explicit ||
				outer.ResultUsage() != outer.Child.ResultUsage() || outer.Plan.IsUnknownInputTransport() {
				t.Fatal("missing ordinary explicit physical-identity TEXT boundary")
			}
			identity, err := sqltype.PlanCoercion(outer.Child.ResultUsage(), outer.Usage, sqltype.Explicit)
			if err != nil || !identity.Equal(outer.Plan) {
				t.Fatal("TEXT exit is not the ordinary identity plan", err)
			}
			logical, err := outer.LogicalInputCast()
			if err != nil || logical.Source != nil || logical.Target.Type != nil {
				t.Fatal("outward UNKNOWN evidence escaped", err)
			}
			rebuilt, err := n.Rebuild(n.Info, n.Children())
			if err != nil || !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
				t.Fatal("rebuild/group lost boundary", err)
			}
			mapped, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || changed {
				t.Fatal("identity retag", changed, err)
			}
			retained := mapped.(functions.TypeInquiry).Args[0].(plan.CoercionExpr)
			switch inner := outer.Child.(type) {
			case plan.LiteralExpr:
				marker := inner.UnknownInput
				if marker == nil || len(marker.Chain) != 3 ||
					retained.Child.(plan.LiteralExpr).UnknownInput != marker {
					t.Fatal("TEXT boundary lost authenticated raw chain backing")
				}
				replacement := *marker.Chain[1].Target
				next, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
					if d == marker.Chain[1].Target {
						return &replacement
					}
					return d
				})
				if err != nil || !changed {
					t.Fatal("authorized inner generation mapping through TEXT", err)
				}
				nextOuter := next.(functions.TypeInquiry).Args[0].(plan.CoercionExpr)
				nextMarker := nextOuter.Child.(plan.LiteralExpr).UnknownInput
				if nextMarker == marker || nextMarker.Chain[1].Target != &replacement ||
					marker.Chain[1].Target == &replacement || nextOuter.UnknownInputCast != nil {
					t.Fatal("mapped raw generation lost boundary or mutated accepted backing")
				}
				bad := inner
				spoof := *marker
				spoof.Chain = append([]expr.TransparentInputCast(nil), marker.Chain...)
				spoof.Chain[1].Target = &replacement
				bad.UnknownInput = &spoof
				if _, err := stored.ExpressionDependRefs(ctx, e.CatalogRead(), bad); err == nil {
					t.Fatal("foreign inner raw generation acquired dependency authority")
				}
			case plan.CoercionExpr:
				marker := inner.UnknownInputCast
				if marker == nil || len(marker.Transparent) != 2 || !inner.Plan.IsUnknownInputTransport() ||
					retained.Child.(plan.CoercionExpr).UnknownInputCast != marker {
					t.Fatal("TEXT boundary lost real IO/chain backing")
				}
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
				value, err := outer.Eval(&evaluation)
				text, textErr := value.Text()
				if err != nil || textErr != nil || text != "x  " || value.Type().ID() != sqltype.Text {
					t.Fatal("TEXT exit replaced bpcharout bytes with trimming conversion", text, err, textErr)
				}
				var order []int
				inner.Child = typeDescribeObserved{Info: expr.InfoOf(inner.Child), value: sqltype.String(inner.Child.ResultUsage().Type, "x  "), index: 1, order: &order}
				outer.Child = inner
				next, err := n.Rebuild(n.Info, []expr.Expr{outer})
				if err != nil {
					t.Fatal(err)
				}
				value, err = next.Eval(&evaluation)
				oid, oidErr := value.TypeOID()
				if err != nil || oidErr != nil || oid != 25 || !reflect.DeepEqual(order, []int{1}) {
					t.Fatal("TEXT exit erased or duplicated producer", oid, order, err, oidErr)
				}
				cause := errors.New("TEXT exit producer failure")
				order = nil
				inner.Child = typeDescribeObserved{Info: expr.InfoOf(inner.Child), index: 1, order: &order, failure: cause}
				outer.Child = inner
				failing, err := n.Rebuild(n.Info, []expr.Expr{outer})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := failing.Eval(&evaluation); !errors.Is(err, cause) || !reflect.DeepEqual(order, []int{1}) {
					t.Fatal("TEXT boundary erased producer failure", err, order)
				}
				spoof := *marker
				foreign := *marker.Target
				spoof.Target = &foreign
				inner.UnknownInputCast = &spoof
				if _, err := stored.ExpressionDependRefs(ctx, e.CatalogRead(), inner); err == nil {
					t.Fatal("foreign IO target acquired dependency authority")
				}
			default:
				t.Fatalf("unexpected inner carrier %T", outer.Child)
			}
			for _, candidate := range []expr.Expr{n, rebuilt, mapped} {
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
				value, err := candidate.Eval(&evaluation)
				oid, oidErr := value.TypeOID()
				if err != nil || oidErr != nil || oid != 25 {
					t.Fatal("TEXT exit did not clear logical705", oid, err, oidErr)
				}
				replay, err := stored.DefaultSQL(ctx, e.CatalogRead(), candidate)
				if err != nil {
					t.Fatal("scalar render", err)
				}
				rebound := bind(replay)
				value, err = rebound.Eval(&evaluation)
				oid, oidErr = value.TypeOID()
				if err != nil || oidErr != nil || oid != 25 {
					t.Fatal("scalar replay erased TEXT boundary", replay, oid, err, oidErr)
				}
			}
		}
		b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT 'x'::text::unknown::text").(*parser.Select).Targets[0]
		if _, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil {
			t.Fatal("selected remedy widened ordinary nil-sink UNKNOWN admission")
		}
	}
}
