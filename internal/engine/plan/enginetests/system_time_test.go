package enginetests_test

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSystemTimePlanTraversal(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
	query, _ := enginetest.QueryFixture(t, e, "SELECT id FROM t TABLESAMPLE system_time(3)")
	project := query.Root.(*plan.ProjectNode)
	source := project.Child.(*plan.SourceNode)
	if source.Sample == nil || source.Sample.Procedure == nil || source.Sample.Handler.Address != source.Sample.Procedure.Address() {
		t.Fatal("missing selected handler", source)
	}
	original := source.Sample
	usage := original.Argument.ResultUsage()
	if usage.Type.ID() != sqltype.Float8 {
		t.Fatal("time argument retained rows-only bigint usage", usage)
	}
	value, err := sqltype.Float(usage.Type, 1.25)
	if err != nil {
		t.Fatal(err)
	}
	replacement := plan.LiteralExpr{Info: expr.Info{Usage: usage, Pos: original.Argument.SourcePosition()}, Value: value}
	visits := 0
	mapped, err := plan.RewriteQueryExpressions(source, func(bound expr.Expr) (expr.Expr, bool, error) { visits++; return replacement, true, nil })
	if err != nil || visits != 1 || mapped == source || !reflect.DeepEqual(mapped.(*plan.SourceNode).Sample.Argument, replacement) || source.Sample != original {
		t.Fatal("count rewrite lost source identity or mutated backing", visits, mapped, err)
	}
	tree := func(root plan.Node) string {
		var text strings.Builder
		if err := plan.AppendQueryTree(context.Background(), &text, root); err != nil {
			t.Fatal(err)
		}
		return text.String()
	}
	if tree(source) == tree(mapped) {
		t.Fatal("different sample count has same analyzed identity")
	}
	other, sample := *source, *original
	sample.Handler.Address.ObjectID++
	other.Sample = &sample
	if tree(source) == tree(&other) {
		t.Fatal("different handler has same analyzed identity")
	}
	seen := 0
	plan.QueryExpressions(source, func(bound expr.Expr) {
		if !reflect.DeepEqual(bound, original.Argument) {
			t.Fatal("wrong source operand")
		}
		seen++
	})
	if seen != 1 {
		t.Fatal("sample count omitted from expression walks", seen)
	}
	remapped, err := stored.RemapViewQuery(e.CatalogRead(), source, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || tree(remapped) != tree(source) || remapped.(*plan.SourceNode).Sample == original || !reflect.DeepEqual(original.Argument, source.Sample.Argument) {
		t.Fatal("stored remap lost or mutated sample", remapped, err)
	}
	t.Run("stored_occurrences", func(t *testing.T) {
		enginetest.Run(t, e, "CREATE VIEW sample_identity AS SELECT id FROM ONLY t AS captured TABLESAMPLE system_time(3)")
		enginetest.Run(t, e, "CREATE VIEW sample_other_count AS SELECT id FROM ONLY t AS captured TABLESAMPLE system_time(1)")
		read := e.CatalogRead()
		relation := read.MustRelation(read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "sample_identity"}))
		other := read.MustRelation(read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: relation.Namespace(), Name: "sample_other_count"}))
		accepted, different := stored.ViewOf(relation), stored.ViewOf(other)
		root, syntax := accepted.Query().Root, accepted.Parts().Syntax
		references := maps.Clone(accepted.Parts().RoutineRefs)
		var handler analyze.ViewLiteralPath
		found := 0
		for path := range references {
			if path.Clause == analyze.ViewSampleHandler {
				handler = path
				found++
			}
		}
		if found != 1 {
			t.Fatal("handler has no unique stored occurrence", references)
		}
		for _, deparse := range []bool{false, true} {
			policy := stored.Presentation{Deparse: deparse, Columns: relation.Columns}
			text, err := accepted.SelectSQL(context.Background(), read, policy)
			if err != nil || !strings.Contains(text, "TABLESAMPLE") {
				t.Fatal("sampling disappeared during rendering", text, err)
			}
			statements, err := parser.Parse(text)
			if err != nil || len(statements) != 1 {
				t.Fatal("sample replay does not parse", text, err)
			}
			tables := parser.FromTables(statements[0].(*parser.Select).From)
			if len(tables) != 1 || !tables[0].Only || tables[0].Sample == nil {
				t.Fatal("sample/ONLY/alias source lost", text)
			}
			otherText, err := different.SelectSQL(context.Background(), read, stored.Presentation{Deparse: deparse, Columns: other.Columns})
			if err != nil || text == otherText {
				t.Fatal("distinct counts rendered as the same source", otherText, err)
			}
		}
		rejected := accepted.With(func(parts *stored.ViewParts) {
			parts.RoutineRefs = maps.Clone(parts.RoutineRefs)
			ref := parts.RoutineRefs[handler]
			parts.RoutineRefs[handler] = new(ids.Domain).Ref(ref.Epoch, ref.Address)
		})
		if err := rejected.Validate(read, relation); err == nil {
			t.Fatal("foreign handler capture acquired publication authority")
		}
		if _, err := rejected.SelectSQL(context.Background(), read, stored.Presentation{}); err == nil {
			t.Fatal("renderer trusted foreign handler capture")
		}
		bigint, _ := read.LookupType(sqltype.Int8)
		integer, err := sqltype.Integer(bigint, 1)
		if err != nil {
			t.Fatal(err)
		}
		wrongArgument := accepted.With(func(parts *stored.ViewParts) {
			query := *parts.Query
			query.Root, err = plan.RewriteQueryExpressions(query.Root, func(bound expr.Expr) (expr.Expr, bool, error) {
				if bound.ResultUsage().Type.ID() != sqltype.Float8 {
					return bound, false, nil
				}
				return plan.LiteralExpr{Info: expr.Info{Usage: bigint.Usage()}, Value: integer}, true, nil
			})
			parts.Query = &query
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := wrongArgument.Validate(read, relation); err == nil {
			t.Fatal("stored time handler accepted rows-only bigint argument")
		}
		if accepted.Query().Root != root || accepted.Parts().Syntax != syntax || !maps.Equal(references, accepted.Parts().RoutineRefs) {
			t.Fatal("rejected capture changed accepted source backing")
		}
		if err := accepted.Validate(read, relation); err != nil {
			t.Fatal("rejection contaminated accepted definition", err)
		}
	})
	t.Run("destination_remap", func(t *testing.T) {
		destination := engine.New()
		enginetest.Run(t, destination, "CREATE TABLE time_oid_padding(n int)")
		enginetest.Run(t, destination, "CREATE EXTENSION tsm_system_time")
		target, _ := enginetest.QueryFixture(t, destination, "SELECT id FROM t TABLESAMPLE system_time(1)")
		targetSource := target.Root.(*plan.ProjectNode).Child.(*plan.SourceNode)
		read := destination.CatalogRead()
		mapper := stored.ViewQueryRemapper{
			Catalog: read,
			Ref: func(ref ids.ObjectRef) ids.ObjectRef {
				switch ref {
				case source.Ref:
					return targetSource.Ref
				case original.Handler:
					return targetSource.Sample.Handler
				default:
					return read.Owner().Domain.Ref(read.Epoch(), ref.Address)
				}
			},
			Type: func(d *sqltype.Descriptor) *sqltype.Descriptor {
				typ, _ := read.LookupType(d.ID())
				return typ
			},
			Procedure: func(procedure *catalog.Procedure) (*catalog.Procedure, error) {
				if procedure == original.Procedure {
					return targetSource.Sample.Procedure, nil
				}
				t.Fatal("unexpected sampling procedure", procedure)
				return nil, nil
			},
		}
		mapped, err := mapper.Query(source)
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(*plan.SourceNode)
		if next.Ref != targetSource.Ref || next.Sample.Handler != targetSource.Sample.Handler || next.Sample.Procedure != targetSource.Sample.Procedure || next.Sample.Argument.ResultUsage().Type != targetSource.Sample.Argument.ResultUsage().Type || original.Handler == next.Sample.Handler {
			t.Fatal("destination lost remapped handler/source/type authority", next)
		}
		if source.Sample != original || source.Sample.Argument.ResultUsage() != usage {
			t.Fatal("destination remap mutated original accepted backing")
		}
	})
	t.Run("correlated_argument", func(t *testing.T) {
		for _, tc := range []struct {
			name, sql string
			local     bool
		}{
			{"ancestor", "SELECT id,(SELECT count(*) FROM t AS sampled TABLESAMPLE system_time(t.v::float8)) AS n FROM t", false},
			{"from_predecessor", "SELECT id,(SELECT count(*) FROM t AS budget CROSS JOIN t AS sampled TABLESAMPLE system_time(budget.v::float8)) AS n FROM t", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				correlated := engine.New()
				enginetest.Run(t, correlated, "CREATE EXTENSION tsm_system_time")
				query, input := enginetest.QueryFixture(t, correlated, tc.sql)
				outer := query.Root.(*plan.ProjectNode)
				findSample := func(root plan.Node) (*plan.TableSample, *plan.JoinNode) {
					var selected *plan.TableSample
					var join *plan.JoinNode
					plan.VisitCTEQuery(root, func(node plan.Node) {
						if source, ok := node.(*plan.SourceNode); ok && source.Sample != nil {
							selected = source.Sample
						}
						if n, ok := node.(*plan.JoinNode); ok {
							join = n
						}
					}, func(expr.Expr) {}, true)
					return selected, join
				}
				selected, join := findSample(query.Root)
				if selected == nil || selected.Argument.ResultUsage().Type.ID() != sqltype.Float8 {
					t.Fatal("correlated time argument lost float8 type", selected)
				}
				var column plan.ColumnExpr
				columns := 0
				analyze.WalkQueryArgumentExpression(selected.Argument, func(bound expr.Expr) {
					if n, ok := bound.(plan.ColumnExpr); ok {
						column = n
						columns++
					}
				})
				origin := input.Ref
				origin.Address.SubID = 2 // t.v, not the sampled source's output.
				if columns != 1 || column.Scope != 1 || column.Slot != 1 || column.Origin != origin || !selected.Argument.Properties().RowDependent {
					t.Fatal("correlated argument lost captured column identity", column, columns)
				}
				if tc.local {
					if len(selected.Captures) != 1 || join == nil || !join.CorrelatedRight || column.Frame != join.Frame || column.Frame == outer.Frame {
						t.Fatal("same-frame sample lost FROM capture ownership", selected.Captures, join)
					}
					capture := selected.Captures[0]
					if capture.Frame != column.Frame || capture.Slot != column.Slot || capture.QueryOccurrence != 0 || capture.Name != "budget" {
						t.Fatal("sample captured the wrong FROM predecessor", capture, column)
					}
				} else if column.Frame != outer.Frame || len(selected.Captures) != 0 {
					// Ancestor columns use the outer evaluation frame, not the
					// same-frame FROM captures consumed by a correlated join.
					t.Fatal("ancestor sample lost outer-frame ownership", column, selected.Captures)
				}
				before := selected.Argument
				captures := append([]plan.FunctionCorrelation(nil), selected.Captures...)
				mapped, err := stored.RemapViewQuery(correlated.CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
				if err != nil || mapped == query.Root || !reflect.DeepEqual(selected.Argument, before) || !reflect.DeepEqual(selected.Captures, captures) {
					t.Fatal("correlated remap mutated original accepted argument/captures", err)
				}
				next, _ := findSample(mapped)
				if next == nil || next == selected || !reflect.DeepEqual(next.Argument, before) || !reflect.DeepEqual(next.Captures, captures) || next.Argument.ResultUsage().Type.ID() != sqltype.Float8 {
					t.Fatal("correlated remap lost argument/capture/type", next)
				}
			})
		}
	})
}
