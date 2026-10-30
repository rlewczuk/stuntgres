package enginetests_test

import (
	"context"
	"maps"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
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

func TestXMLPredicateFrozenOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, `CREATE TABLE xml_predicate_owner(p text,x xml)`)
	enginetest.SessionSQL(t, s, `CREATE VIEW xml_predicate_owned AS SELECT x IS DOCUMENT AS d,XMLEXISTS(p PASSING BY REF x BY VALUE) AS e FROM xml_predicate_owner`)
	read := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "xml_predicate_owned")
	accepted := stored.ViewOf(relation)
	root := accepted.Query().Root.(*plan.ProjectNode)
	syntax := accepted.Parts().Syntax
	originalArgs := append([]parser.Expr(nil), syntax.Targets[1].(parser.FunctionExpr).Args...)
	if err := accepted.Validate(read, relation); err != nil {
		t.Fatal(err)
	}
	document, ok := root.Expressions[0].(functions.XMLDocumentPredicate)
	if !ok {
		t.Fatalf("document recipe %T", root.Expressions[0])
	}
	exists, ok := root.Expressions[1].(functions.XMLExists)
	if !ok || exists.CallRoutineReference() == nil || exists.CallRoutineReference().RoutineOID() != 2614 {
		t.Fatal("actual xmlexists authority", root.Expressions[1])
	}
	for _, node := range []expr.Expr{document, exists} {
		mapped, changed, err := stored.RetagTypes(read, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed || mapped.ResultUsage() != node.ResultUsage() {
			t.Fatal("identity retag changed accepted generation", err)
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || remapped.ResultUsage() != node.ResultUsage() {
			t.Fatal("identity remap changed authority", err)
		}
	}
	replace := func(mutate func(*parser.FunctionExpr)) *stored.View {
		return accepted.With(func(parts *stored.ViewParts) {
			next := *syntax
			next.Targets = append([]parser.Expr(nil), syntax.Targets...)
			raw := next.Targets[1].(parser.FunctionExpr)
			raw.Args = append([]parser.Expr(nil), raw.Args...)
			mutate(&raw)
			next.Targets[1] = raw
			project := *root
			project.Syntax = &next
			query := *parts.Query
			query.Root = &project
			parts.Query, parts.Syntax = &query, &next
			parts.Frames = maps.Clone(parts.Frames)
			parts.Frames[project.Frame] = &project
			parts.Queries = maps.Clone(parts.Queries)
			delete(parts.Queries, syntax)
			parts.Queries[&next] = &project
		})
	}
	// Ordinary function arguments replay from captured ordinal/slot identities,
	// not column spelling or positions, which must remain free to follow renames.
	for _, mutate := range []func(*parser.FunctionExpr){
		func(raw *parser.FunctionExpr) { raw.Args[0] = raw.Args[1] },
		func(raw *parser.FunctionExpr) { raw.Args[1] = raw.Args[0] },
	} {
		spelling := replace(mutate)
		if err := spelling.ValidateLiterals(read); err != nil {
			t.Fatal("raw column spelling replaced frozen slot authority", err)
		}
		bound, ok := spelling.Query().Root.(*plan.ProjectNode).Expressions[1].(functions.XMLExists)
		if !ok || bound.CallRoutineReference() != exists.CallRoutineReference() || bound.ResultUsage() != exists.ResultUsage() {
			t.Fatal("raw spelling replaced actual xmlexists authority")
		}
		for _, deparse := range []bool{false, true} {
			presentation := stored.Presentation{Deparse: deparse, Columns: relation.Columns}
			want, err := accepted.SelectSQL(context.Background(), read, presentation)
			if err != nil {
				t.Fatal(err)
			}
			got, err := spelling.SelectSQL(context.Background(), read, presentation)
			if err != nil || got != want {
				t.Fatal("raw spelling redirected captured function arguments", deparse, got, want, err)
			}
		}
	}
	for _, mutate := range []func(*parser.FunctionExpr){
		func(raw *parser.FunctionExpr) {
			raw.Args[0] = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "/forged", Pos: raw.Args[0].SourcePosition()}}
		},
		func(raw *parser.FunctionExpr) {
			raw.Args[1] = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "<forged/>", Pos: raw.Args[1].SourcePosition()}}
		},
		func(raw *parser.FunctionExpr) { raw.Args = raw.Args[:1] },
	} {
		rejected := replace(mutate)
		if err := rejected.ValidateLiterals(read); err == nil {
			t.Fatal("foreign source occurrence acquired accepted authority")
		}
	}
	if accepted.Query().Root != root || accepted.Parts().Syntax != syntax || !reflect.DeepEqual(syntax.Targets[1].(parser.FunctionExpr).Args, originalArgs) || exists.Path != root.Expressions[1].(functions.XMLExists).Path || exists.CallRoutineReference() != root.Expressions[1].(functions.XMLExists).CallRoutineReference() {
		t.Fatal("rejected publication mutated retained backing")
	}
	for _, deparse := range []bool{false, true} {
		if _, err := stored.ViewDefinitionSQL(context.Background(), read, relation, stored.Presentation{Deparse: deparse}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("implicit-cast", xmlPredicateCastFrozenOwnership)
	t.Run("erased-source-cast", xmlPredicateErasedSourceCastOwnership)
}

func xmlPredicateCastFrozenOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	// The alternate routines deliberately have the same results. Output
	// equivalence cannot authenticate a selected cast or executable occurrence.
	for _, name := range []string{"xml_predicate_cast_source", "xml_predicate_cast_alternate"} {
		enginetest.SessionSQL(t, s, "CREATE FUNCTION "+name+"(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(DOCUMENT ''<r/>'')'")
	}
	enginetest.SessionSQL(t, s, "CREATE FUNCTION xml_predicate_exists_alternate(text,xml) RETURNS boolean LANGUAGE sql IMMUTABLE AS 'SELECT true'")
	enginetest.SessionSQL(t, s, "CREATE TABLE xml_predicate_cast_owner(n integer)")
	enginetest.SessionSQL(t, s, "CREATE CAST (integer AS xml) WITH FUNCTION xml_predicate_cast_source(integer) AS IMPLICIT")
	enginetest.SessionSQL(t, s, "CREATE VIEW xml_predicate_cast_owned AS SELECT XMLEXISTS('/r' PASSING n) AS v FROM xml_predicate_cast_owner")
	read := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "xml_predicate_cast_owned")
	accepted := stored.ViewOf(relation)
	root := accepted.Query().Root.(*plan.ProjectNode)
	parent, ok := root.Expressions[0].(functions.XMLExists)
	if !ok || parent.CallRoutineReference() == nil || parent.CallRoutineReference().RoutineOID() != 2614 {
		t.Fatal("cast-bearing recipe lost selected xmlexists routine", root.Expressions[0])
	}
	edge, ok := read.Cast(sqltype.Int4, sqltype.XML)
	if !ok || edge.Context != "i" || edge.Method != "f" {
		t.Fatal("fixture lost its declared implicit function cast", edge)
	}
	selected := read.MustProcedure(edge.Function)
	if selected.Name() != "xml_predicate_cast_source" || selected.Strict {
		t.Fatal("wrong selected nonSTRICT cast procedure", selected)
	}
	findCast := func(node expr.Expr) plan.SQLRoutineExpr {
		var call plan.SQLRoutineExpr
		count := 0
		plan.WalkExpression(node, func(child expr.Expr) {
			if candidate, ok := child.(plan.SQLRoutineExpr); ok && candidate.Ref.Address == edge.Function {
				call = candidate
				count++
			}
		})
		if count != 1 {
			t.Fatal("expected one actual selected catalog-cast call", count, node)
		}
		return call
	}
	call := findCast(parent.Context)
	if call.Procedure != selected || call.RoutineReference != selected || call.Ref.Domain() != read.Owner().Domain || call.Ref.Epoch != read.Epoch() || call.ResultUsage().Type.ID() != sqltype.XML || len(call.Args) != 1 || call.Args[0].ResultUsage().Type.ID() != sqltype.Int4 || !call.Signature.ValidFor(selected.RoutineDefinition()) || call.ValidateStoredProvider() != nil {
		t.Fatal("cast association or invocation generation was not retained", call)
	}
	if err := accepted.Validate(read, relation); err != nil {
		t.Fatal(err)
	}
	var alternateCast, alternateExists *catalog.Procedure
	for _, descriptor := range read.Descriptors() {
		if procedure, ok := descriptor.(*catalog.Procedure); ok && procedure.Namespace() == relation.Namespace() {
			switch procedure.Name() {
			case "xml_predicate_cast_alternate":
				alternateCast = procedure
			case "xml_predicate_exists_alternate":
				alternateExists = procedure
			}
		}
	}
	if alternateCast == nil || alternateExists == nil || alternateCast.Address() == selected.Address() || alternateCast.Signature != selected.Signature || alternateCast.Result != selected.Result || alternateCast.Strict {
		t.Fatal("same-valued alternate executable fixtures missing")
	}
	parts := accepted.Parts()
	originalSyntax := parts.Syntax
	originalExpressions := append([]expr.Expr(nil), root.Expressions...)
	originalRefs := maps.Clone(parts.RoutineRefs)
	originalDependencies := append([]catalog.ExpressionDependency(nil), parts.Dependencies...)
	expressionBacking, argumentBacking := &root.Expressions[0], &call.Args[0]
	inputGeneration, resultGeneration := call.Signature.Inputs[0].Type, call.Signature.Result.Type
	assertAssociation := func(node expr.Expr) {
		next, ok := node.(functions.XMLExists)
		if !ok || next.CallRoutineReference() != parent.CallRoutineReference() || next.ResultUsage() != parent.ResultUsage() {
			t.Fatal("rewrite replaced selected xmlexists authority", node)
		}
		cast := findCast(next.Context)
		if cast.Procedure != selected || cast.RoutineReference != selected || cast.Ref != call.Ref || cast.Signature.Inputs[0].Type != inputGeneration || cast.Signature.Result.Type != resultGeneration || cast.ValidateStoredProvider() != nil || !reflect.DeepEqual(node, parent) {
			t.Fatal("rewrite changed selected cast or descriptor generation", cast)
		}
	}
	rebuilt, err := parent.Rebuild(expr.InfoOf(parent), parent.Children())
	if err != nil {
		t.Fatal(err)
	}
	assertAssociation(rebuilt)
	retagged, changed, err := stored.RetagTypes(read, parent, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || changed {
		t.Fatal("identity retag changed a cast-bearing recipe", changed, err)
	}
	assertAssociation(retagged)
	remapped, err := stored.RemapExpression(parent, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertAssociation(remapped)
	for _, substitute := range []func(*plan.SQLRoutineExpr){
		func(bad *plan.SQLRoutineExpr) { bad.Procedure = alternateCast },
		func(bad *plan.SQLRoutineExpr) { bad.RoutineReference = alternateCast },
		func(bad *plan.SQLRoutineExpr) { bad.Ref = read.Owner().Domain.Ref(read.Epoch(), alternateCast.Address()) },
		func(bad *plan.SQLRoutineExpr) { foreign := *bad.Usage.Type; bad.Usage.Type = &foreign },
	} {
		bad := call
		substitute(&bad)
		if err := bad.ValidateStoredProvider(); err == nil {
			t.Fatal("substituted cast executable or foreign result generation accepted", bad)
		}
	}
	// Forge the real frozen executable occurrence keys, not ignored raw spelling.
	// Existing dependencies and the accepted bound recipe remain authoritative.
	forgeries := 0
	for path, ref := range originalRefs {
		var alternate *catalog.Procedure
		switch ref.Address {
		case edge.Function:
			alternate = alternateCast
		case (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2614}):
			alternate = alternateExists
		default:
			continue
		}
		forgeries++
		rejected := accepted.With(func(next *stored.ViewParts) {
			next.RoutineRefs = maps.Clone(next.RoutineRefs)
			next.RoutineRefs[path] = read.Owner().Domain.Ref(read.Epoch(), alternate.Address())
		})
		if err := rejected.Validate(read, relation); err == nil {
			t.Fatal("same-valued substituted executable occurrence acquired publication authority", ref.Address)
		}
	}
	if forgeries != 2 {
		t.Fatal("missing selected cast or xmlexists frozen executable occurrence", forgeries)
	}
	if accepted.Query().Root != root || accepted.Parts().Syntax != originalSyntax || &root.Expressions[0] != expressionBacking || &call.Args[0] != argumentBacking || !reflect.DeepEqual(root.Expressions, originalExpressions) || !reflect.DeepEqual(accepted.Parts().RoutineRefs, originalRefs) || !reflect.DeepEqual(accepted.Parts().Dependencies, originalDependencies) || read.MustProcedure(edge.Function) != selected {
		t.Fatal("cast-bearing forgery or rewrite mutated accepted backing")
	}
	if err := accepted.Validate(read, relation); err != nil {
		t.Fatal("rejected cast-bearing publication contaminated accepted definition", err)
	}
}

func xmlPredicateErasedSourceCastOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE FUNCTION xml_predicate_erased_source(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(DOCUMENT ''<r/>'')'")
	enginetest.SessionSQL(t, s, "CREATE CAST (integer AS xml) WITH FUNCTION xml_predicate_erased_source(integer) AS IMPLICIT")
	enginetest.SessionSQL(t, s, "CREATE VIEW xml_predicate_erased_casts AS SELECT CAST(NULL AS integer) IS DOCUMENT AS d,XMLPARSE(DOCUMENT CAST('<r/>' AS text)) AS x")
	read := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "xml_predicate_erased_casts")
	accepted := stored.ViewOf(relation)
	parts := accepted.Parts()
	root := accepted.Query().Root.(*plan.ProjectNode)
	document := root.Expressions[0].(functions.XMLDocumentPredicate)
	parsed := root.Expressions[1].(functions.XMLParse)
	cast, err := plan.XMLSourceOperand(document.Child, document.Pos)
	if err != nil {
		t.Fatal(err)
	}
	// Both ordinary same-Usage casts are genuinely absent from execution,
	// while the document predicate still owns its real nonSTRICT SQL cast.
	selected := document.Child.(plan.SQLRoutineExpr)
	edge, ok := read.Cast(sqltype.Int4, sqltype.XML)
	if !ok || selected.Ref.Address != edge.Function || selected.Procedure != read.MustProcedure(edge.Function) || selected.ValidateStoredProvider() != nil {
		t.Fatal("document source lost actual selected cast authority")
	}
	if _, ok := cast.(plan.LiteralExpr); !ok {
		t.Fatal("fixture retained an executable no-op source cast", cast)
	}
	text, err := plan.XMLSourceOperand(parsed.Child, parsed.Pos)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := text.(plan.LiteralExpr); !ok {
		t.Fatal("XMLPARSE fixture retained an executable no-op source cast", text)
	}
	if cast.SourcePosition() == document.Syntax.Child.SourcePosition() || text.SourcePosition() == parsed.Syntax.Child.SourcePosition() {
		t.Fatal("fixture does not distinguish standard CAST from its actual operand")
	}
	if err := accepted.Validate(read, relation); err != nil {
		t.Fatal(err)
	}
	originalUsages := maps.Clone(parts.CastUsages)
	backing := &root.Expressions[0]
	for target := range root.Expressions {
		path := analyze.ViewLiteralPath{Query: root.Frame, Clause: analyze.ViewTargetLiteral, Target: target, Node: 1}
		if _, ok := originalUsages[path]; !ok {
			t.Fatal("missing exact written source-cast occurrence", path)
		}
		for _, remove := range []bool{false, true} {
			rejected := accepted.With(func(next *stored.ViewParts) {
				next.CastUsages = maps.Clone(next.CastUsages)
				if remove {
					delete(next.CastUsages, path)
				} else {
					next.CastUsages[path] = enginetest.Usage(e, sqltype.Bool)
				}
			})
			if err := rejected.ValidateLiterals(read); err == nil {
				t.Fatal("erased source cast acquired authority without its exact Usage", path, remove)
			}
		}
	}
	if accepted.Query().Root != root || &root.Expressions[0] != backing || accepted.Parts().Syntax != parts.Syntax || !reflect.DeepEqual(accepted.Parts().CastUsages, originalUsages) || document.Child.(plan.SQLRoutineExpr).Procedure != selected.Procedure {
		t.Fatal("rejected source-cast publication mutated accepted backing")
	}
	if err := accepted.Validate(read, relation); err != nil {
		t.Fatal("rejected source-cast publication contaminated its accepted owner", err)
	}
}
