package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestStatioSysTablesProjection(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	integer, _ := f.e.Catalog.Read(state).LookupType(sqltype.Int4)
	parents := map[string]ids.ObjectAddress{}
	for i, c := range []struct { name string; namespace ids.ObjectAddress; kind catalog.RelationKind }{
		{"heap", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.TableRelation},
		{"no_index", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.TableRelation},
		{"zero_index", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.TableRelation},
		{"materialized", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.MaterializedViewRelation},
		{"user", f.schema, catalog.TableRelation},
		{"parent", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.PartitionedRelation},
		{"view", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.ViewRelation},
		{"foreign", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.ForeignRelation},
		{"sequence", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.SequenceRelation},
		{"composite", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, catalog.CompositeRelation},
	} {
		a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(21700+i)}
		state.SetDescriptor(a, &catalog.Relation{ObjectAddress:a, ObjectNamespace:c.namespace, ObjectName:"sys_projection_"+c.name, ObjectRevision:1, Owner:f.owner, RelationKind:c.kind, Attributes:[]int32{1}, Columns:[]catalog.Column{{Name:"k", Usage:integer.Usage()}}})
		parents[c.name] = a
	}
	indexes := map[ids.ObjectAddress]pgcatalog.IndexBlockFact{}
	for i, c := range []struct { parent string; fact pgcatalog.IndexBlockFact }{{"heap",pgcatalog.IndexBlockFact{Read:2,Hit:3}}, {"heap",pgcatalog.IndexBlockFact{Read:4,Hit:5}}, {"zero_index",pgcatalog.IndexBlockFact{}}, {"user",pgcatalog.IndexBlockFact{Read:90,Hit:90}}} {
		a := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(21800+i)}
		state.SetDescriptor(a,&catalog.Relation{ObjectAddress:a,ObjectNamespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:11},ObjectName:"sys_projection_index",ObjectRevision:1,Owner:f.owner,RelationKind:catalog.IndexRelation,SQLIndex:&catalog.IndexDefinition{Parent:parents[c.parent],Keys:[]catalog.IndexKey{{Attribute:1}}}})
		indexes[a] = c.fact
	}
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t,f,view)
	source := view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatioSysTablesOID})
	if source.Name()!="pg_statio_sys_tables" || !source.VirtualView || source.RowType!=12181 || len(source.Columns)!=11 { t.Fatal("positive selected registration",source) }
	for i,c := range source.Columns {
		collation:=uint32(0); if i==1 || i==2 { collation=950 }
		if !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod!=-1 || c.Usage.Collation.ID!=collation || c.OriginTable!=12179 || c.OriginAttribute!=int16(i+1) { t.Fatal("source-owned metadata",i,c) }
	}
	base:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:f.e,Auth:auth})
	runtime:=&statioTableFacts{Runtime:base,heap:map[ids.ObjectAddress]pgcatalog.IndexBlockFact{parents["heap"]:{Read:2,Hit:5}},indexes:indexes,fetched:map[ids.ObjectAddress]int{}}
	in:=pgcatalog.Input{Context:t.Context(),Catalog:view,Roles:auth.Roles,Owner:view.Owner(),Ref:view.Owner().Domain.Ref(view.Epoch(),source.Address()),Source:source,Runtime:runtime,Keep:pgcatalog.Keep{Column:2,Test:func(v sqltype.Value)bool{s,_:=v.Text();return strings.HasPrefix(s,"sys_projection_")}}}
	produce,ok:=pgcatalog.ProducerFor(pgcatalog.StatioSysTablesOID); if !ok { t.Fatal("selected producer missing") }
	rows,err:=produce(in); if err!=nil { t.Fatal(err) }
	want:=map[string][]any{"sys_projection_heap":{int64(2),int64(5),int64(6),int64(8),nil,nil,nil,nil},"sys_projection_no_index":{int64(0),int64(0),nil,nil,nil,nil,nil,nil},"sys_projection_zero_index":{int64(0),int64(0),int64(0),int64(0),nil,nil,nil,nil},"sys_projection_materialized":{int64(0),int64(0),nil,nil,nil,nil,nil,nil}}
	if len(rows)!=len(want) { t.Fatal("exact admitted kinds",rows) }
	for _,row:=range rows {
		name,_:=row[2].Text(); expected,ok:=want[name]; if !ok { t.Fatal("excluded identity",name) }
		got:=make([]any,8)
		for i,v:=range row[3:] { if v.Type()!=source.Columns[i+3].Usage.Type { t.Fatal("typed NULL/counter",v) }; if !v.IsNull() { n,err:=v.Integer();if err!=nil {t.Fatal(err)};got[i]=n } }
		if !reflect.DeepEqual(got,expected) {t.Fatal(name,got,expected)}
		delete(want,name)
	}
	for a:=range runtime.fetched { if a==parents["user"] || a.ObjectID==21803 {t.Fatal("namespace exclusion before facts",a)} }
	allInput:=in
	allInput.Source=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatioAllTablesOID})
	allInput.Ref=view.Owner().Domain.Ref(view.Epoch(),allInput.Source.Address())
	allProduce,ok:=pgcatalog.ProducerFor(pgcatalog.StatioAllTablesOID);if !ok {t.Fatal("shared ALL producer missing")}
	allRows,err:=allProduce(allInput);if err!=nil || len(allRows)!=5 {t.Fatal("ALL includes the independent user complement",allRows,err)}
	byID:=map[uint32][]sqltype.Value{}
	for _,row:=range allRows {id,err:=row[0].OID();if err!=nil || byID[id]!=nil {t.Fatal("unique ALL identity",row,err)};byID[id]=row}
	for _,row:=range rows {id,_:=row[0].OID();if !reflect.DeepEqual(byID[id],row) {t.Fatal("exact eleven typed ALL/SYS cells",byID[id],row)}}
	user:=byID[uint32(parents["user"].ObjectID)]
	if len(user)!=11 {t.Fatal("actual user complement missing",user)}
	userRead,_:=user[5].Integer();userHit,_:=user[6].Integer()
	if userRead!=90 || userHit!=90 {t.Fatal("independent complement index facts",user)}
	for _,column:=range []int{0,1,2} {
		runtime.fetched=map[ids.ObjectAddress]int{}
		in.Keep=pgcatalog.Keep{Column:column,Test:func(sqltype.Value)bool{return false}}
		rows,err:=produce(in);if err!=nil || len(rows)!=0 || len(runtime.fetched)!=0 {t.Fatal("Keep before heap/index demand",column,rows,err,runtime.fetched)}
	}
	if !reflect.DeepEqual(pgcatalog.KeyColumns(pgcatalog.StatioSysTablesOID),[]int{0,1,2}) {t.Fatal("identity pushdown")}
	ctx,cancel:=context.WithCancel(t.Context());cancel();in.Context=ctx
	if _,err:=produce(in);!errors.Is(err,context.Canceled) {t.Fatal("cancellation",err)}
}
