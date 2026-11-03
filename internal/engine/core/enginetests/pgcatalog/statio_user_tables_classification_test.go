package pgcatalog_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatioUserTablesClassification(t *testing.T) {
	f:=newDefaultUnitFixture(t);f.actor(f.reader)
	base:=f.e.CatalogRead();state:=catalog.CloneState(f.e.Catalog.State)
	namespaces:=map[string]ids.ObjectAddress{}
	for _,descriptor:=range base.Descriptors() {if ns,ok:=descriptor.(*catalog.Namespace);ok {namespaces[ns.Name()]=ns.Address()}}
	cases:=[]struct{schema string;system bool}{{"pg_catalog",true},{"information_schema",true},{"pg_toast",true},{"pg_toast_42",true},{"pg_toasty",true},{"PG_TOAST",false},{"Pg_Catalog",false},{"information_schema_extra",false},{"public",false},{"xpg_toast",false},{"pg_temp_3",false}}
	relations:=make([]*catalog.Relation,len(cases));var writer statistics.Writer
	for i,c:=range cases {
		ns,ok:=namespaces[c.schema];if !ok {ns=ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.OID(33000+i)};d:=&catalog.Namespace{ObjectAddress:ns,ObjectName:c.schema,ObjectRevision:1,Owner:f.owner,ACL:authorization.ACLValue{Explicit:true}};state.SetDescriptor(ns,d);state.BindName(catalog.DescriptorNameKey(d),ns);namespaces[c.schema]=ns}
		kind:=catalog.TableRelation;if i==1 {kind=catalog.MaterializedViewRelation}
		d:=&catalog.Relation{ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(33100+i)},ObjectNamespace:ns,ObjectName:fmt.Sprintf("user_table_unit_%d",i),ObjectRevision:1,Owner:f.owner,RelationKind:kind,Security:catalog.RelationSecurity{ACL:authorization.ACLValue{Explicit:true},RLSEnabled:true,RLSForced:true}}
		state.SetDescriptor(d.Address(),d);state.BindName(catalog.DescriptorNameKey(d),d.Address());relations[i]=d
		ref:=f.e.Catalog.Domain.Ref(base.Epoch(),d.Address())
		writer.Add(&f.e.Statistics,statistics.Key{Object:ref,Category:statistics.Relation},statistics.Fact{BlocksFetched:7,BlocksHit:5})
		writer.Add(&f.e.Statistics,statistics.Key{Object:ref,Category:statistics.IO},statistics.Fact{BlocksFetched:900,BlocksHit:100,IndexBlocksRead:700,IndexBlocksHit:800})
		for j:=range 2 {a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(33200+i*2+j)};idx:=&catalog.Relation{ObjectAddress:a,ObjectNamespace:ns,ObjectName:fmt.Sprintf("user_table_unit_index_%d_%d",i,j),ObjectRevision:1,Owner:f.owner,RelationKind:catalog.IndexRelation,SQLIndex:&catalog.IndexDefinition{Parent:d.Address(),Keys:[]catalog.IndexKey{{Attribute:1}}}};state.SetDescriptor(a,idx);r:=f.e.Catalog.Domain.Ref(base.Epoch(),a);writer.Add(&f.e.Statistics,statistics.Key{Object:r,Category:statistics.Relation},statistics.Fact{BlocksFetched:100,BlocksHit:90,IndexBlocksRead:int64(2+j*2),IndexBlocksHit:int64(3+j*2)});writer.Add(&f.e.Statistics,statistics.Key{Object:r,Category:statistics.IO},statistics.Fact{IndexBlocksRead:900,IndexBlocksHit:900})}
	}
	for i,kind:=range []catalog.RelationKind{catalog.PartitionedRelation,catalog.ViewRelation,catalog.SequenceRelation,catalog.ForeignRelation,catalog.CompositeRelation,catalog.IndexRelation} {
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(33300+i)}
		d:=&catalog.Relation{ObjectAddress:a,ObjectNamespace:namespaces["public"],ObjectName:fmt.Sprintf("user_table_unit_decoy_%d",i),ObjectRevision:1,Owner:f.owner,RelationKind:kind}
		if kind==catalog.IndexRelation { d.SQLIndex=&catalog.IndexDefinition{Parent:relations[0].Address(),Keys:[]catalog.IndexKey{{Attribute:1}}} }
		state.SetDescriptor(a,d)
	}
	writer.Flush(&f.e.Statistics,time.Now(),true)
	auth:=dtpUnitAuthority(t,f,f.e.Catalog.Read(state))
	denied:=relations[5]
	if err:=auth.Check(access.Requirement{Object:auth.Ref(denied.Address()),Privileges:authorization.SelectPrivilege});err==nil {t.Fatal("ACL fixture admits table")}
	if err:=auth.CanUse(auth.Ref(denied.Namespace()));err==nil {t.Fatal("ACL fixture admits namespace")}
	input:=statioSysTableInput(t,f.e,auth,pgcatalog.StatioUserTablesOID)
	input.Keep=pgcatalog.Keep{Column:2,Test:func(v sqltype.Value)bool{s,_:=v.Text();return strings.HasPrefix(s,"user_table_unit_")}}
	held:=statioSysUnitRows(t,input);got:=statioSysTableUnitBag(t,held)
	allInput:=statioSysTableInput(t,f.e,auth,pgcatalog.StatioAllTablesOID);allInput.Keep=input.Keep
	all:=statioSysTableUnitBag(t,statioSysUnitRows(t,allInput));want:=map[ids.OID][]sqltype.Value{}
	for i,c:=range cases {d:=relations[i];row:=statioSysTableUnitRow(t,input,d,c.schema,2,5,6,8);if !reflect.DeepEqual(all[d.Address().ObjectID],row) {t.Fatal("Relation category and index-specific arithmetic",c,all[d.Address().ObjectID],row)};if !c.system {want[d.Address().ObjectID]=row}}
	sysInput:=statioSysTableInput(t,f.e,auth,pgcatalog.StatioSysTablesOID);sysInput.Keep=input.Keep
	sys:=statioSysTableUnitBag(t,statioSysUnitRows(t,sysInput))
	for id,row:=range want {if !reflect.DeepEqual(got[id],row) || sys[id]!=nil {t.Fatal("independent USER arithmetic and named SYS exclusion",id,got[id],row,sys[id])}}
	if len(all)!=len(cases) || !reflect.DeepEqual(got,want) {t.Fatal("exact classification",got,want)}
	changed:=catalog.CloneState(state)
	rename:=*relations[5]
	rename.ObjectName="user_table_unit_renamed"
	rename.ObjectRevision=rename.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[5]))
	changed.SetDescriptor(rename.Address(),&rename)
	changed.BindName(catalog.DescriptorNameKey(&rename),rename.Address())
	move:=*relations[6]
	move.ObjectNamespace=namespaces["pg_toasty"]
	move.ObjectRevision=move.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[6]))
	changed.SetDescriptor(move.Address(),&move)
	changed.BindName(catalog.DescriptorNameKey(&move),move.Address())
	changed.UnbindName(catalog.DescriptorNameKey(relations[7]))
	changed.RemoveDescriptor(relations[7].Address()) // Published nonzero history intentionally remains.
	newRelation:=*relations[7]
	newRelation.ObjectAddress.ObjectID=33400
	newRelation.ObjectRevision=1
	changed.SetDescriptor(newRelation.Address(),&newRelation)
	changed.BindName(catalog.DescriptorNameKey(&newRelation),newRelation.Address())
	auth.Recapture(f.e.Catalog.Read(changed))
	freshInput:=statioSysTableInput(t,f.e,auth,pgcatalog.StatioUserTablesOID);freshInput.Keep=input.Keep
	fresh:=statioSysTableUnitBag(t,statioSysUnitRows(t,freshInput))
	next:=map[ids.OID][]sqltype.Value{};for id,row:=range want {next[id]=row};next[rename.Address().ObjectID]=statioSysTableUnitRow(t,input,&rename,"PG_TOAST",2,5,6,8);delete(next,move.Address().ObjectID);delete(next,relations[7].Address().ObjectID);next[newRelation.Address().ObjectID]=statioSysTableUnitRow(t,input,&newRelation,"information_schema_extra",0,0)
	if !reflect.DeepEqual(fresh,next) || !reflect.DeepEqual(statioSysTableUnitBag(t,held),want) {t.Fatal("current descriptor lifetime and retained immutable scalars",fresh,next)}
	// Move an actual excluded identity into the admitted namespace without
	// changing its qualified statistics identity or its nonzero history.
	movedIn:=move
	movedIn.ObjectNamespace=namespaces["Pg_Catalog"]
	movedIn.ObjectRevision=movedIn.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(&move))
	changed.SetDescriptor(movedIn.Address(),&movedIn)
	changed.BindName(catalog.DescriptorNameKey(&movedIn),movedIn.Address())
	auth.Recapture(f.e.Catalog.Read(changed))
	freshInput=statioSysTableInput(t,f.e,auth,pgcatalog.StatioUserTablesOID);freshInput.Keep=input.Keep
	fresh=statioSysTableUnitBag(t,statioSysUnitRows(t,freshInput));next[movedIn.Address().ObjectID]=statioSysTableUnitRow(t,input,&movedIn,"Pg_Catalog",2,5,6,8)
	if !reflect.DeepEqual(fresh,next) {t.Fatal("schema move admission",fresh,next)}
	t.Run("replacement_generation", statioUserTableReplacement)
}
