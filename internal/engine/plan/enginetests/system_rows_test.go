package enginetests_test

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSystemRowsPlanIdentity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
	query, _ := enginetest.QueryFixture(t, e, "SELECT id FROM t TABLESAMPLE system_rows(3)")
	project := query.Root.(*plan.ProjectNode)
	source := project.Child.(*plan.SourceNode)
	if source.Sample == nil || source.Sample.Procedure == nil || source.Sample.Handler.Address != source.Sample.Procedure.Address() {
		t.Fatal("missing selected handler", source)
	}
	original := source.Sample
	usage := original.Count.ResultUsage()
	value, err := sqltype.Integer(usage.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	replacement := plan.LiteralExpr{Info: expr.Info{Usage: usage, Pos: original.Count.SourcePosition()}, Value: value}
	visits := 0
	mapped, err := plan.RewriteQueryExpressions(source, func(bound expr.Expr) (expr.Expr, bool, error) { visits++; return replacement, true, nil })
	if err != nil || visits != 1 || mapped == source || !reflect.DeepEqual(mapped.(*plan.SourceNode).Sample.Count, replacement) || source.Sample != original {
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
		if !reflect.DeepEqual(bound, original.Count) {
			t.Fatal("wrong source operand")
		}
		seen++
	})
	if seen != 1 {
		t.Fatal("sample count omitted from expression walks", seen)
	}
	remapped, err := stored.RemapViewQuery(e.CatalogRead(), source, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || tree(remapped) != tree(source) || remapped.(*plan.SourceNode).Sample == original || !reflect.DeepEqual(original.Count, source.Sample.Count) {
		t.Fatal("stored remap lost or mutated sample", remapped, err)
	}
	t.Run("stored_occurrences", func(t *testing.T) {
		enginetest.Run(t, e, "CREATE VIEW sample_identity AS SELECT id FROM ONLY t AS captured TABLESAMPLE system_rows(3)")
		enginetest.Run(t, e, "CREATE VIEW sample_other_count AS SELECT id FROM ONLY t AS captured TABLESAMPLE system_rows(1)")
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
		if accepted.Query().Root != root || accepted.Parts().Syntax != syntax || !maps.Equal(references, accepted.Parts().RoutineRefs) {
			t.Fatal("rejected capture changed accepted source backing")
		}
		if err := accepted.Validate(read, relation); err != nil {
			t.Fatal("rejection contaminated accepted definition", err)
		}
	})
}
