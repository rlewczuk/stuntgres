package schemacmds_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestLtreeProviderIdentity(t *testing.T) {
	e,_,ext,_:=lqueryProviderInstalled(t,false)
	view:=e.CatalogRead()
	base:=view.MustType(ext.Members[3])
	if len(ext.Members)!=6 || !base.SQLType.IsLtree() || base.Owner!=ext.Owner { t.Fatal("ltree manifest") }
	if err:=(stored.TypeProvider{}).Validate(view,base); err!=nil { t.Fatal(err) }
	array,ok:=view.LookupArray(base.SQLType)
	if !ok || array.Concrete() || array.Element()!=base.SQLType || array.ID()!=base.Array { t.Fatal("companion array") }
	for i,name:=range []string{"ltree_in","ltree_out"} {
		proc:=view.MustProcedure(ext.Members[4+i])
		symbol,err:=extension.ProcedureSymbol(view,proc)
		if err!=nil || symbol!=name { t.Fatal("codec symbol",symbol,err) }
	}
	for _,corrupt:=range []func(*catalog.State,*catalog.Extension){
		func(_ *catalog.State,d *catalog.Extension){ d.Members=d.Members[:3] },
		func(_ *catalog.State,d *catalog.Extension){ d.Members=append(d.Members,d.Members[3]) },
		func(_ *catalog.State,d *catalog.Extension){ d.Members[0],d.Members[3]=d.Members[3],d.Members[0] },
		func(s *catalog.State,d *catalog.Extension){ s.RemoveDescriptor(d.Members[4]) },
		func(s *catalog.State,d *catalog.Extension){ p:=*s.MustProcedure(d.Members[4]); p.Result++; s.SetDescriptor(p.Address(),&p) },
		func(s *catalog.State,d *catalog.Extension){ p:=*s.MustProcedure(d.Members[5]); p.Signature="25"; s.SetDescriptor(p.Address(),&p) },
		func(s *catalog.State,d *catalog.Extension){ p:=*s.MustProcedure(d.Members[5]); p.Display.Source="lquery_out"; s.SetDescriptor(p.Address(),&p) },
		func(s *catalog.State,d *catalog.Extension){ p:=*s.MustProcedure(d.Members[4]); p.Implementation=extension.StockIntarray; s.SetDescriptor(p.Address(),&p) },
		func(s *catalog.State,d *catalog.Extension){ p:=*s.MustProcedure(d.Members[4]); p.Extension=ids.ObjectAddress{}; s.SetDescriptor(p.Address(),&p) },
		func(s *catalog.State,d *catalog.Extension){ b:=*s.MustType(d.Members[3]); b.Array=0; s.SetDescriptor(b.Address(),&b) },
		func(s *catalog.State,d *catalog.Extension){ b:=*s.MustType(d.Members[3]); b.SQLType,_=sqltype.Ltree(b.SQLType.ID(),b.Name(),b.SQLType.SQLName()); s.SetDescriptor(b.Address(),&b) },
		func(s *catalog.State,_ *catalog.Extension){ a:=*s.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(base.Array)}); foreign,_:=sqltype.Ltree(base.SQLType.ID(),base.Name(),base.SQLType.SQLName()); a.SQLType=sqltype.LookupArray(a.SQLType.ID(),a.Name(),foreign); s.SetDescriptor(a.Address(),&a) },
	} {
		candidate:=catalog.CloneState(view.State())
		bad:=*ext; bad.Members=slices.Clone(ext.Members); candidate.SetDescriptor(bad.Address(),&bad)
		corrupt(candidate,&bad)
		if err:=extension.StockLtree.ValidateExtension(e.Catalog.Read(candidate),&bad); err==nil { t.Fatal("corrupt ltree graph accepted") }
		if view.MustType(base.Address())!=base { t.Fatal("validation mutated live graph") }
	}
}

func TestLtreeProviderPublication(t *testing.T) {
	e:=engine.New()
	op,done:=enginetest.RoleOperation(t,e,"postgres"); defer done()
	stage:=lqueryProviderStage(t,e,op)
	candidate:=e.StageRead(stage)
	ext:=candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"}))
	candidate.MustProcedure(ext.Members[5]).Result=25
	before:=e.CatalogRead().State()
	if _,err:=e.ValidateCatalogStage(context.Background(),stage); err==nil { t.Fatal("invalid late triple accepted") }
	if e.CatalogRead().State()!=before || e.CatalogRead().NameAddress(ids.NameKey{ClassID:ids.TypeClassID,Namespace:ext.TargetNamespace,Name:"lquery"})!=(ids.ObjectAddress{}) { t.Fatal("published lquery prefix") }
}

func TestLtreeProviderRemap(t *testing.T) {
	source,_,ext,_:=lqueryProviderInstalled(t,false)
	target,_,dest,_:=lqueryProviderInstalled(t,true)
	mapping:=map[ids.ObjectAddress]ids.ObjectAddress{ext.Address():dest.Address()}
	for i,a:=range ext.Members { mapping[a]=dest.Members[i] }
	for _,slot:=range []int{0,3} {
		old:=source.CatalogRead().MustType(ext.Members[slot]); next:=target.CatalogRead().MustType(dest.Members[slot])
		mapping[ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(old.Array)}]=ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(next.Array)}
	}
	candidate:=catalog.CloneState(source.CatalogRead().State())
	if err:=typedef.ImportScalarTypesLocked(candidate,mapping,func()(int64,error){ t.Fatal("scalar allocated enum member"); return 0,nil }); err!=nil { t.Fatal(err) }
	if _,err:=typedef.ReownDomainTypes(candidate,mapping,true,routine.RangeTypeRebinder(candidate,mapping)); err!=nil { t.Fatal(err) }
	typedef.RefreshScalarImportDestinations(candidate)
	base:=source.CatalogRead().MustType(ext.Members[3])
	imported:=candidate.MustType(base.Address())
	if !imported.SQLType.IsLtree() || imported.SQLType==base.SQLType || imported.SQLType.ID()!=sqltype.ID(dest.Members[3].ObjectID) { t.Fatal("destination scalar") }
	for _,text:=range []string{"", "Top.Countries.Europe"} {
		v,err:=base.SQLType.Input(t.Context(),text,-1); if err!=nil { t.Fatal(err) }
		for _,value:=range []sqltype.Value{v,sqltype.Null(base.SQLType)} {
			moved,err:=typedef.ReownDomainDatum(value,imported.SQLType,nil,candidate.ScalarImports(),nil)
			if err!=nil || moved.Type()!=imported.SQLType || moved.IsNull()!=value.IsNull() { t.Fatal("authorized import",err) }
			back,err:=moved.RemapLtree(base.SQLType)
			if err!=nil || back!=value { t.Fatal("immutable carrier sharing",err) }
			if _,err:=value.Retag(imported.SQLType); err==nil { t.Fatal("ordinary retag crossed generation") }
		}
	}
}


func TestLtreeProviderOwners(t *testing.T) {
	e,_,ext,_:=lqueryProviderInstalled(t,false)
	view:=e.CatalogRead()
	metadata,err:=extension.OwnerMetadata(view,e.RoleStore.PublishedRead(&e.Catalog.Owner,view),ext)
	if err!=nil { t.Fatal(err) }
	record,found,err:=extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata,"-- ")))
	if err!=nil || !found { t.Fatal("owner record",err) }
	for _,change:=range []func(*extension.OwnerRecord){
		func(r *extension.OwnerRecord){ r.Members=r.Members[:3] },
		func(r *extension.OwnerRecord){ r.Members=append(r.Members,r.Members[3]) },
		func(r *extension.OwnerRecord){ r.Members[3].Name="lquery" },
		func(r *extension.OwnerRecord){ r.Members[0].Name="ltree" },
		func(r *extension.OwnerRecord){ r.Members[3].Slot=4 },
		func(r *extension.OwnerRecord){ r.Members[5].Signature="lquery" },
		func(r *extension.OwnerRecord){ r.Members[4].Signature="25" },
		func(r *extension.OwnerRecord){ r.Members[3].Kind="procedure"; r.Members[3].Signature="2275" },
	} {
		bad:=record; bad.Members=slices.Clone(record.Members); change(&bad)
		data,err:=json.Marshal(bad); if err!=nil { t.Fatal(err) }
		if _,found,err:=extension.DecodeOwners(extension.OwnersMarker+" "+base64.StdEncoding.EncodeToString(data)); !found || err==nil { t.Fatal("invalid symbolic owner recipe admitted",bad,err) }
	}
	if err:=extension.StockLtree.ValidateExtension(view,ext); err!=nil { t.Fatal("owner decoding mutated live installation",err) }
}
