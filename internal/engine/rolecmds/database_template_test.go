package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
)

func templateUnitSource(t *testing.T) (*core.Engine,*core.Engine) {
	t.Helper();e,seed:=engine.New(),engine.New()
	source:=databaseCreateUnitInstall(t,e,seed,"unit_template")
	op,done:=enginetest.RoleOperation(t,source,"postgres")
	for _,stmt:=range []parser.Statement{primaryKeyUnitCreate("t","template_pk"),primaryKeyUnitInsert(1),&parser.CreateSequence{Name:primaryKeyUnitName("template_seq"),Persistence:"logged",Options:[]parser.SequenceOption{{Name:"start",Value:41}}}} {
		if _,err:=source.Execute(op,stmt,0,nil);err!=nil {done();t.Fatal(err)}
	}
	done();return e,source
}

func TestDatabaseTemplateImmutableSource(t *testing.T) {
	e,source:=templateUnitSource(t)
	relation:=primaryKeyUnitRelation(t,source,"t");address:=relation.Address();index:=relation.Constraints.Unique[0].Index
	seq:=primaryKeyUnitRelation(t,source,"template_seq").Address()
	heap:=source.Storage.Relations[address];idx:=source.Storage.IndexRoots[index];generator:=source.Storage.Sequences[seq]
	rows:=append(heap.Versions[:0:0],heap.Versions...)
	entries:=append(idx.Entries[:0:0],idx.Entries...)
	// A private transaction's row must not enter committed storage capture.
	s:=enginetest.StartSession(t,source,nil);tx:=enginetest.TransactionBegin(t,s)
	op:=enginetest.TransactionOperation(t,s)
	if _,err:=source.Execute(op,primaryKeyUnitInsert(2),0,nil);err!=nil {op.Done();t.Fatal(err)};op.Done()
	root:=source.Catalog.State
	target:=databaseCreateUnitTarget(t,e,source,"immutable_clone")
	if source.Catalog.State!=root||source.Storage.Relations[address]!=heap||source.Storage.IndexRoots[index]!=idx||source.Storage.Sequences[seq]!=generator||!reflect.DeepEqual(heap.Versions,rows)||!reflect.DeepEqual(idx.Entries,entries) {t.Fatal("private remint changed source roots/backing")}
	if target.Storage.Relations[address]==heap||target.Storage.IndexRoots[index]==idx||target.Storage.Sequences[seq]==generator {t.Fatal("mutable storage backing shared")}
	if len(target.Storage.Relations[address].Versions)!=1 {t.Fatal("private row entered committed copy")}
	if &target.Storage.Relations[address].Versions[0]==&heap.Versions[0] {t.Fatal("row vector shared")}
	if !target.Storage.Relations[address].Versions[0].Frozen||!target.Storage.Relations[address].Versions[0].ImportFrozen {t.Fatal("destination row retained source transaction provenance")}
	enginetest.TransactionEnd(t,s,tx,false)
}

func TestDatabaseTemplateProvenance(t *testing.T) {
	e,source:=templateUnitSource(t);target:=databaseCreateUnitTarget(t,e,source,"provenance_clone")
	if target.Catalog.Domain==source.Catalog.Domain||target.Catalog.Current().Owner()==source.Catalog.Current().Owner()||target.Catalog.Principals!=source.Catalog.Principals {t.Fatal("database-local identity or shared principal identity lost")}
	a:=primaryKeyUnitRelation(t,source,"t").Address()
	if target.CatalogRead().Get(a)==nil {t.Fatal("local SQL OID not retained")}
	for _,pair:=range [][2]*core.Engine{{source,target},{target,source}} {
		for _,address:=range []ids.ObjectAddress{a,{ClassID:ids.RelationClassID,ObjectID:1259}} {
			v:=pair[0].CatalogRead();ref:=pair[0].Catalog.Domain.Ref(v.Epoch(),address)
			if err:=enginetest.ValidateObjectRef(pair[1],pair[1].CatalogRead(),ref);err==nil {t.Fatal("foreign domain reference accepted")}
		}
	}
}

type templateValidationProvider struct {catalog.Provider; hook func() error; once sync.Once; failure error}
func (p *templateValidationProvider) Validate(v catalog.Read,d catalog.Descriptor) error {
	if d.Name()=="t" {p.once.Do(func(){p.failure=p.hook()});if p.failure!=nil{return p.failure}}
	return p.Provider.Validate(v,d)
}

func TestDatabaseTemplatePublication(t *testing.T) {
	for _,mode:=range []string{"validation_failure","canceled_preparation","source_authority_changed"} {
		t.Run(mode,func(t *testing.T){
			e,source:=templateUnitSource(t)
			unchanged:=databaseCreateUnitUnchanged(t,e)
			ctx,cancel:=context.WithCancel(t.Context());defer cancel()
			sentinel:=errors.New("template validation failure")
			original:=e.Catalog.Providers[ids.RelationClassID]
			provider:=&templateValidationProvider{Provider:original}
			provider.hook=func()error {
				switch mode {
				case "validation_failure":return sentinel
				case "canceled_preparation":cancel()
				case "source_authority_changed":
					e.Mu.Lock();next:=catalog.CloneState(source.Catalog.State);d:=*next.MustDatabase(source.Catalog.DatabaseAddress());d.ObjectRevision++;next.SetDescriptor(d.Address(),&d);roots:=source.Catalog.ProjectSharedLocked(next,e.Cluster.Catalogs);source.Catalog.PublishLocked(next);roots.InstallLocked();e.Mu.Unlock()
				}
				return nil
			}
			e.Catalog.Providers[ids.RelationClassID]=provider
			op,done,err:=enginetest.Operation(e,ctx)
			if err!=nil {t.Fatal(err)}
			_,err=e.Execute(op,&parser.CreateDatabaseStmt{Name:"unit_failed_clone",Options:[]parser.DatabaseOption{{Name:"template",Value:source.DatabaseName}}},0,nil)
			done();e.Catalog.Providers[ids.RelationClassID]=original
			if err==nil {t.Fatal("failed private preparation published")}
			if mode=="validation_failure"&&!errors.Is(err,sentinel){t.Fatal(err)}
			if mode=="canceled_preparation"&&!errors.Is(err,context.Canceled){t.Fatal(err)}
			if mode!="source_authority_changed" {unchanged()} else if e.Cluster.Targets["unit_failed_clone"]!=nil {t.Fatal("stale source authority published")}
			if !source.ReplacementMu.TryLock(){t.Fatal("failed preparation leaked replacement read protection")};source.ReplacementMu.Unlock()
			a,err:=e.AdmitDatabase(t.Context(),e.Roles().LookupRef("postgres"),core.DatabaseSelector{Name:source.DatabaseName,Explicit:true});if err!=nil {t.Fatal("source admission leaked",err)};a.Release()
			op,done,err=enginetest.Operation(e,t.Context());if err!=nil {t.Fatal(err)};defer done()
			if _,err:=e.Execute(op,&parser.CreateDatabaseStmt{Name:"unit_failed_clone",Options:[]parser.DatabaseOption{{Name:"template",Value:source.DatabaseName}}},0,nil);err!=nil {t.Fatal("same-name retry",err)}
		})
	}
	t.Run("final_cancel",func(t *testing.T){
		e,source:=templateUnitSource(t);target:=databaseCreateUnitTarget(t,e,source,"unit_final_cancel")
		unchanged:=databaseCreateUnitUnchanged(t,e)
		source.ReplacementMu.RLock();e.Mu.Lock();lease,err:=source.AcquireDatabaseTemplateLocked(nil);e.Mu.Unlock();if err!=nil {t.Fatal(err)}
		base,cancel:=context.WithCancel(t.Context());defer cancel();ctx:=&databaseCreateCancelContext{Context:base,cancel:cancel}
		_,err=databaseCreateUnitPublish(e,ctx,"unit_final_cancel",target)
		lease.Release();source.ReplacementMu.RUnlock()
		if !errors.Is(err,context.Canceled)||!ctx.admitted {t.Fatal(err)};unchanged()
		if _,err:=e.SelectDatabase(t.Context(),"unit_final_cancel");err==nil {t.Fatal("canceled target selectable")}
		a,err:=e.AdmitDatabase(t.Context(),e.Roles().LookupRef("postgres"),core.DatabaseSelector{Name:source.DatabaseName,Explicit:true});if err!=nil {t.Fatal(err)};a.Release()
		if _,err:=databaseCreateUnitPublish(e,t.Context(),"unit_final_cancel",target);err!=nil {t.Fatal("same-name final-publication recovery",err)}
	})
}

func TestDatabaseAuthorityTemplatePolicy(t *testing.T) {
	e:=engine.New();old:=e.CatalogRead().MustDatabase(e.Catalog.DatabaseAddress());changed:=*old;changed.IsTemplate=!old.IsTemplate
	if catalog.DatabaseAuthorityCompatible(old,&changed){t.Fatal("template policy omitted from authority")}
	changed=*old;changed.FrozenXID++
	if !catalog.DatabaseAuthorityCompatible(old,&changed){t.Fatal("maintenance horizon changed authority")}
	options:=ddl.CreateDatabaseOptions{IsTemplate:true,AllowConnections:false,ConnectionLimit:2}
	private:=rolecmds.NewDatabaseRuntime(e,e,"template_policy",e.Roles().LookupRef("postgres"),options)
	policy:=private.CatalogRead().MustDatabase(private.Catalog.DatabaseAddress())
	if !policy.IsTemplate||policy.AllowConnections||policy.ConnectionLimit!=2 {t.Fatal("private template policy lost")}
}

func TestDatabaseTemplateDefaultSource(t *testing.T) {
	e,source:=templateUnitSource(t)
	template:=databaseCreateUnitInstall(t,e,source,"template1")
	op,done:=enginetest.RoleOperation(t,template,"postgres")
	if _,err:=template.Execute(op,primaryKeyUnitInsert(2),0,nil);err!=nil {done();t.Fatal(err)}
	done()
	op,done=enginetest.RoleOperation(t,e,"postgres");defer done()
	address:=primaryKeyUnitRelation(t,source,"t").Address()
	for _,stmt:=range []*parser.CreateDatabaseStmt{
		{Name:"default_template_clone"},
		{Name:"explicit_template_clone",Options:[]parser.DatabaseOption{{Name:"template",Value:"template1"}}},
	} {
		if _,err:=e.Execute(op,stmt,0,nil);err!=nil {t.Fatal(err)}
		target:=e.Cluster.Targets[stmt.Name]
		if target==nil||len(target.Storage.Relations[address].Versions)!=2 {t.Fatal("source ignored registered template1")}
	}
}
