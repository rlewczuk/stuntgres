package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCubeTransformProviderIdentity(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	view := e.CatalogRead()
	cube := string(extension.CubeSignature(base.SQLType.ID()))
	want := map[string]string{"cube_subset":cube+",1007", "cube_union":cube+","+cube, "cube_inter":cube+","+cube, "cube_enlarge":cube+",701,23"}
	for name, signature := range want {
		address := view.NameAddress(ids.NameKey{ClassID:ids.ProcedureClassID,Namespace:ext.TargetNamespace,Name:name,Signature:signature})
		p, ok := view.Procedure(address)
		if !ok || address.ObjectID == 0 || p.Result != ids.OID(base.SQLType.ID()) || p.Extension != ext.Address() || p.Owner != ext.Owner {
			t.Fatal("transform identity",name,p)
		}
		if err := (extension.CatalogProvider{ids.ProcedureClassID}).Validate(view,p); err != nil { t.Fatal(err) }
		for _, corrupt := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Signature = "23,23" },
			func(p *catalog.Procedure) { p.Result = 23 },
			func(p *catalog.Procedure) { p.Implementation = nil },
			func(p *catalog.Procedure) { p.Extension = ids.ObjectAddress{} },
		} {
			next := catalog.CloneState(view.State())
			counterfeit := *p
			corrupt(&counterfeit)
			next.SetDescriptor(address,&counterfeit)
			if err := extension.StockCube.ValidateExtension(view.WithState(next),ext); err == nil { t.Fatal("forged provider admitted",name) }
		}
	}
	// Appended transformations must not displace coordinate operator backings.
	procedures, operators := cubeCoordinateUnitMembers(t, view, ext, base)
	for i, address := range operators {
		op := view.MustOperator(address)
		proc := view.MustProcedure(procedures[i+2])
		if op.Function != proc.Address().ObjectID || proc.Name() != []string{"cube_coord", "cube_coord_llur"}[i] || proc.Signature != catalog.ProcedureSignature(cube+",23") || proc.Result != 701 {
			t.Fatal("transform additions displaced coordinate backing", op, proc)
		}
	}
	metadata, err := extension.OwnerMetadata(view,e.roles.PublishedRead(&e.catalog.Owner,view),ext)
	if err != nil { t.Fatal(err) }
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata),"-- "))
	if err != nil || !found { t.Fatal("owner metadata",err) }
	owners := map[string]string{"cube_subset":"cube,1007","cube_union":"cube,cube","cube_inter":"cube,cube","cube_enlarge":"cube,701,23"}
	for _, member := range record.Members {
		if signature, ok := owners[member.Name]; ok {
			if member.Kind != "procedure" || member.Signature != signature || member.Owner != "postgres" { t.Fatal("symbolic owner identity",member) }
			delete(owners,member.Name)
		}
	}
	if len(owners) != 0 { t.Fatal("missing transform ownership",owners) }
	if err := extension.StockCube.ValidateExtension(e.CatalogRead(),ext); err != nil { t.Fatal(err) }
}

func TestCubeTransformProviderControls(t *testing.T) {
	cubeCallableIdentity(t, sqltype.CubeSubset, sqltype.CubeEnlarge)
	e, _, base := cubeUnitInstalled(t)
	view, ctx := e.CatalogRead(), context.Background()
	value, err := base.SQLType.Input(ctx, "(0)", -1)
	if err != nil { t.Fatal(err) }
	path := analyze.ViewLiteralPath{Query:1,Clause:analyze.ViewTargetLiteral}
	for _, sample := range []struct{value sqltype.Value;want string}{
		{value,`CAST('(0)' AS "public"."cube")`},
		{sqltype.Null(base.SQLType),`CAST(NULL AS "public"."cube")`},
	} {
		literal := plan.LiteralExpr{Info:expr.Info{Usage:base.SQLType.Usage()},Value:sample.value}
		for depth := range 4 {
			var raw parser.Expr = parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue}}
			casts := make(map[analyze.ViewLiteralPath]sqltype.Usage)
			for i := range depth {
				raw = parser.CastExpr{Child:raw}
				key := path
				key.Node = i
				casts[key] = base.SQLType.Usage()
			}
			leaf := path
			leaf.Node = depth
			captured := stored.NewView(stored.ViewParts{Literals:map[analyze.ViewLiteralPath]plan.LiteralExpr{leaf:literal},CastUsages:casts})
			ordinal := 0
			got, err := stored.NewQueryRenderer(captured).ExpressionSQLAt(ctx,view,raw,path,&ordinal,stored.DeparseParent{})
			want := sample.want
			for i := 1; i < depth; i++ {
				want = `CAST(`+want+` AS "public"."cube")`
			}
			if err != nil || got != want {
				t.Fatal("cube captured leaf annotation accumulated or outer cast lost",depth,got,want,err)
			}
		}
		text, _ := view.LookupType(sqltype.Text)
		leaf := path
		leaf.Node = 1
		captured := stored.NewView(stored.ViewParts{Literals:map[analyze.ViewLiteralPath]plan.LiteralExpr{leaf:literal},CastUsages:map[analyze.ViewLiteralPath]sqltype.Usage{path:text.Usage()}})
		raw := parser.CastExpr{Child:parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue}}}
		ordinal := 0
		got, err := stored.NewQueryRenderer(captured).ExpressionSQLAt(ctx,view,raw,path,&ordinal,stored.DeparseParent{})
		want := "CAST("+sample.want+" AS TEXT)"
		if err != nil || got != want {
			t.Fatal("different-usage cube conversion lost",got,want,err)
		}
	}
}
