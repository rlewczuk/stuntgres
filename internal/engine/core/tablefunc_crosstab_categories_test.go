package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func categoriesExpression(t *testing.T, e *core.Engine) plan.InstalledTablefuncCrosstabExpr {
	t.Helper()
	n := crosstabUnitExpression(t, e, "crosstab", "SELECT 'g'::text,'a'::text,'10'::text", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "SELECT 'a'::text"}})
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Name:"k",Attribute:1,Usage:text.Usage()},{Name:"v",Attribute:2,Usage:integer.Usage()}})
	if err != nil { t.Fatal(err) }
	n.Usage = shape.Usage()
	return n
}

func TestTablefuncCrosstabCategoriesExpressionIdentity(t *testing.T) {
	e := engine.New()
	crosstabUnitInstalled(t,e)
	t.Run("canonical_shared_selector", func(t *testing.T) {
		categoriesCanonicalSelection(t, e)
	})
	n := categoriesExpression(t,e)
	view := e.CatalogRead()
	if !n.Categories || n.Procedure.Signature!="25,25" || n.Procedure.Display.Source!="crosstab_hash" || n.Procedure.Origin.Declaration!=16 || n.RoutineReference!=n.Procedure {
		t.Fatal("selected hash declaration",n)
	}
	if err:=stored.ValidateInstalledTablefuncCrosstab(n,view);err!=nil {t.Fatal(err)}
	original := append([]expr.Expr(nil),n.Args...)
	for _,rebuild:=range []func(expr.Info,[]expr.Expr)(expr.Expr,error){n.Rebuild,n.Substitute}{
		children:=append([]expr.Expr(nil),n.Args...)
		x,err:=rebuild(n.Info,children)
		if err!=nil {t.Fatal(err)}
		m:=x.(plan.InstalledTablefuncCrosstabExpr)
		if !m.Categories || m.Ref!=n.Ref || m.Procedure!=n.Procedure || m.RoutineReference!=n.Procedure || m.Usage!=n.Usage || &m.Args[0]==&children[0] || &m.Args[0]==&n.Args[0] || !m.SameSourceOccurrence(n) || !m.SameGroup(n,func(a,b expr.Expr)bool{return reflect.DeepEqual(a,b)},false){t.Fatal("reconstruction lost mode/owned capture")}
		children[0]=nil
		if !reflect.DeepEqual(m.Args,original)||!reflect.DeepEqual(n.Args,original){t.Fatal("borrowed reconstruction arguments")}
	}
	for _,mutate:=range []func(*plan.InstalledTablefuncCrosstabExpr){
		func(x *plan.InstalledTablefuncCrosstabExpr){x.Categories=false},
		func(x *plan.InstalledTablefuncCrosstabExpr){x.Ref.Epoch++},
		func(x *plan.InstalledTablefuncCrosstabExpr){x.RoutineReference=nil},
		func(x *plan.InstalledTablefuncCrosstabExpr){x.Args=x.Args[:1]},
	}{bad:=n;mutate(&bad);if stored.ValidateInstalledTablefuncCrosstab(bad,view)==nil{t.Fatal("forged mode/capture admitted")}}
	badMode:=n;badMode.Categories=false
	if _,err:=badMode.Rebuild(badMode.Info,badMode.Args);err==nil{t.Fatal("rebuild accepted signature/mode disagreement")}
	integer,_:=view.LookupType(sqltype.Int4)
	wrong:=append([]expr.Expr(nil),n.Args...)
	one, err := sqltype.Integer(integer, 1)
	if err != nil { t.Fatal(err) }
	wrong[1]=plan.LiteralExpr{Info:expr.Info{Usage:integer.Usage()},Value:one}
	if _,err:=n.Rebuild(n.Info,wrong);err==nil{t.Fatal("hash formal accepted INT4")}
	foreign:=engine.New();crosstabUnitInstalled(t,foreign)
	if stored.ValidateInstalledTablefuncCrosstab(n,foreign.CatalogRead())==nil{t.Fatal("foreign same-number capture admitted")}
	for _,mutate:=range []func(*catalog.Procedure){
		func(p *catalog.Procedure){p.Signature="25,23"},
		func(p *catalog.Procedure){p.Display.Source="crosstab"},
		func(p *catalog.Procedure){p.Owner=foreign.CatalogRead().MustProcedure(n.Ref.Address).Owner},
	}{p:=*n.Procedure;mutate(&p);bad:=n;bad.Procedure,bad.RoutineReference=&p,&p;if stored.ValidateInstalledTablefuncCrosstab(bad,view)==nil{t.Fatal("forged hash declaration admitted")}}
	mapped,changed,err:=stored.RetagTypes(view,n,func(d *sqltype.Descriptor)*sqltype.Descriptor{return d})
	if err!=nil||changed||!reflect.DeepEqual(mapped,n){t.Fatal("unchanged retag replaced capture",changed,err)}
	categoriesExpressionRemap(t,e,n)
	if !reflect.DeepEqual(n.Args,original){t.Fatal("remap changed original argument backing")}
	crosstabPreparedOwnership(t,true)
}

func categoriesExpressionRemap(t *testing.T,e *core.Engine,n plan.InstalledTablefuncCrosstabExpr){
	t.Helper()
	enginetest.Run(t,e,"CREATE ROLE ct_category_current")
	read:=e.CatalogRead()
	ext:=read.MustExtension(read.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"tablefunc"}))
	mapping:=map[ids.ObjectAddress]ids.ObjectAddress{ext.Address():{ClassID:ids.ExtensionClassID,ObjectID:990000}}
	for slot,address:=range ext.Installation.Members{
		destination:=address
		if address.ClassID==ids.ProcedureClassID{destination.ObjectID=ids.OID(990001+slot)}
		mapping[address]=destination
	}
	state:=catalog.CloneState(read.State())
	for address:=range mapping{state.UnbindName(catalog.DescriptorNameKey(read.Get(address)));state.RemoveDescriptor(address)}
	remapDescriptor:=extension.DescriptorRemapper(mapping)
	for address,destination:=range mapping{d:=remapDescriptor(read.Get(address));state.SetDescriptor(destination,d);state.BindName(catalog.DescriptorNameKey(d),destination)}
	owner,ok:=e.Roles().Lookup("ct_category_current");if !ok{t.Fatal("missing actual current owner")}
	current:=*state.MustProcedure(mapping[n.Ref.Address]);current.Owner=owner.Ref;current.ObjectRevision++;state.SetDescriptor(current.Address(),&current)
	state.SetEpoch(state.Epoch()+1)
	destination:=e.Catalog.Read(state)
	if err:=extension.StockTablefunc.ValidateExtension(destination,destination.MustExtension(mapping[ext.Address()]));err!=nil{t.Fatal("remapped full hash provider",err)}
	remap:=func(ref ids.ObjectRef)ids.ObjectRef{ref.Epoch=state.Epoch();if address,ok:=mapping[ref.Address];ok{ref.Address=address};return ref}
	mapper:=stored.ViewQueryRemapper{Catalog:destination,Ref:remap,Type:func(d *sqltype.Descriptor)*sqltype.Descriptor{return d},Procedure:extension.ProcedureRemapper(remapDescriptor,nil,nil)}
	query:=&plan.ProjectNode{QuerySchema:plan.QuerySchema{{Name:"pivot",Usage:n.ResultUsage()}},Child:&plan.ValuesNode{Rows:[][]expr.Expr{{}}},Expressions:[]expr.Expr{n}}
	out,err:=mapper.Query(query);if err!=nil{t.Fatal(err)}
	m:=out.(*plan.ProjectNode).Expressions[0].(plan.InstalledTablefuncCrosstabExpr)
	if !m.Categories||m.Ref!=remap(n.Ref)||m.Procedure==n.Procedure||m.Procedure==&current||!exec.SameRoutineDeclaration(m.Procedure,&current)||m.Procedure.Owner!=n.Procedure.Owner||m.Procedure.Revision()!=n.Procedure.Revision()||m.RoutineReference!=m.Procedure||&m.Args[0]==&n.Args[0]||!sqltype.SameRecordUsage(m.Usage,n.Usage){t.Fatal("hash remap lost original/current identity or argument ownership")}
	if err:=stored.ValidateInstalledTablefuncCrosstab(m,destination);err!=nil{t.Fatal(err)}
	text,err:=(stored.Deparser{View:destination}).SQL(context.Background(),m,stored.DeparseParent{})
	if err!=nil||!strings.Contains(text,"crosstab")||!strings.Contains(text,"SELECT ''a''::text"){t.Fatal("hash rendering lost second argument",text,err)}
	state.RemoveDescriptor(m.Ref.Address)
	if stored.ValidateInstalledTablefuncCrosstab(m,destination)==nil{t.Fatal("removed hash member admitted")}
}

func categoriesCanonicalSelection(t *testing.T, e *core.Engine) {
	t.Helper()
	for _, tc := range []struct {
		query, signature string
		categories       bool
	}{
		{"SELECT crosstab('source'::text,'categories'::text)", "25,25", true},
		{"SELECT crosstab('source','categories')", "25,25", true},
		{"SELECT crosstab('source',7::integer)", "25,23", false},
		{"SELECT crosstab('source')", "25", false},
		{"SELECT crosstab2('source')", "25", false},
		{"SELECT crosstab3('source')", "25", false},
		{"SELECT crosstab4('source')", "25", false},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		raw := enginetest.Statement(t, tc.query).(*parser.Select).Targets[0].(parser.FunctionExpr)
		node, handled, err := b.BindSQLCallable(raw)
		if err != nil || !handled {
			t.Fatal("canonical installed name bypassed shared selection", tc.query, handled, err)
		}
		n, ok := node.(plan.InstalledTablefuncCrosstabExpr)
		if !ok || string(n.Procedure.Signature) != tc.signature || n.Categories != tc.categories || n.RoutineReference != n.Procedure {
			t.Fatal("canonical selected declaration lost", tc.query, node)
		}
	}
	enginetest.Run(t, e, "CREATE SCHEMA ct_selector_shadow")
	enginetest.Run(t, e, "CREATE FUNCTION ct_selector_shadow.crosstab(text,text) RETURNS integer LANGUAGE SQL AS 'SELECT 73'")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "SET search_path TO ct_selector_shadow,public")
	for _, qualified := range []bool{false, true} {
		b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
		b.Clause.AggregateClause = "SELECT"
		query := "SELECT crosstab('source','categories')"
		if qualified {
			query = "SELECT public.crosstab('source','categories')"
		}
		raw := enginetest.Statement(t, query).(*parser.Select).Targets[0].(parser.FunctionExpr)
		node, handled, err := b.BindSQLCallable(raw)
		if err != nil || !handled {
			t.Fatal("ordinary path selection failed", query, handled, err)
		}
		if qualified {
			n, ok := node.(plan.InstalledTablefuncCrosstabExpr)
			if !ok || !n.Categories || n.Procedure.Signature != "25,25" {
				t.Fatal("qualified genuine provider lost to shadow", node)
			}
		} else {
			n, ok := node.(plan.SQLRoutineExpr)
			if !ok || n.Procedure.Implementation != nil || n.Procedure.Namespace() != e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ct_selector_shadow"}) {
				t.Fatal("ordinary path winner acquired installed authority", node)
			}
		}
	}
}
