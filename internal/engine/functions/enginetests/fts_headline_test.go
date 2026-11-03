package enginetests_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The leaf provider protocol test has the same family name. This composed
// supplement obtains Info from the actual binder, not a fabricated collation.
func TestFTSHeadlineProviderLifecycle(t *testing.T) {
	e := engine.New()
	text, ok := e.CatalogRead().LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing TEXT")
	}
	retain := func(t *testing.T, node functions.FTSHeadline) {
		t.Helper()
		if err := node.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(expr.Info, []expr.Expr) (expr.Expr, error){node.Rebuild, node.Substitute} {
			changed, err := change(node.Info, node.Children())
			if err != nil {
				t.Fatal(err)
			}
			next, ok := changed.(functions.FTSHeadline)
			if !ok || next.Validate() != nil || next.Plan != node.Plan || !reflect.DeepEqual(next.Info, node.Info) {
				t.Fatal("headline reconstruction lost bound authority", changed)
			}
		}
		render, err := node.Render()
		if err != nil || render.Namespace != "pg_catalog" || render.Replay.Validate == nil || render.Replay.Validate() != nil {
			t.Fatal("headline replay authority", err)
		}
	}
	for _, explicit := range []bool{false, true} {
		for _, options := range []bool{false, true} {
			for _, fromOptions := range []bool{false, true} {
				if fromOptions && !options {
					continue
				}
				for _, collation := range []struct {
					name string
					id   uint32
				}{{"C", 950}, {"POSIX", 951}} {
					t.Run(fmt.Sprintf("explicit_%t/options_%t/from_options_%t/%s", explicit, options, fromOptions, collation.name), func(t *testing.T) {
						document, option := "'Cat'::text", "''::text"
						clause := ` COLLATE "` + collation.name + `"`
						if fromOptions {
							option += clause
						} else {
							document += clause
						}
						arguments := document + ",'cat'::tsquery"
						if explicit {
							arguments = "'english'::regconfig," + arguments
						}
						if options {
							arguments += "," + option
						}
						query := "SELECT ts_headline(" + arguments + ")"
						binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
						raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
						bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
						if err != nil {
							t.Fatal(err)
						}
						node, ok := bound.(functions.FTSHeadline)
						if !ok || node.Usage.Type != text || node.Usage.Typmod != -1 || node.Usage.Collation.ID != collation.id || node.CollationConflict || node.CollationExplicit.Character != strings.Index(query, "COLLATE")+1 {
							t.Fatal("headline lost bound collation/position", bound)
						}
						volatility := expr.Stable
						if explicit {
							volatility = expr.Immutable
						}
						if node.Properties().Volatility != volatility || node.CallRoutineReference() == nil {
							t.Fatal("headline changed routine properties")
						}
						retain(t, node)
						persisted, err := (stored.ScalarRenderer{}).SQL(t.Context(), e.CatalogRead(), node)
						if err != nil {
							t.Fatal(err)
						}
						replay := "SELECT " + persisted
						fresh := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
						rebound, err := fresh.Bind(enginetest.Statement(t, replay).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
						if err != nil {
							t.Fatal(err)
						}
						next, ok := rebound.(functions.FTSHeadline)
						if !ok || next.Usage != node.Usage || next.CollationConflict || next.CollationExplicit.Character != strings.Index(replay, "COLLATE")+1 {
							t.Fatal("headline replay lost collation provenance", rebound)
						}
						retain(t, next)
					})
				}
			}
		}
	}
	t.Run("implicit_conflict", func(t *testing.T) {
		binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
		left, right := text.Usage(), text.Usage()
		left.Collation.ID, right.Collation.ID = 950, 951
		// Public parameter formals convey type identity, not expression
		// collation. Real row columns supply independently declared implicit
		// collations, as in ordinary table-backed expression binding.
		columns := []catalog.Column{{Name: "doc", Usage: left}, {Name: "opts", Usage: right}}
		namespace := &analyze.QueryNamespace{
			Columns: []analyze.QueryColumn{
				{Descriptor: columns[0], Slot: 0, Leaf: -1, Physical: -1},
				{Descriptor: columns[1], Slot: 1, Leaf: -1, Physical: -1},
			},
			Frame: columns,
		}
		binder.Query.Namespace = namespace
		const query = "SELECT ts_headline(doc,'cat'::tsquery,opts)"
		bound, err := binder.Bind(enginetest.Statement(t, query).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node, ok := bound.(functions.FTSHeadline)
		if !ok || !node.CollationConflict || node.CollationExplicit != (parser.Position{}) || node.CollationLeft != left.Collation || node.CollationRight != right.Collation || node.CollationConflictPos.Character != strings.LastIndex(query, "opts")+1 || !node.Usage.Valid() {
			t.Fatal("headline lost implicit conflict state", bound)
		}
		retain(t, node)
		persisted, err := (stored.Deparser{
			View: e.CatalogRead(),
			ColumnSQL: func(column plan.ColumnExpr) (string, error) {
				return columns[column.Slot].Name, nil
			},
		}).SQL(t.Context(), node, stored.DeparseParent{})
		if err != nil {
			t.Fatal(err)
		}
		fresh := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
		fresh.Query.Namespace = namespace
		replay := "SELECT " + persisted
		rebound, err := fresh.Bind(enginetest.Statement(t, replay).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		next, ok := rebound.(functions.FTSHeadline)
		if !ok || !next.CollationConflict || next.Usage != node.Usage || next.CollationLeft != node.CollationLeft || next.CollationRight != node.CollationRight || next.CollationConflictPos.Character != strings.LastIndex(replay, "opts")+1 {
			t.Fatal("replay lost implicit conflict provenance", rebound)
		}
		retain(t, next)
	})
}
