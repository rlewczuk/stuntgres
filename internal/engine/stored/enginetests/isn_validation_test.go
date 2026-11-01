package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIsnValidationStoredIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil); err != nil { t.Fatal(err) }
	view := e.CatalogRead()
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	foreign := engine.New().CatalogRead()
	for _, call := range []struct{ name, role, input string }{
		{"is_valid", "ean13", "2222222222222!"}, {"make_valid", "ean13", "2222222222222!"},
		{"is_valid", "isbn", "123456789X!"}, {"make_valid", "isbn", "123456789X!"},
		{"is_valid", "isbn13", "9781234567897!"}, {"make_valid", "isbn13", "9781234567897!"},
		{"is_valid", "ismn", "M123456785!"}, {"make_valid", "ismn", "M123456785!"},
		{"is_valid", "ismn13", "9790123456785!"}, {"make_valid", "ismn13", "9790123456785!"},
		{"is_valid", "issn", "12345679!"}, {"make_valid", "issn", "12345679!"},
		{"is_valid", "issn13", "9771234567003!"}, {"make_valid", "issn13", "9771234567003!"},
		{"is_valid", "upc", "123456789012!"}, {"make_valid", "upc", "123456789012!"},
		{"isn_weak", "", ""}, {"isn_weak", "boolean", "true"},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		var args []parser.Expr
		if call.role != "" {
			args = []parser.Expr{parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: call.input}}, TypeName: parser.TypeName{Name: call.role}, Syntax: parser.PostfixCast}}
		}
		node, err := b.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: call.name}}}, Args: args})
		if err != nil { t.Fatal(call, err) }
		n := node.(functions.IsnValidation)
		captured := n.RoutineReference.(*catalog.Procedure)
		if _, changed, err := stored.RetagTypes(view, n, identity); err != nil || changed { t.Fatal("genuine identity retag", call, err) }
		remapped, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !reflect.DeepEqual(remapped, n) { t.Fatal("genuine reference remap", call, err) }
		refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
		if err != nil { t.Fatal(err) }
		found := false
		for _, ref := range refs { found = found || ref == captured.Address() }
		if !found { t.Fatal("lost routine dependency", call) }
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Origin = nil },
			func(p *catalog.Procedure) { p.Volatility = (p.Volatility+1)%3 },
			func(p *catalog.Procedure) { p.Signature = "25" },
			func(p *catalog.Procedure) { p.Result = 25 },
			func(p *catalog.Procedure) { p.Strict = false },
		} {
			bad := *captured
			mutate(&bad)
			forged := n
			forged.RoutineReference = &bad
			if _, _, err := stored.RetagTypes(view, forged, identity); err == nil { t.Fatal("forged original", call) }
			state := catalog.CloneState(view.State())
			state.SetDescriptor(bad.Address(), &bad)
			if _, _, err := stored.RetagTypes(view.WithState(state), n, identity); err == nil { t.Fatal("forged current", call) }
		}
		badRevision := *captured
		badRevision.ObjectRevision = captured.Revision()+1
		forgedRevision := n
		forgedRevision.RoutineReference = &badRevision
		if _, _, err := stored.RetagTypes(view, forgedRevision, identity); err == nil { t.Fatal("forged original revision", call) }
		foreignResult := *n.Usage.Type
		forgedResult := n
		forgedResult.Usage.Type = &foreignResult
		if _, _, err := stored.RetagTypes(view, forgedResult, identity); err == nil { t.Fatal("foreign result generation", call) }
		if n.Count == 1 {
			foreignFormal := *n.Args[0].ResultUsage().Type
			forgedFormal := n
			forgedFormal.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: foreignFormal.Usage()}, Value: sqltype.Null(&foreignFormal)}
			if _, _, err := stored.RetagTypes(view, forgedFormal, identity); err == nil { t.Fatal("foreign formal generation", call) }
		}
		forged := n
		forged.RoutineRef.Epoch++
		if _, _, err := stored.RetagTypes(view, forged, identity); err == nil { t.Fatal("forged reference epoch", call) }
		forged = n
		forged.RoutineRef = foreign.Owner().Domain.Ref(view.Epoch(), n.RoutineRef.Address)
		if _, _, err := stored.RetagTypes(view, forged, identity); err == nil { t.Fatal("foreign reference owner", call) }
		if !reflect.DeepEqual(expr.InfoOf(n).RoutineReference, captured) { t.Fatal("rejection changed source") }
	}
}
