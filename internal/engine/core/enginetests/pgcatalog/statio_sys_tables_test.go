package pgcatalog_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func statioSysTableInput(t *testing.T,e *core.Engine,auth core.AuthorizationView,oid ids.OID) pgcatalog.Input {
	t.Helper()
	read:=auth.Catalog
	a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:oid}
	d:=read.MustRelation(a)
	if len(d.Columns)!=11 || !d.VirtualView {t.Fatal("eleven-column builtin",d)}
	return pgcatalog.Input{Context:context.Background(),Catalog:read,Roles:auth.Roles,Owner:read.Owner(),Ref:auth.Ref(a),Source:d,Runtime:core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:auth.Session.Settings})}
}

func statioSysTableUnitRow(t *testing.T,in pgcatalog.Input,d *catalog.Relation,schema string,counts ...int64) []sqltype.Value {
	t.Helper()
	oid,err:=sqltype.OIDValue(in.Source.Columns[0].Usage.Type,uint32(d.Address().ObjectID));if err!=nil {t.Fatal(err)}
	row:=make([]sqltype.Value,11)
	row[0],row[1],row[2]=oid,sqltype.String(in.Source.Columns[1].Usage.Type,schema),sqltype.String(in.Source.Columns[2].Usage.Type,d.Name())
	for i:=3;i<11;i++ { row[i]=sqltype.Null(in.Source.Columns[i].Usage.Type);if i-3<len(counts) {row[i],err=sqltype.Integer(in.Source.Columns[i].Usage.Type,counts[i-3]);if err!=nil {t.Fatal(err)}} }
	return row
}

func statioSysTableUnitBag(t *testing.T,rows [][]sqltype.Value) map[ids.OID][]sqltype.Value {
	t.Helper();bag:=map[ids.OID][]sqltype.Value{}
	for _,row:=range rows { if len(row)!=11 {t.Fatal(row)};oid,err:=row[0].OID();if err!=nil || bag[ids.OID(oid)]!=nil {t.Fatal("unique identities",row,err)};bag[ids.OID(oid)]=row }
	return bag
}

func TestPGStatioSysTablesClassification(t *testing.T) {
	f:=newDefaultUnitFixture(t);f.actor(f.reader)
	base:=f.e.CatalogRead();state:=catalog.CloneState(f.e.Catalog.State)
	namespaces:=map[string]ids.ObjectAddress{}
	for _,descriptor:=range base.Descriptors() {if ns,ok:=descriptor.(*catalog.Namespace);ok {namespaces[ns.Name()]=ns.Address()}}
	cases:=[]struct{schema string;system bool}{{"pg_catalog",true},{"information_schema",true},{"pg_toast",true},{"pg_toast_42",true},{"pg_toasty",true},{"PG_TOAST",false},{"Pg_Catalog",false},{"information_schema_extra",false},{"public",false},{"xpg_toast",false},{"pg_temp_3",false}}
	relations:=make([]*catalog.Relation,len(cases));var writer statistics.Writer
	for i,c:=range cases {
		ns,ok:=namespaces[c.schema];if !ok {ns=ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.OID(33000+i)};d:=&catalog.Namespace{ObjectAddress:ns,ObjectName:c.schema,ObjectRevision:1,Owner:f.owner,ACL:authorization.ACLValue{Explicit:true}};state.SetDescriptor(ns,d);state.BindName(catalog.DescriptorNameKey(d),ns);namespaces[c.schema]=ns}
		kind:=catalog.TableRelation;if i==1 {kind=catalog.MaterializedViewRelation}
		d:=&catalog.Relation{ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(33100+i)},ObjectNamespace:ns,ObjectName:fmt.Sprintf("sys_table_unit_%d",i),ObjectRevision:1,Owner:f.owner,RelationKind:kind,Security:catalog.RelationSecurity{ACL:authorization.ACLValue{Explicit:true},RLSEnabled:true,RLSForced:true}}
		state.SetDescriptor(d.Address(),d);state.BindName(catalog.DescriptorNameKey(d),d.Address());relations[i]=d
		ref:=f.e.Catalog.Domain.Ref(base.Epoch(),d.Address())
		writer.Add(&f.e.Statistics,statistics.Key{Object:ref,Category:statistics.Relation},statistics.Fact{BlocksFetched:7,BlocksHit:5})
		writer.Add(&f.e.Statistics,statistics.Key{Object:ref,Category:statistics.IO},statistics.Fact{BlocksFetched:900,BlocksHit:100,IndexBlocksRead:700,IndexBlocksHit:800})
		for j:=range 2 {a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(33200+i*2+j)};idx:=&catalog.Relation{ObjectAddress:a,ObjectNamespace:ns,ObjectName:fmt.Sprintf("sys_table_unit_index_%d_%d",i,j),ObjectRevision:1,Owner:f.owner,RelationKind:catalog.IndexRelation,SQLIndex:&catalog.IndexDefinition{Parent:d.Address(),Keys:[]catalog.IndexKey{{Attribute:1}}}};state.SetDescriptor(a,idx);r:=f.e.Catalog.Domain.Ref(base.Epoch(),a);writer.Add(&f.e.Statistics,statistics.Key{Object:r,Category:statistics.Relation},statistics.Fact{BlocksFetched:100,BlocksHit:90,IndexBlocksRead:int64(2+j*2),IndexBlocksHit:int64(3+j*2)});writer.Add(&f.e.Statistics,statistics.Key{Object:r,Category:statistics.IO},statistics.Fact{IndexBlocksRead:900,IndexBlocksHit:900})}
	}
	for i,kind:=range []catalog.RelationKind{catalog.PartitionedRelation,catalog.ViewRelation,catalog.SequenceRelation,catalog.ForeignRelation,catalog.CompositeRelation,catalog.IndexRelation} {
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(33300+i)}
		d:=&catalog.Relation{ObjectAddress:a,ObjectNamespace:namespaces["pg_catalog"],ObjectName:fmt.Sprintf("sys_table_unit_decoy_%d",i),ObjectRevision:1,Owner:f.owner,RelationKind:kind}
		if kind==catalog.IndexRelation { d.SQLIndex=&catalog.IndexDefinition{Parent:relations[0].Address(),Keys:[]catalog.IndexKey{{Attribute:1}}} }
		state.SetDescriptor(a,d)
	}
	writer.Flush(&f.e.Statistics,time.Now(),true)
	auth:=dtpUnitAuthority(t,f,f.e.Catalog.Read(state))
	denied:=relations[3]
	if err:=auth.Check(access.Requirement{Object:auth.Ref(denied.Address()),Privileges:authorization.SelectPrivilege});err==nil {t.Fatal("ACL fixture admits table")}
	if err:=auth.CanUse(auth.Ref(denied.Namespace()));err==nil {t.Fatal("ACL fixture admits namespace")}
	input:=statioSysTableInput(t,f.e,auth,pgcatalog.StatioSysTablesOID)
	input.Keep=pgcatalog.Keep{Column:2,Test:func(v sqltype.Value)bool{s,_:=v.Text();return strings.HasPrefix(s,"sys_table_unit_")}}
	held:=statioSysUnitRows(t,input);got:=statioSysTableUnitBag(t,held)
	allInput:=statioSysTableInput(t,f.e,auth,pgcatalog.StatioAllTablesOID);allInput.Keep=input.Keep
	all:=statioSysTableUnitBag(t,statioSysUnitRows(t,allInput));want:=map[ids.OID][]sqltype.Value{}
	for i,c:=range cases {d:=relations[i];row:=statioSysTableUnitRow(t,input,d,c.schema,2,5,6,8);if !reflect.DeepEqual(all[d.Address().ObjectID],row) {t.Fatal("Relation category and index-specific arithmetic",c,all[d.Address().ObjectID],row)};if c.system {want[d.Address().ObjectID]=row}}
	if len(all)!=len(cases) || !reflect.DeepEqual(got,want) {t.Fatal("exact classification",got,want)}
	changed:=catalog.CloneState(state)
	rename:=*relations[0]
	rename.ObjectName="sys_table_unit_renamed"
	rename.ObjectRevision=rename.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[0]))
	changed.SetDescriptor(rename.Address(),&rename)
	changed.BindName(catalog.DescriptorNameKey(&rename),rename.Address())
	move:=*relations[1]
	move.ObjectNamespace=namespaces["public"]
	move.ObjectRevision=move.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[1]))
	changed.SetDescriptor(move.Address(),&move)
	changed.BindName(catalog.DescriptorNameKey(&move),move.Address())
	changed.UnbindName(catalog.DescriptorNameKey(relations[2]))
	changed.RemoveDescriptor(relations[2].Address()) // Published nonzero history intentionally remains.
	newRelation:=*relations[2]
	newRelation.ObjectAddress.ObjectID=33400
	newRelation.ObjectRevision=1
	changed.SetDescriptor(newRelation.Address(),&newRelation)
	changed.BindName(catalog.DescriptorNameKey(&newRelation),newRelation.Address())
	auth.Recapture(f.e.Catalog.Read(changed))
	freshInput:=statioSysTableInput(t,f.e,auth,pgcatalog.StatioSysTablesOID);freshInput.Keep=input.Keep
	fresh:=statioSysTableUnitBag(t,statioSysUnitRows(t,freshInput))
	next:=map[ids.OID][]sqltype.Value{};for id,row:=range want {next[id]=row};next[rename.Address().ObjectID]=statioSysTableUnitRow(t,input,&rename,"pg_catalog",2,5,6,8);delete(next,move.Address().ObjectID);delete(next,relations[2].Address().ObjectID);next[newRelation.Address().ObjectID]=statioSysTableUnitRow(t,input,&newRelation,"pg_toast",0,0)
	if !reflect.DeepEqual(fresh,next) || !reflect.DeepEqual(statioSysTableUnitBag(t,held),want) {t.Fatal("current descriptor lifetime and retained immutable scalars",fresh,next)}
	// Move an actual excluded identity into the admitted namespace without
	// changing its qualified statistics identity or its nonzero history.
	movedIn:=*relations[5]
	movedIn.ObjectNamespace=namespaces["pg_toasty"]
	movedIn.ObjectRevision=movedIn.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[5]))
	changed.SetDescriptor(movedIn.Address(),&movedIn)
	changed.BindName(catalog.DescriptorNameKey(&movedIn),movedIn.Address())
	auth.Recapture(f.e.Catalog.Read(changed))
	freshInput=statioSysTableInput(t,f.e,auth,pgcatalog.StatioSysTablesOID);freshInput.Keep=input.Keep
	fresh=statioSysTableUnitBag(t,statioSysUnitRows(t,freshInput));next[movedIn.Address().ObjectID]=statioSysTableUnitRow(t,input,&movedIn,"pg_toasty",2,5,6,8)
	if !reflect.DeepEqual(fresh,next) {t.Fatal("schema move admission",fresh,next)}
	t.Run("replacement_generation",func(t *testing.T) {
		e:=engine.New()
		address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:1259}
		oldRef:=e.Catalog.Domain.Ref(e.CatalogRead().Epoch(),address)
		var published,late statistics.Writer
		published.Add(&e.Statistics,statistics.Key{Object:oldRef,Category:statistics.Relation},statistics.Fact{BlocksFetched:7,BlocksHit:5})
		published.Flush(&e.Statistics,time.Now(),true)
		late.Add(&e.Statistics,statistics.Key{Object:oldRef,Category:statistics.Relation},statistics.Fact{BlocksFetched:100,BlocksHit:90})
		capture:=func() ([]sqltype.Value,pgcatalog.Input) {
			t.Helper()
			s:=enginetest.StartSession(t,e,nil)
			op:=enginetest.TransactionOperation(t,s);defer op.Done()
			scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)}
			defer func(){if err:=scope.Finish(nil);err!=nil {t.Error(err)}}()
			e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);e.Mu.Unlock();if err!=nil {t.Fatal(err)}
			in:=statioSysTableInput(t,e,auth,pgcatalog.StatioSysTablesOID)
			in.Keep=pgcatalog.Keep{Column:0,Test:func(v sqltype.Value)bool{id,_:=v.OID();return id==1259}}
			rows:=statioSysUnitRows(t,in);if len(rows)!=1 {t.Fatal(rows)}
			return rows[0],in
		}
		old,input:=capture()
		wantOld:=statioSysTableUnitRow(t,input,input.Catalog.MustRelation(address),"pg_catalog",2,5)
		if !reflect.DeepEqual(old,wantOld) {t.Fatal("nonzero system generation fixture",old,wantOld)}
		if err:=engine.Reset(t.Context(),e);err!=nil {t.Fatal(err)}
		nextRef:=e.Catalog.Domain.Ref(e.CatalogRead().Epoch(),address)
		if oldRef==nextRef {t.Fatal("replacement reused qualified identity")}
		late.Flush(&e.Statistics,time.Now(),true)
		fresh,nextInput:=capture()
		wantFresh:=statioSysTableUnitRow(t,nextInput,nextInput.Catalog.MustRelation(address),"pg_catalog",0,0)
		if !reflect.DeepEqual(fresh,wantFresh) || !reflect.DeepEqual(old,wantOld) {t.Fatal("retired nonzero history leaked into replacement or retained row",fresh,old)}
	})
}
