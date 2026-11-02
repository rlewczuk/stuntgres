package rolecmds_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Only the first actual private provider-validation call stops. A second copy
// traverses the normal replacement and publication path without an artificial
// mutex shared with the first call.
type templateConcurrentProvider struct {
	catalog.Provider
	entered chan struct{}
	release chan struct{}
	first atomic.Bool
}

func (p *templateConcurrentProvider) Validate(v catalog.Read,d catalog.Descriptor) error {
	if d.Name()=="t"&&p.first.CompareAndSwap(false,true) {
		close(p.entered)
		<-p.release
	}
	return p.Provider.Validate(v,d)
}

type templateAdmissionWaitContext struct {
	context.Context
	once sync.Once
	waiting chan struct{}
}

func (c *templateAdmissionWaitContext) Done() <-chan struct{} {
	c.once.Do(func(){close(c.waiting)})
	return c.Context.Done()
}

func TestDatabaseTemplateConcurrentPublication(t *testing.T) {
	for _,sameName:=range []bool{false,true} {
		t.Run(map[bool]string{false:"different_names",true:"same_name"}[sameName],func(t *testing.T){
			e,source:=templateUnitSource(t)
			ctx,cancel:=context.WithTimeout(t.Context(),enginetest.Liveness);defer cancel()
			provider:=&templateConcurrentProvider{Provider:e.Catalog.Providers[ids.RelationClassID],entered:make(chan struct{}),release:make(chan struct{})}
			e.Catalog.Providers[ids.RelationClassID]=provider
			var release sync.Once;unblock:=func(){release.Do(func(){close(provider.release)})};defer unblock()
			left,leftDone,err:=enginetest.Operation(e,ctx);if err!=nil {t.Fatal(err)};defer leftDone()
			right,rightDone,err:=enginetest.Operation(e,ctx);if err!=nil {t.Fatal(err)};defer rightDone()
			statement:=func(name string)*parser.CreateDatabaseStmt{return &parser.CreateDatabaseStmt{Name:name,Options:[]parser.DatabaseOption{{Name:"template",Value:source.DatabaseName}}}}
			first:=make(chan error,1)
			go func(){_,err:=e.Execute(left,statement("concurrent_left"),0,nil);first<-err}()
			joined:=false
			defer func(){unblock();if !joined {<-first}}()
			select {case <-provider.entered:case <-ctx.Done():t.Fatal("private source copy not reached",ctx.Err())}
			// The replacement fence is real, not merely an admission-reader count.
			if source.ReplacementMu.TryLock(){source.ReplacementMu.Unlock();t.Fatal("copy lost source replacement protection")}
			waitBase,waitCancel:=context.WithCancel(ctx);defer waitCancel()
			waitCtx:=&templateAdmissionWaitContext{Context:waitBase,waiting:make(chan struct{})}
			admitted:=make(chan error,1)
			go func(){a,err:=e.AdmitDatabase(waitCtx,e.Roles().LookupRef("postgres"),core.DatabaseSelector{Name:source.DatabaseName,Explicit:true});if a!=nil {a.Release()};admitted<-err}()
			admissionJoined:=false
			defer func(){waitCancel();if !admissionJoined {<-admitted}}()
			select {case <-waitCtx.waiting:case <-ctx.Done():t.Fatal("source exclusion wait not reached",ctx.Err())}
			// An unrelated target remains immediately admissible.
			a,err:=e.AdmitDatabase(ctx,e.Roles().LookupRef("postgres"),core.DatabaseSelector{});if err!=nil {t.Fatal(err)};a.Release()
			name:="concurrent_right";if sameName {name="concurrent_left"}
			if _,err:=e.Execute(right,statement(name),0,nil);err!=nil {t.Fatal("compatible copy blocked or rejected",err)}
			frontier:=e.Cluster.NextOID
			select {case err:=<-admitted:admissionJoined=true;t.Fatal("connection crossed remaining copy lease",err);default:}
			unblock()
			err=<-first;joined=true
			if sameName {var state interface{SQLState()string};if !errors.As(err,&state)||state.SQLState()!="42P04" {t.Fatal("same-name loser",err)}} else if err!=nil {t.Fatal("independent copy lost to shared-root publication",err)}
			if sameName&&e.Cluster.NextOID!=frontier {t.Fatal("losing copy consumed a database identity")}
			if err:=<-admitted;err!=nil {t.Fatal("last copy did not release source admission",err)};admissionJoined=true
			if !source.ReplacementMu.TryLock(){t.Fatal("copy leaked replacement read protection")};source.ReplacementMu.Unlock()
			e.Catalog.Providers[ids.RelationClassID]=provider.Provider
			if e.Cluster.Targets["concurrent_left"]==nil||(!sameName&&e.Cluster.Targets["concurrent_right"]==nil) {t.Fatal("completed copy missing")}
		})
	}
}

func TestDatabaseTemplateSourceEpoch(t *testing.T) {
	e,source:=templateUnitSource(t)
	ctx,cancel:=context.WithTimeout(t.Context(),enginetest.Liveness);defer cancel()
	holder:=enginetest.StartSession(t,e,nil)
	tx:=enginetest.TransactionBegin(t,holder)
	hold:=enginetest.TransactionOperation(t,holder)
	if _,err:=e.Execute(hold,primaryKeyUnitCreate("epoch_holder","epoch_holder_pk"),0,nil);err!=nil {hold.Done();t.Fatal(err)}
	hold.Done()
	principal:=e.Roles().LookupRef("postgres")
	resource:=lock.Resource{ObjectRef:e.Cluster.RoleDomain.Ref(1,ids.ObjectAddress{ClassID:ids.RoleClassID,ObjectID:ids.OID(principal.ID())})}
	if err:=e.Locks.Acquire(ctx,holder.Transactions.Ticket,resource,lock.AccessExclusive);err!=nil {t.Fatal(err)}
	op,done,err:=enginetest.Operation(e,ctx);if err!=nil {t.Fatal(err)};defer done()
	result:=make(chan error,1)
	go func(){_,err:=e.Execute(op,&parser.CreateDatabaseStmt{Name:"epoch_clone",Options:[]parser.DatabaseOption{{Name:"template",Value:source.DatabaseName}}},0,nil);result<-err}()
	joined:=false
	defer func(){cancel();if !joined {<-result}}()
	enginetest.LockPending(t,e,1)
	pending:=e.Locks.Inspect().Resources[resource]
	if pending==nil||len(pending.Pending)!=1||pending.Pending[0].Mode!=lock.AccessShare {t.Fatal("copy did not wait on its actual owner principal")}
	old:=source.CatalogRead()
	descriptor:=old.MustDatabase(source.Catalog.DatabaseAddress())
	if err:=engine.Reset(ctx,source);err!=nil {t.Fatal(err)}
	current:=source.CatalogRead()
	if current.Epoch()==old.Epoch()||!catalog.DatabaseAuthorityCompatible(current.MustDatabase(source.Catalog.DatabaseAddress()),descriptor) {t.Fatal("reset did not distinguish epoch from retained attachment authority")}
	unchanged:=databaseCreateUnitUnchanged(t,e)
	enginetest.TransactionEnd(t,holder,tx,false)
	err=<-result;joined=true
	var conflict catalog.Conflict
	if !errors.As(err,&conflict) {t.Fatal("pending copy did not reject its original source epoch",err)}
	unchanged()
	a,err:=e.AdmitDatabase(ctx,principal,core.DatabaseSelector{Name:source.DatabaseName,Explicit:true});if err!=nil {t.Fatal(err)};a.Release()
	retry,retryDone,err:=enginetest.Operation(e,ctx);if err!=nil {t.Fatal(err)};defer retryDone()
	if _,err:=e.Execute(retry,&parser.CreateDatabaseStmt{Name:"epoch_clone",Options:[]parser.DatabaseOption{{Name:"template",Value:source.DatabaseName}}},0,nil);err!=nil {t.Fatal("same-name retry after source replacement",err)}
}
