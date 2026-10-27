package core_test

import (
	"strconv"
	"sync"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestRelationSizeRuntimeAggregation(t *testing.T) {
	e,r:=relationSizeOwnerFixture(t)
	s:=tablespaceInfoUnitScope(t,e)
	host:=core.HostOf(s)
	int8:=enginetest.Usage(e,sqltype.Int8).Type
	name:=enginetest.Usage(e,sqltype.Name).Type
	database:=e.CatalogRead().MustDatabase(e.Catalog.DatabaseAddress())
	measure:=func(kind functions.SizeKind,target string) int64 {
		t.Helper();value,err:=host.SizeInquiry(kind,sqltype.String(name,target),"main",int8)
		if err!=nil{t.Fatal(err)};n,err:=value.Integer();if err!=nil{t.Fatal(err)};return n
	}
	dbBefore:=measure(functions.DatabaseSize,database.Name())
	defaultBefore:=measure(functions.TablespaceSize,"pg_default")
	globalBefore:=measure(functions.TablespaceSize,"pg_global")
	value,_:=sqltype.Integer(enginetest.Usage(e,sqltype.Int4).Type,1)
	e.Mu.Lock()
	e.Storage.Relations[r.Address()].Versions=[]storage.RowVersion{{Row:1,Version:1,Values:[]sqltype.Value{value}}}
	sharedAddress:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:1260}
	shared,found:=e.Catalog.Current().Relation(sharedAddress)
	if !found||!catalog.SharedRelation(shared){e.Mu.Unlock();t.Fatal("missing shared catalog authority")}
	// A genuine stored root is charged; virtual catalog row presentation alone
	// does not create one. Database accounting must still exclude shared roots.
	e.Storage.InstallRelationLocked(sharedAddress,&storage.Relation{})
	e.Mu.Unlock()
	if got:=measure(functions.DatabaseSize,database.Name());got!=dbBefore+49{t.Fatal("database shared exclusion",got,dbBefore)}
	if got:=measure(functions.TablespaceSize,"pg_default");got!=defaultBefore+49{t.Fatal("default placement",got,defaultBefore)}
	if got:=measure(functions.TablespaceSize,"pg_global");got!=globalBefore+64{t.Fatal("shared allocation",got,globalBefore)}
}

func TestRelationSizeConcurrentCapture(t *testing.T) {
	e,r:=relationSizeOwnerFixture(t)
	s:=tablespaceInfoUnitScope(t,e)
	host:=core.HostOf(s)
	int8:=enginetest.Usage(e,sqltype.Int8).Type
	arg:=sqltype.String(enginetest.Usage(e,sqltype.Regclass).Type,strconv.FormatUint(uint64(r.Address().ObjectID),10))
	value,_:=sqltype.Integer(enginetest.Usage(e,sqltype.Int4).Type,1)
	one:=[]storage.RowVersion{{Row:1,Version:1,Values:[]sqltype.Value{value}}}
	two:=[]storage.RowVersion{one[0],{Row:2,Version:2,Values:[]sqltype.Value{value}}}
	e.Mu.Lock();root:=e.Storage.Relations[r.Address()];root.Versions=one;e.Mu.Unlock()
	var wg sync.WaitGroup
	start:=make(chan struct{})
	wg.Add(1)
	go func(){defer wg.Done();<-start;for j:=range 128{e.Mu.Lock();if j%2==0{root.Versions=two}else{root.Versions=one};e.Mu.Unlock()}}()
	close(start)
	for range 128 {
		got,err:=host.SizeInquiry(functions.TableSize,arg,"main",int8)
		if err!=nil{wg.Wait();t.Fatal(err)}
		n,err:=got.Integer()
		if err!=nil||n!=113&&n!=153{wg.Wait();t.Fatal("incoherent root capture",n,err)}
	}
	wg.Wait()
	if len(one)!=1||len(two)!=2||one[0].Values[0]!=value{t.Fatal("capture mutated immutable inputs")}
}
