package pgcatalog_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func statioUserTableReplacement(t *testing.T) {
	e:=engine.New()
	address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:33600}
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
		state:=catalog.CloneState(auth.Catalog.State())
		d:=&catalog.Relation{ObjectAddress:address,ObjectNamespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},ObjectName:"user_generation_candidate",ObjectRevision:1,Owner:auth.State.CurrentUser,RelationKind:catalog.TableRelation}
		state.SetDescriptor(address,d)
		auth.Recapture(e.Catalog.Read(state))
		in:=statioSysTableInput(t,e,auth,pgcatalog.StatioUserTablesOID)
		in.Keep=pgcatalog.Keep{Column:0,Test:func(v sqltype.Value)bool{id,_:=v.OID();return id==uint32(address.ObjectID)}}
		rows:=statioSysUnitRows(t,in);if len(rows)!=1 {t.Fatal(rows)}
		return rows[0],in
	}
	old,input:=capture()
	wantOld:=statioSysTableUnitRow(t,input,input.Catalog.MustRelation(address),"public",2,5)
	if !reflect.DeepEqual(old,wantOld) {t.Fatal("nonzero USER generation",old,wantOld)}
	if err:=engine.Reset(t.Context(),e);err!=nil {t.Fatal(err)}
	nextRef:=e.Catalog.Domain.Ref(e.CatalogRead().Epoch(),address)
	if oldRef==nextRef {t.Fatal("replacement reused qualified identity")}
	late.Flush(&e.Statistics,time.Now(),true)
	fresh,nextInput:=capture()
	wantFresh:=statioSysTableUnitRow(t,nextInput,nextInput.Catalog.MustRelation(address),"public",0,0)
	if !reflect.DeepEqual(fresh,wantFresh) || !reflect.DeepEqual(old,wantOld) {t.Fatal("retired category-qualified facts or retained row mutated",fresh,old)}
}
