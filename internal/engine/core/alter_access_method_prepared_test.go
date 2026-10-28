package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestAlterAccessMethodPreparedOwnership(t *testing.T) {
	e:=engine.New();s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{"CREATE TABLE method_owned(id int)","CREATE ACCESS METHOD method_owned_heap TYPE TABLE HANDLER heap_tableam_handler","BEGIN"} {enginetest.SessionSQL(t,s,q)}
	defer enginetest.SessionSQL(t,s,"ROLLBACK")
	cycle:=s.NewProtocolCycle()
	command:=func(fn func(*core.StatementScope) error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
	command(func(scope *core.StatementScope) error {_,err:=scope.ParseProtocol("method_select","SELECT COALESCE(-(7::integer),0) AS folded,id FROM method_owned",nil);return err})
	bind:=func(name string){command(func(scope *core.StatementScope) error {_,err:=scope.BindProtocolPortal(name,"method_select",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err})}
	bind("before")
	registry:=core.PayloadOf(s).PreparedSQL
	definition:=registry.Lookup("method_select")
	owner:=definition.Description.Folds
	analysis:=definition.Description.Analysis
	if owner==nil || analysis==nil || owner.Analysis!=analysis {t.Fatal("missing actual registered definition owner")}
	accepted:=owner.Current()
	if len(accepted.Entries)==0 {t.Fatal("missing accepted unary fold backing")}
	savedEntries:=append(accepted.Entries[:0:0],accepted.Entries...)
	savedDependencies:=append([]catalog.ExpressionDependency(nil),analysis.Dependencies...)
	oldPortal:=registry.Binding("before")
	if oldPortal.Resource==nil || oldPortal.Resource.Root==nil || oldPortal.Folds==nil {t.Fatal("missing accepted graph/group")}
	graph,portalGroup:=oldPortal.Resource.Root,*oldPortal.Folds
	old:=primaryKeyUnitRelation(t,e,"method_owned")
	capture:=func()(catalog.Read,core.PreparedEnvironment){
		op,err:=s.Acquire(t.Context());if err!=nil {t.Fatal(err)};defer op.Done()
		e.Mu.Lock();defer e.Mu.Unlock()
		auth,err:=e.AuthorizationLocked(op);if err!=nil {t.Fatal(err)}
		env,err:=e.PreparedEnvironmentLocked(auth,true);if err!=nil {t.Fatal(err)}
		return auth.Catalog,env
	}
	read,env:=capture()
	for _,address:=range []ids.ObjectAddress{old.Address(),{ClassID:ids.AccessMethodClassID,ObjectID:2},{ClassID:ids.ProcedureClassID,ObjectID:3}} {
		found:=false
		for _,dep:=range analysis.Dependencies {if dep.Ref.Address==address {found=dep.Revision==read.Get(address).Revision()}}
		if !found {t.Fatal("analysis omitted actual relation/method/handler stamp",address)}
	}
	stale,err:=core.BeginFoldPublication(&definition.Description,definition.Body,env,read,nil);if err!=nil || stale==nil {t.Fatal(err)}
	enginetest.SessionSQL(t,s,"ALTER TABLE method_owned SET ACCESS METHOD method_owned_heap")
	current,currentEnv:=capture()
	if analysis.Valid(current,currentEnv) {t.Fatal("old accepted analysis remained valid after genuine method change")}
	if err=stale.Accept(t.Context(),current,currentEnv,current.Epoch());!errors.Is(err,ids.ErrCatalogReplaced) {t.Fatal("stale candidate publication admitted",err)}
	bind("after")
	if registry.Lookup("method_select")!=definition || definition.Description.Folds!=owner || definition.Description.Analysis!=analysis {t.Fatal("Bind replaced the registered definition's original analysis/owner")}
	newPortal:=registry.Binding("after")
	currentAnalysis:=newPortal.Entry.Description.Analysis
	if currentAnalysis==nil || currentAnalysis==analysis || owner.Analysis!=currentAnalysis || !currentAnalysis.Valid(current,currentEnv) {t.Fatal("accepted portal did not refresh actual defining authority")}
	if newPortal==oldPortal || newPortal.Resource.Root==graph {t.Fatal("new admission reused the old executable graph")}
	method,_:=current.LookupAccessMethod("method_owned_heap")
	found:=false
	for _,dep:=range currentAnalysis.Dependencies {if dep.Ref.Address==method.Address() {found=dep.Revision==method.Revision()}}
	if !found {t.Fatal("new analysis failed to adopt represented method")}
	if oldPortal.Resource.Root!=graph || oldPortal.Folds.Generation!=portalGroup.Generation || !reflect.DeepEqual(oldPortal.Folds.Entries,portalGroup.Entries) || !reflect.DeepEqual(analysis.Dependencies,savedDependencies) || !reflect.DeepEqual(accepted.Entries,savedEntries) || &accepted.Entries[0]!=&portalGroup.Entries[0] {t.Fatal("new binding mutated old accepted artifacts")}
	// No requirement for gratuitous constant-fold churn: old accepted entries
	// stay immutable whether the real executable dependency projection reuses
	// or replaces its current generation.
	nextAnalysis:=currentAnalysis
	nextAccepted:=owner.Current()
	enginetest.SessionSQL(t,s,"ALTER TABLE method_owned SET ACCESS METHOD method_owned_heap")
	bind("noop")
	noopPortal:=registry.Binding("noop")
	if definition.Description.Analysis!=analysis || noopPortal.Entry.Description.Analysis==nil || !reflect.DeepEqual(noopPortal.Entry.Description.Analysis.Dependencies,nextAnalysis.Dependencies) || owner.Analysis!=noopPortal.Entry.Description.Analysis || owner.Current().Generation!=nextAccepted.Generation || &owner.Current().Entries[0]!=&nextAccepted.Entries[0] {t.Fatal("same-method no-op changed accepted stamps/backing or registered ownership")}
	command(func(scope *core.StatementScope) error {return scope.CloseProtocolStatement("method_select")})
	if owner.Live() || oldPortal.Resource.Root!=graph || oldPortal.Retired {t.Fatal("definition closure revoked accepted portal backing")}
}
