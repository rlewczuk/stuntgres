package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestExplainStoredAliasRemap(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE explain_alias_source(id integer)")
	enginetest.Run(t, e, `CREATE VIEW explain_alias_view AS
		SELECT "left side"."left key" AS l,"right side"."right key" AS r,
		       "derived side"."derived key" AS d,"function side"."function key" AS g,
		       "values side"."values key" AS v
		FROM explain_alias_source AS "left side"("left key")
		CROSS JOIN explain_alias_source AS "right side"("right key")
		CROSS JOIN (SELECT id FROM explain_alias_source) AS "derived side"("derived key")
		CROSS JOIN generate_series(1,2) AS "function side"("function key")
		CROSS JOIN (VALUES(1)) AS "values side"("values key")`)
	ast := enginetest.Statement(t, "SELECT * FROM explain_alias_view").(*parser.Select)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	var original, mapped *stored.View
	err = func() error {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			return err
		}
		ref, err := e.ResolveReference(auth, ast.From.(*parser.TableReference).Name)
		if err != nil {
			return err
		}
		relation, ok := auth.Catalog.Relation(ref.Address)
		if !ok || relation.View == nil {
			t.Fatal("CREATE VIEW did not publish its real frozen definition")
		}
		original = stored.ViewOf(relation)
		mapped, err = stored.RemapView(auth.Catalog, original, nil,
			func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch++; return ref },
			func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil)
		return err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if mapped == original || mapped.Query() == original.Query() || mapped.Query().Root == original.Query().Root {
		t.Fatal("mapper reused the frozen source graph")
	}
	type occurrence struct {
		node    plan.Node
		columns []string
	}
	collect := func(root plan.Node) map[string]occurrence {
		out := make(map[string]occurrence)
		plan.VisitExecutableQuery(root, func(node plan.Node) {
			var name string
			var columns []string
			switch n := node.(type) {
			case *plan.SourceNode:
				name, columns = n.RangeName, n.ColumnAliases
			case *plan.DerivedNode:
				name, columns = n.RangeName, n.ColumnAliases
			case *plan.FunctionFromNode:
				name, columns = n.RangeName, n.ColumnAliases
			case *plan.ProjectNode:
				name, columns = n.RangeName, n.ColumnAliases
			}
			if name != "" {
				if _, exists := out[name]; exists {
					t.Fatalf("distinct occurrences collapsed into %q", name)
				}
				out[name] = occurrence{node, columns}
			}
		}, func(expr.Expr) {})
		return out
	}
	before, after := collect(original.Query().Root), collect(mapped.Query().Root)
	for name, column := range map[string]string{
		"left side": "left key", "right side": "right key",
		"derived side": "derived key", "function side": "function key", "values side": "values key",
	} {
		old, oldOK := before[name]
		next, nextOK := after[name]
		if !oldOK || !nextOK || old.node == next.node || len(old.columns) != 1 || len(next.columns) != 1 || old.columns[0] != column || next.columns[0] != column {
			t.Fatalf("lost occurrence %q/%q: before=%+v after=%+v", name, column, old, next)
		}
		if &old.columns[0] != &next.columns[0] {
			t.Errorf("copied immutable alias backing for %q", name)
		}
		if !reflect.DeepEqual(old.node.Schema(), next.node.Schema()) || &old.node.Schema()[0] == &next.node.Schema()[0] {
			t.Errorf("schema was mutated or not independently owned for %q", name)
		}
		if source, ok := old.node.(*plan.SourceNode); ok {
			replacement, ok := next.node.(*plan.SourceNode)
			if !ok || source.Ref.Address != replacement.Ref.Address || replacement.Ref.Epoch != source.Ref.Epoch+1 || source.Occurrence != replacement.Occurrence {
				t.Errorf("source identity was not remapped independently of its labels: %q", name)
			}
		}
	}
	if before["left side"].node == before["right side"].node || after["left side"].node == after["right side"].node {
		t.Fatal("self-join occurrences share a source node")
	}
}
