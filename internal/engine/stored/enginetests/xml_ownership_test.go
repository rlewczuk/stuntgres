package enginetests_test

import (
	"context"
	"maps"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestXMLFrozenCaptureOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, `CREATE TABLE xml_capture_source(a text,b text)`)
	enginetest.SessionSQL(t, s, `CREATE VIEW xml_capture_view AS SELECT XMLSERIALIZE(CONTENT XMLPARSE(CONTENT a PRESERVE WHITESPACE) AS text NO INDENT) AS first,XMLSERIALIZE(DOCUMENT XMLPARSE(DOCUMENT b STRIP WHITESPACE) AS name INDENT) AS second FROM xml_capture_source`)
	read := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "xml_capture_view")
	accepted := stored.ViewOf(relation)
	root := accepted.Query().Root.(*plan.ProjectNode)
	if err := accepted.Validate(read, relation); err != nil {
		t.Fatal(err)
	}
	if len(accepted.CastUsages()) != 2 {
		t.Fatal("missing resolved XML targets")
	}
	for path, usage := range accepted.CastUsages() {
		replacement := enginetest.Usage(e, sqltype.Name)
		if usage.Type.ID() == sqltype.Name {
			replacement = enginetest.Usage(e, sqltype.Text)
		}
		rejected := accepted.With(func(parts *stored.ViewParts) {
			parts.CastUsages = maps.Clone(parts.CastUsages)
			parts.CastUsages[path] = replacement
		})
		if err := rejected.Validate(read, relation); err == nil {
			t.Fatal("another current target acquired occurrence authority")
		}
		foreign := *usage.Type
		usage.Type = &foreign
		rejected = accepted.With(func(parts *stored.ViewParts) {
			parts.CastUsages = maps.Clone(parts.CastUsages)
			parts.CastUsages[path] = usage
		})
		if err := rejected.Validate(read, relation); err == nil {
			t.Fatal("foreign target generation accepted")
		}
	}
	syntax := accepted.Parts().Syntax
	rejected := accepted.With(func(parts *stored.ViewParts) {
		next := *syntax
		next.Targets = append([]parser.Expr(nil), syntax.Targets...)
		first := next.Targets[0].(parser.XMLSerializeExpr)
		other := next.Targets[1].(parser.XMLSerializeExpr)
		first.Child = other.Child
		next.Targets[0] = first
		parts.Syntax = &next
	})
	if err := rejected.ValidateLiterals(read); err == nil {
		t.Fatal("swapped written operand occurrence accepted")
	}
	// Also replace the registered raw root coherently: the operand itself must
	// still authenticate against the accepted expression, not just root maps.
	swappedSyntax := rejected.Parts().Syntax
	rejected = accepted.With(func(parts *stored.ViewParts) {
		project := *root
		project.Syntax = swappedSyntax
		query := *parts.Query
		query.Root = &project
		parts.Query, parts.Syntax = &query, swappedSyntax
		parts.Frames = maps.Clone(parts.Frames)
		parts.Frames[project.Frame] = &project
		parts.Queries = maps.Clone(parts.Queries)
		delete(parts.Queries, syntax)
		parts.Queries[swappedSyntax] = &project
	})
	if err := rejected.ValidateLiterals(read); err == nil {
		t.Fatal("registered swapped written operand occurrence accepted")
	}
	for _, node := range root.Expressions {
		serialize, _, err := plan.XMLSerialization(node.(plan.CoercionExpr))
		if err != nil {
			t.Fatal(err)
		}
		mapped, changed, err := stored.RetagTypes(read, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed {
			t.Fatal("identity retag", err)
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		mappedSource, _, err := plan.XMLSerialization(mapped.(plan.CoercionExpr))
		if err != nil {
			t.Fatal(err)
		}
		remappedSource, _, err := plan.XMLSerialization(remapped.(plan.CoercionExpr))
		if err != nil {
			t.Fatal(err)
		}
		if mappedSource.ResultUsage() != serialize.ResultUsage() || remappedSource.ResultUsage() != serialize.ResultUsage() {
			t.Fatal("identity mapping replaced conversion authority")
		}
		parse := serialize.Child.(plan.CoercionExpr).Child.(functions.XMLParse)
		if _, _, err := stored.RetagTypes(read, node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == parse.Child.ResultUsage().Type {
				return nil
			}
			return d
		}); err == nil {
			t.Fatal("missing source conversion TEXT generation accepted")
		}
		if _, err := serialize.Rebuild(expr.InfoOf(serialize), nil); err == nil {
			t.Fatal("malformed rebuild accepted")
		}
	}
	if accepted.Query().Root != root || accepted.Parts().Syntax != syntax {
		t.Fatal("rejected publication mutated accepted backing")
	}
	for _, display := range []bool{false, true} {
		if _, err := stored.ViewDefinitionSQL(context.Background(), read, relation, stored.Presentation{Deparse: display}); err != nil {
			t.Fatal("XML presentation", err)
		}
	}
}

func TestXMLSerializationAssociation(t *testing.T) {
	e := engine.New()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	opener := parser.Position{Offset: 0, Character: 1}
	parsePosition := parser.Position{Offset: 21, Character: 22}
	literalPosition := parser.Position{Offset: 38, Character: 39}
	raw := parser.XMLSerializeExpr{
		Pos: opener, Target: parser.TypeName{Name: "name"},
		Child: parser.XMLParseExpr{Pos: parsePosition, Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "<a/>", Pos: literalPosition}}},
	}
	bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	accepted := bound.(plan.CoercionExpr)
	conversion := accepted.Child.(plan.CoercionExpr)
	source, target, err := plan.XMLSerialization(accepted)
	if err != nil {
		t.Fatal("genuine selected NAME association", err)
	}
	declared := enginetest.Usage(e, sqltype.Name)
	if target.Type != declared.Type || target.Collation != source.ResultUsage().Collation || target.Collation == declared.Collation {
		t.Fatal("fixture lost distinguishing effective versus declared NAME collation", target, declared)
	}
	checked, err := conversion.Plan.Rebind(source.ResultUsage(), target, sqltype.Implicit)
	if err != nil || !checked.Equal(conversion.Plan) {
		t.Fatal("selected plan and effective Usage disagree", err)
	}
	reject := func(label string, candidate plan.CoercionExpr) {
		t.Helper()
		if _, _, err := plan.XMLSerialization(candidate); err == nil {
			t.Fatal("accepted malformed association:", label)
		}
	}
	// The former construction kept the declared NAME collation in its plan.
	stale := conversion
	stale.Plan, err = sqltype.PlanCoercion(source.ResultUsage(), declared, sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	candidate := accepted
	candidate.Child = stale
	reject("declared instead of effective target", candidate)
	foreign := *target.Type
	foreignUsage := target
	foreignUsage.Type = &foreign
	stale.Plan, err = sqltype.PlanCoercion(source.ResultUsage(), foreignUsage, sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Child = stale
	reject("foreign target generation in selected plan", candidate)
	stale = conversion
	stale.Pos = parsePosition
	candidate.Child = stale
	reject("conversion from another source occurrence", candidate)
	swapped := source
	swapped.Syntax.Child = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue, Pos: literalPosition}}
	stale = conversion
	stale.Child = swapped
	candidate.Child = stale
	reject("another written operand occurrence", candidate)
	swapped = source
	swapped.Document = !source.Document
	stale.Child = swapped
	candidate.Child = stale
	reject("provider options differ from captured syntax", candidate)
	candidate = accepted
	candidate.Mode = sqltype.Assignment
	reject("result marker context", candidate)
	for _, mapExpression := range []func(expr.Expr) (expr.Expr, error){
		func(node expr.Expr) (expr.Expr, error) {
			mapped, changed, err := stored.RetagTypes(e.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if changed {
				t.Fatal("identity retag changed accepted association")
			}
			return mapped, err
		},
		func(node expr.Expr) (expr.Expr, error) {
			return stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		},
	} {
		mapped, err := mapExpression(accepted)
		if err != nil {
			t.Fatal(err)
		}
		retained, usage, err := plan.XMLSerialization(mapped.(plan.CoercionExpr))
		if err != nil || usage != target || retained.Child != source.Child {
			t.Fatal("mapping lost accepted target/source association", err)
		}
	}
	if _, _, err := plan.XMLSerialization(accepted); err != nil {
		t.Fatal("rejection mutated accepted association", err)
	}
}
