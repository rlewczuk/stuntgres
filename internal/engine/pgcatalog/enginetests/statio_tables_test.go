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

type statioTableFacts struct {
	pgcatalog.Runtime
	heap, indexes map[ids.ObjectAddress]pgcatalog.IndexBlockFact
	fetched map[ids.ObjectAddress]int
}
func (r *statioTableFacts) TableBlockStatistics(a ids.ObjectAddress) pgcatalog.IndexBlockFact {r.fetched[a]++;return r.heap[a]}
func (r *statioTableFacts) IndexBlockStatistics(a ids.ObjectAddress) pgcatalog.IndexBlockFact {r.fetched[a]++;return r.indexes[a]}

func TestStatioAllTablesProjection(t *testing.T) {
	f:=newDefaultUnitFixture(t)
	state:=catalog.CloneState(f.e.Catalog.State)
	integer,_:=f.e.Catalog.Read(state).LookupType(sqltype.Int4)
	addresses:=map[string]ids.ObjectAddress{}
	for i,tc:=range []struct{name string;kind catalog.RelationKind}{
		{"heap",catalog.TableRelation},{"no_index",catalog.TableRelation},{"zero_indexes",catalog.TableRelation},
		{"materialized",catalog.MaterializedViewRelation},{"parent",catalog.PartitionedRelation},
		{"view",catalog.ViewRelation},{"foreign",catalog.ForeignRelation},{"sequence",catalog.SequenceRelation},{"composite",catalog.CompositeRelation},
	} {
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(21500+i)}
		d:=&catalog.Relation{ObjectAddress:a,ObjectNamespace:f.schema,ObjectName:"statio_projection_"+tc.name,ObjectRevision:1,Owner:f.owner,RelationKind:tc.kind,Attributes:[]int32{1},Columns:[]catalog.Column{{Name:"k",Usage:integer.Usage()}}}
		state.SetDescriptor(a,d);addresses[tc.name]=a
	}
	indexes:=map[ids.ObjectAddress]pgcatalog.IndexBlockFact{}
	for i,tc:=range []struct{parent string;fact pgcatalog.IndexBlockFact}{
		{"heap",pgcatalog.IndexBlockFact{Read:2,Hit:3}},{"heap",pgcatalog.IndexBlockFact{Read:4,Hit:5}},
		{"zero_indexes",pgcatalog.IndexBlockFact{}},{"zero_indexes",pgcatalog.IndexBlockFact{}},
		{"parent",pgcatalog.IndexBlockFact{Read:99,Hit:99}},
	} {
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(21600+i)}
		d:=&catalog.Relation{ObjectAddress:a,ObjectNamespace:f.schema,ObjectName:"statio_projection_index",ObjectRevision:1,Owner:f.owner,RelationKind:catalog.IndexRelation,SQLIndex:&catalog.IndexDefinition{Parent:addresses[tc.parent],Keys:[]catalog.IndexKey{{Attribute:1}}}}
		state.SetDescriptor(a,d);indexes[a]=tc.fact
	}
	view:=f.e.Catalog.Read(state);auth:=dtpUnitAuthority(t,f,view)
	source:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatioAllTablesOID})
	if !source.VirtualView || source.RowType!=12176 || len(source.Columns)!=11 {t.Fatal("existing row identity and exact shape",source)}
	for i,c:=range source.Columns {
		collation:=uint32(0);if i==1||i==2 {collation=950}
		if !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod!=-1 || c.Usage.Collation.ID!=collation || c.OriginTable!=12174 || c.OriginAttribute!=int16(i+1) {t.Fatal("exact attribute metadata",i,c)}
	}
	base:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:f.e,Auth:auth})
	runtime:=&statioTableFacts{Runtime:base,heap:map[ids.ObjectAddress]pgcatalog.IndexBlockFact{addresses["heap"]:{Read:2,Hit:5}},indexes:indexes,fetched:map[ids.ObjectAddress]int{}}
	in:=pgcatalog.Input{Context:t.Context(),Catalog:view,Roles:auth.Roles,Owner:view.Owner(),Ref:view.Owner().Domain.Ref(view.Epoch(),source.Address()),Source:source,Runtime:runtime,Keep:pgcatalog.Keep{Column:2,Test:func(v sqltype.Value) bool {s,_:=v.Text();return strings.HasPrefix(s,"statio_projection_")}}}
	produce,ok:=pgcatalog.ProducerFor(pgcatalog.StatioAllTablesOID);if !ok {t.Fatal("missing registered source")}
	rows,err:=produce(in);if err!=nil {t.Fatal(err)}
	want:=map[string][]any{
		"statio_projection_heap":{int64(2),int64(5),int64(6),int64(8),nil,nil,nil,nil},
		"statio_projection_no_index":{int64(0),int64(0),nil,nil,nil,nil,nil,nil},
		"statio_projection_zero_indexes":{int64(0),int64(0),int64(0),int64(0),nil,nil,nil,nil},
		"statio_projection_materialized":{int64(0),int64(0),nil,nil,nil,nil,nil,nil},
	}
	if len(rows)!=len(want) {t.Fatal("r/m only",len(rows),rows)}
	for _,row:=range rows {name,_:=row[2].Text();expected,ok:=want[name];if !ok {t.Fatal("fabricated/excluded identity",name)};got:=make([]any,8);for i,v:=range row[3:] {if !v.IsNull() {n,err:=v.Integer();if err!=nil {t.Fatal(err)};got[i]=n}};if !reflect.DeepEqual(got,expected) {t.Fatal(name,got,expected)};delete(want,name)}
	if len(want)!=0 {t.Fatal("missing relations",want)}
	if runtime.fetched[addresses["parent"]]!=0 {t.Fatal("excluded parent fetched")}
	for _,column:=range []int{0,1,2} {
		runtime.fetched=map[ids.ObjectAddress]int{}
		in.Keep=pgcatalog.Keep{Column:column,Test:func(sqltype.Value)bool{return false}}
		rows,err:=produce(in);if err!=nil || len(rows)!=0 || len(runtime.fetched)!=0 {t.Fatal("Keep before any reader demand",column,rows,err,runtime.fetched)}
	}
	if !reflect.DeepEqual(pgcatalog.KeyColumns(pgcatalog.StatioAllTablesOID),[]int{0,1,2}) {t.Fatal("identity pushdown")}
	ctx,cancel:=context.WithCancel(t.Context());cancel();in.Context=ctx
	if _,err:=produce(in);!errors.Is(err,context.Canceled) {t.Fatal("producer cancellation",err)}
	user := view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatioUserTablesOID})
	if user.Name()!="pg_statio_user_tables" || !user.VirtualView || user.RowType!=12185 || len(user.Columns)!=11 {t.Fatal("registered USER identity/shape",user)}
	if _,ok:=pgcatalog.ProducerFor(user.Address().ObjectID);!ok || !reflect.DeepEqual(pgcatalog.KeyColumns(user.Address().ObjectID),[]int{0,1,2}) {t.Fatal("registered USER producer/keys")}
	for _,tc:=range []struct{name string;system bool}{{"pg_catalog",true},{"information_schema",true},{"pg_toastlike",true},{"pg_toast_temp_7",true},{"public",false},{"pg_temp_7",false},{"PG_TOAST",false},{"xpg_toast",false}} {if statioSystemNamespace(tc.name)!=tc.system {t.Fatal("released partition",tc)}}
}
