package enginetests_test

import (
	"context"
	"maps"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTypeDescribeComposedStoredOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE td_chain_source(v text)")
	enginetest.SessionSQL(t, s, "CREATE VIEW td_chain_view AS SELECT pg_typeof('x'::unknown::anyelement::unknown) AS raw,pg_typeof(v::unknown::anycompatible::unknown) AS io FROM td_chain_source")
	view := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "td_chain_view")
	definition := stored.ViewOf(relation)
	root := definition.Query().Root.(*plan.ProjectNode)
	if len(root.Expressions) != 2 || len(definition.CastUsages()) != 6 {
		t.Fatal("not every original cast captured")
	}
	if err := definition.Validate(view, relation); err != nil {
		t.Fatal(err)
	}
	for i, node := range root.Expressions {
		composite := node.(expr.Composite)
		children := composite.Children()
		mapped, changed, err := stored.RetagTypes(view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed {
			t.Fatal("identity retag", err)
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			literal := children[0].(plan.LiteralExpr)
			marker := literal.UnknownInput
			if marker == nil || len(marker.Chain) != 3 {
				t.Fatal("raw chain was not retained on one literal")
			}
			for _, candidate := range []expr.Expr{mapped, remapped} {
				retained := candidate.(expr.Composite).Children()[0].(plan.LiteralExpr).UnknownInput
				if retained != marker || &retained.Chain[0] != &marker.Chain[0] {
					t.Fatal("identity operation replaced raw backing")
				}
			}
			for index := range marker.Chain {
				missing := marker.Chain[index].Target
				if _, _, err := stored.RetagTypes(view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
					if d == missing {
						return nil
					}
					return d
				}); err == nil {
					t.Fatal("missing inner raw remap accepted")
				}
				bad := literal
				m := *marker
				m.Chain = append([]expr.TransparentInputCast(nil), marker.Chain...)
				foreign := *m.Chain[index].Target
				m.Chain[index].Target = &foreign
				bad.UnknownInput = &m
				if _, err := composite.Rebuild(expr.InfoOf(node), []expr.Expr{bad}); err == nil {
					t.Fatal("foreign raw generation rebuilt")
				}
				if _, err := stored.ExpressionDependRefs(context.Background(), view, bad); err == nil {
					t.Fatal("foreign inner raw target acquired dependency authority")
				}
			}
			if literal.UnknownInput != marker {
				t.Fatal("rejection changed raw backing")
			}
		} else {
			transport := children[0].(plan.CoercionExpr)
			marker := transport.UnknownInputCast
			if marker == nil || len(marker.Transparent) != 2 {
				t.Fatal("IO no-ops were not retained beside one coercion")
			}
			for _, candidate := range []expr.Expr{mapped, remapped} {
				retained := candidate.(expr.Composite).Children()[0].(plan.CoercionExpr).UnknownInputCast
				if retained != marker || &retained.Transparent[0] != &marker.Transparent[0] {
					t.Fatal("identity operation replaced IO backing")
				}
			}
			for index := range marker.Transparent {
				bad := transport
				m := *marker
				m.Transparent = append([]expr.TransparentInputCast(nil), marker.Transparent...)
				foreign := *m.Transparent[index].Target
				missing := marker.Transparent[index].Target
				if _, _, err := stored.RetagTypes(view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
					if d == missing {
						return nil
					}
					return d
				}); err == nil {
					t.Fatal("missing inner IO remap accepted")
				}
				m.Transparent[index].Target = &foreign
				bad.UnknownInputCast = &m
				if _, err := composite.Rebuild(expr.InfoOf(node), []expr.Expr{bad}); err == nil {
					t.Fatal("foreign IO generation rebuilt")
				}
				if _, err := stored.ExpressionDependRefs(context.Background(), view, bad); err == nil {
					t.Fatal("foreign inner IO target acquired dependency authority")
				}
			}
			if transport.UnknownInputCast != marker {
				t.Fatal("rejection changed IO backing")
			}
		}
	}
	// Every frozen ordinal keeps its exact descriptor, independently of whether
	// presentation chooses to elide that authenticated no-op.
	for path, usage := range definition.CastUsages() {
		foreign := *usage.Type
		rejected := definition.With(func(parts *stored.ViewParts) {
			parts.CastUsages = maps.Clone(parts.CastUsages)
			usage.Type = &foreign
			parts.CastUsages[path] = usage
		})
		if err := rejected.Validate(view, relation); err == nil {
			t.Fatal("foreign frozen step published", path)
		}
		if definition.Query().Root != root || definition.CastUsages()[path].Type == &foreign {
			t.Fatal("rejected publication mutated accepted graph")
		}
	}
	for _, display := range []bool{false, true} {
		if _, err := stored.ViewDefinitionSQL(context.Background(), view, relation, stored.Presentation{Deparse: display}); err != nil {
			t.Fatal("frozen composed render", err)
		}
	}
	for path, literal := range definition.Literals() {
		if literal.UnknownInput == nil {
			continue
		}
		marker := literal.UnknownInput
		rejected := definition.With(func(parts *stored.ViewParts) {
			parts.Literals = maps.Clone(parts.Literals)
			m := *marker
			m.Chain = append([]expr.TransparentInputCast(nil), marker.Chain...)
			m.Chain[0], m.Chain[1] = m.Chain[1], m.Chain[0]
			literal.UnknownInput = &m
			parts.Literals[path] = literal
		})
		if err := rejected.ValidateLiterals(view); err == nil {
			t.Fatal("reordered chain published")
		}
		if definition.Literals()[path].UnknownInput != marker {
			t.Fatal("rejected frozen chain mutated accepted marker")
		}
	}
}

func TestTypeDescribeFrozenCastOccurrence(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE td_occurrence_source(v text)")
	enginetest.SessionSQL(t, s, "CREATE VIEW td_occurrence_view AS SELECT pg_typeof(v::unknown) AS io,pg_typeof(v::text) AS concrete,pg_typeof(v::unknown::anyelement::unknown) AS chain,pg_typeof('x'::unknown::anycompatible::unknown) AS raw,pg_typeof(v::unknown::anyelement::unknown::text) AS io_exit,pg_typeof('x'::unknown::anycompatible::unknown::text) AS raw_exit FROM td_occurrence_source")
	enginetest.SessionSQL(t, s, "CREATE POLICY td_occurrence_policy ON td_occurrence_source USING (pg_typeof(v::unknown)::oid=705 AND pg_typeof(v::text)::oid=25 AND pg_typeof(v::unknown::anyelement::unknown)::oid=705 AND pg_typeof('x'::unknown::anycompatible::unknown)::oid=705 AND pg_typeof(v::unknown::anyelement::unknown::text)::oid=25 AND pg_typeof('x'::unknown::anycompatible::unknown::text)::oid=25)")
	read := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "td_occurrence_view")
	owner := preparedFoldStoredRelation(t, e, "td_occurrence_source")
	policy := stored.PolicyOf(owner.Security.Policies[0].Using)
	if policy == nil {
		t.Fatal("missing genuine stored policy")
	}
	for _, tc := range []struct {
		name       string
		definition *stored.View
		validate   func(*stored.View) error
	}{
		{"view", stored.ViewOf(relation), func(candidate *stored.View) error { return candidate.Validate(read, relation) }},
		{"policy", policy.Presentation(), func(candidate *stored.View) error {
			return stored.NewPolicy(policy.Syntax(), policy.Expression(), candidate).Validate(read, owner)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted := tc.definition
			root := accepted.Query().Root
			casts := accepted.CastUsages()
			saved := maps.Clone(casts)
			literals := maps.Clone(accepted.Literals())
			type rawCapture struct {
				marker  *expr.UnknownInput
				steps   []expr.TransparentInputCast
				backing *expr.TransparentInputCast
			}
			var rawCaptures []rawCapture
			for _, literal := range literals {
				if marker := literal.UnknownInput; marker != nil && len(marker.Chain) != 0 {
					rawCaptures = append(rawCaptures, rawCapture{marker, append([]expr.TransparentInputCast(nil), marker.Chain...), &marker.Chain[0]})
				}
			}
			type ioCapture struct {
				marker  *plan.UnknownInputCast
				source  sqltype.Usage
				target  *sqltype.Descriptor
				steps   []expr.TransparentInputCast
				backing *expr.TransparentInputCast
			}
			var ioCaptures []ioCapture
			for _, bound := range root.(*plan.ProjectNode).Expressions {
				plan.WalkExpression(bound, func(node expr.Expr) {
					if carrier, ok := node.(plan.CoercionExpr); ok && carrier.UnknownInputCast != nil {
						marker := carrier.UnknownInputCast
						captured := ioCapture{marker: marker, source: marker.Source, target: marker.Target, steps: append([]expr.TransparentInputCast(nil), marker.Transparent...)}
						if len(marker.Transparent) != 0 {
							captured.backing = &marker.Transparent[0]
						}
						ioCaptures = append(ioCaptures, captured)
					}
				})
			}
			if len(ioCaptures) == 0 {
				t.Fatal("fixture lacks genuine captured IO backing")
			}
			if err := tc.validate(accepted); err != nil {
				t.Fatal("genuine captured occurrence rejected", err)
			}
			var unknown, concrete, outer int
			for path, usage := range casts {
				replacements := []sqltype.ID{sqltype.Text, sqltype.Unknown, 2276, 2283, 2776, 5077, 5079}
				switch usage.Type.ID() {
				case sqltype.Unknown:
					unknown++
				case sqltype.Text:
					concrete++
					replacements = []sqltype.ID{sqltype.Unknown}
				case 2283, 5077:
					outer++
				default:
					continue
				}
				for _, id := range replacements {
					replacement, ok := catalog.DefinitionTypeAt(read, nil, id)
					if !ok {
						t.Fatal("missing canonical replacement", id)
					}
					if usage == replacement.Usage() {
						continue
					}
					rejected := accepted.With(func(parts *stored.ViewParts) {
						parts.CastUsages = maps.Clone(parts.CastUsages)
						parts.CastUsages[path] = replacement.Usage()
					})
					if err := tc.validate(rejected); err == nil {
						t.Fatal("canonical descriptor substituted for another cast occurrence", path, usage.Type.ID(), replacement.ID())
					}
					if accepted.Query().Root != root || !maps.Equal(casts, saved) {
						t.Fatal("rejected publication changed accepted graph or cast map")
					}
					for literalPath, literal := range literals {
						retained := accepted.Literals()[literalPath]
						if retained.UnknownInput != literal.UnknownInput {
							t.Fatal("rejected publication changed accepted marker")
						}
					}
					for _, captured := range rawCaptures {
						if !expr.SameTransparentInputCasts(captured.marker.Chain, captured.steps) ||
							len(captured.marker.Chain) == 0 || &captured.marker.Chain[0] != captured.backing {
							t.Fatal("rejected publication changed accepted raw chain backing")
						}
					}
					for _, captured := range ioCaptures {
						marker := captured.marker
						if marker.Source != captured.source || marker.Target != captured.target || !expr.SameTransparentInputCasts(marker.Transparent, captured.steps) {
							t.Fatal("rejected publication changed accepted IO capture")
						}
						if len(marker.Transparent) != 0 && &marker.Transparent[0] != captured.backing {
							t.Fatal("rejected publication replaced accepted IO chain backing")
						}
					}
				}
			}
			if unknown == 0 || concrete == 0 || outer == 0 {
				t.Fatal("fixture missed a distinguishing occurrence class", unknown, concrete, outer)
			}
			if err := tc.validate(accepted); err != nil {
				t.Fatal("rejection invalidated genuine accepted capture", err)
			}
		})
	}
	for _, display := range []bool{false, true} {
		if _, err := stored.ViewDefinitionSQL(context.Background(), read, relation, stored.Presentation{Deparse: display}); err != nil {
			t.Fatal("genuine occurrence view render", err)
		}
	}
	if _, err := stored.PolicySQL(context.Background(), read, policy); err != nil {
		t.Fatal("genuine occurrence policy render", err)
	}
}

func TestTypeDescribeTextExitStoredOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE td_exit_source(v bpchar(3))")
	enginetest.SessionSQL(t, s, "CREATE VIEW td_exit_view AS SELECT pg_typeof(v::unknown::anyelement::unknown::text) AS io,pg_typeof('x'::unknown::anycompatible::unknown::text) AS raw FROM td_exit_source")
	read := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "td_exit_view")
	definition := stored.ViewOf(relation)
	root := definition.Query().Root.(*plan.ProjectNode)
	if len(root.Expressions) != 2 || len(definition.CastUsages()) != 8 {
		t.Fatal("not every inner/outer cast occurrence captured")
	}
	for i, node := range root.Expressions {
		outer := node.(expr.Composite).Children()[0].(plan.CoercionExpr)
		if outer.UnknownInputCast != nil || outer.ResultUsage().Type.ID() != sqltype.Text {
			t.Fatal("stored outer cast is not concrete TEXT")
		}
		mapped, changed, err := stored.RetagTypes(read, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed {
			t.Fatal("identity retag", changed, err)
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range []expr.Expr{mapped, remapped} {
			retained := candidate.(expr.Composite).Children()[0].(plan.CoercionExpr)
			if retained.UnknownInputCast != nil || retained.Usage != outer.Usage || !retained.Plan.Equal(outer.Plan) {
				t.Fatal("identity mapping erased outer TEXT boundary")
			}
			if i == 0 {
				before := outer.Child.(plan.CoercionExpr)
				after := retained.Child.(plan.CoercionExpr)
				if after.UnknownInputCast != before.UnknownInputCast || !after.Plan.Equal(before.Plan) ||
					&after.UnknownInputCast.Transparent[0] != &before.UnknownInputCast.Transparent[0] {
					t.Fatal("identity mapping replaced inner genuine IO backing")
				}
			} else {
				before := outer.Child.(plan.LiteralExpr).UnknownInput
				after := retained.Child.(plan.LiteralExpr).UnknownInput
				if before == nil || after != before || &after.Chain[0] != &before.Chain[0] {
					t.Fatal("identity mapping replaced inner raw backing")
				}
			}
		}
	}
	for path, usage := range definition.CastUsages() {
		foreign := *usage.Type
		rejected := definition.With(func(parts *stored.ViewParts) {
			parts.CastUsages = maps.Clone(parts.CastUsages)
			usage.Type = &foreign
			parts.CastUsages[path] = usage
		})
		if err := rejected.Validate(read, relation); err == nil {
			t.Fatal("foreign inner/outer cast generation published", path)
		}
		if definition.Query().Root != root || definition.CastUsages()[path].Type == &foreign {
			t.Fatal("rejected publication changed accepted boundary")
		}
	}
	for _, display := range []bool{false, true} {
		if _, err := stored.ViewDefinitionSQL(context.Background(), read, relation, stored.Presentation{Deparse: display}); err != nil {
			t.Fatal("frozen TEXT boundary render", err)
		}
	}
	if err := definition.Validate(read, relation); err != nil {
		t.Fatal("rejection invalidated accepted TEXT exit", err)
	}
}
