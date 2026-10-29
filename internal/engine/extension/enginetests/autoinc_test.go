package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestAutoincProviderIdentity(t *testing.T) {
	e:=engine.New()
	op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
	for _,q:=range []string{"CREATE EXTENSION autoinc","CREATE SCHEMA moved"} { enginetest.RoleExecute(t,e,op,q) }
	read:=e.CatalogRead()
	a:=read.NameAddress(ids.NameKey{ClassID:ids.ProcedureClassID,Namespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},Name:"autoinc"})
	p:=read.MustProcedure(a)
	control:=extension.StockAutoinc.Control()
	if control.Version!="1.0" || !control.Superuser || control.Trusted || !control.Relocatable || control.Comment==nil || *control.Comment!="functions for autoincrementing fields" { t.Fatal(control) }
	if p.Result!=2279 || p.Signature!="" || p.Strict || p.Volatility!=expr.Volatile || p.Installed.Parallel!='u' || p.Installed.ReturnsSet || p.CostEstimate()!=1 || !catalog.SupportsDMLTrigger(p) { t.Fatal("released declaration",p) }
	for _,tc:=range []struct{name string; change func(*catalog.Procedure)}{
		{"unsealed",func(d *catalog.Procedure){ d.Origin=nil }},
		{"copied-origin",func(d *catalog.Procedure){ o:=*d.Origin; installation:=*o.Installation;o.Installation=&installation;d.Origin=&o }},
		{"slot",func(d *catalog.Procedure){ o:=*d.Origin;o.Declaration++;d.Origin=&o }},
		{"provider",func(d *catalog.Procedure){ d.Implementation=extension.StockPgrowlocks }},
		{"signature",func(d *catalog.Procedure){ d.Signature="23" }},
		{"result",func(d *catalog.Procedure){ d.Result=23 }},
		{"strict",func(d *catalog.Procedure){ d.Strict=true }},
		{"source",func(d *catalog.Procedure){ d.Display.Source="lookalike" }},
		{"owner",func(d *catalog.Procedure){ d.Owner=(&authorization.Domain{}).Principal(d.Owner.ID()) }},
	} {
		t.Run(tc.name,func(t *testing.T) {
			state:=catalog.CloneState(read.State());bad:=*p;tc.change(&bad)
			state.SetDescriptor(a,&bad)
			v:=catalog.NewRead(read.Owner(),state)
			if _,err:=extension.ProcedureSymbol(v,&bad);err==nil { t.Fatal("forged installed authority admitted",tc.name) }
			if err:=extension.StockAutoinc.ValidateProcedure(v,&bad);err==nil { t.Fatal("forged provider declaration admitted",tc.name) }
		})
	}
	ref:=read.Owner().Domain.Ref(read.Epoch(),a)
	if err:=extension.ValidateAutoincIdentity(read,p,ref);err!=nil { t.Fatal(err) }
	ref.Epoch++
	if err:=extension.ValidateAutoincIdentity(read,p,ref);err==nil { t.Fatal("foreign epoch admitted") }
	other:=engine.New()
	if err:=extension.ValidateAutoincIdentity(other.CatalogRead(),p,read.Owner().Domain.Ref(read.Epoch(),a));err==nil { t.Fatal("foreign engine declaration admitted") }
	for _,q:=range []string{"ALTER FUNCTION autoinc() RENAME TO real_helper","ALTER FUNCTION real_helper() SET SCHEMA moved","ALTER EXTENSION autoinc DROP FUNCTION moved.real_helper()"} { enginetest.RoleExecute(t,e,op,q) }
	read=e.CatalogRead();moved:=read.MustProcedure(a)
	if symbol,err:=extension.ProcedureSymbol(read,moved);err!=nil || symbol!="autoinc" || !catalog.SupportsDMLTrigger(moved) { t.Fatal("relabel/detach lost authority",symbol,err) }
	enginetest.RoleExecute(t,e,op,"ALTER EXTENSION autoinc ADD FUNCTION moved.real_helper()")
	read=e.CatalogRead()
	if !catalog.SupportsDMLTrigger(read.MustProcedure(a)) { t.Fatal("reattached declaration rejected") }
}
