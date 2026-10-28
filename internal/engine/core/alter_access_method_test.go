package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
)

func TestAlterAccessMethodProvider(t *testing.T) {
	e:=engine.New();s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE TABLE method_provider(id int)")
	original:=e.CatalogRead()
	relation:=primaryKeyUnitRelation(t,e,"method_provider")
	method:=accessMethodUnit(t,e,"method_provider_heap",catalog.HeapAccessMethod,3)
	read:=accessMethodPublish(t,e,method)
	alias:=*relation;alias.TableMethod=method.Address().ObjectID
	if err:=catalog.ValidateRelationTableMethod(read,&alias);err!=nil {t.Fatal(err)}
	builtin,err:=visibility.ResolveHeapAccess(read,2);if err!=nil {t.Fatal(err)}
	actual,err:=visibility.ResolveHeapAccess(read,alias.TableMethod);if err!=nil || actual!=builtin {t.Fatal("alias did not bind genuine heap",err)}
	if original.Get(method.Address())!=nil || relation.TableMethod!=2 {t.Fatal("old Read/descriptor mutated")}
	for _,id:=range []ids.OID{0,403,405,999999} {
		bad:=alias;bad.TableMethod=id
		if err:=catalog.ValidateRelationTableMethod(read,&bad);err==nil {t.Fatal("invalid physical method admitted",id)}
	}
	for _,kind:=range []catalog.RelationKind{catalog.IndexRelation,catalog.ViewRelation,catalog.SequenceRelation,catalog.ForeignRelation,catalog.CompositeRelation,catalog.CatalogRelation} {
		bad:=alias;bad.RelationKind=kind
		if err:=catalog.ValidateRelationTableMethod(read,&bad);err==nil {t.Fatal("ineligible method admitted",kind)}
	}
	root:=alias;root.RelationKind=catalog.PartitionedRelation;root.TableMethod=0
	if err:=catalog.ValidateRelationTableMethod(read,&root);err!=nil {t.Fatal(err)}
	for _,missing:=range []bool{false,true} {
		state:=catalog.CloneState(read.State())
		handler,_:=read.Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:3})
		if missing {state.RemoveDescriptor(handler.Address())} else {forged:=*handler;forged.AccessMethod=catalog.NoAccessMethod;state.SetDescriptor(forged.Address(),&forged)}
		if err:=catalog.ValidateRelationTableMethod(read.WithState(state),&alias);err==nil {t.Fatal("missing/forged handler granted authority")}
	}
	// A new consumer cannot adopt a different provider generation after capture.
	stage:=e.BeginCatalogStage(read);defer e.DiscardCatalogStage(stage)
	alias.ObjectRevision++
	if err:=stage.Replace(&alias);err!=nil {t.Fatal(err)}
	changedMethod:=*method;changedMethod.ObjectRevision++
	state:=catalog.CloneState(read.State());state.SetDescriptor(method.Address(),&changedMethod)
	if stage.AccessMethodsCurrent(read.WithState(state)) {t.Fatal("changed method predecessor admitted")}
	retirement:=e.BeginCatalogStage(read);defer e.DiscardCatalogStage(retirement)
	if err:=retirement.Remove(e.Catalog.Domain.Ref(read.Epoch(),method.Address()));err!=nil {t.Fatal(err)}
	state=catalog.CloneState(read.State());state.SetDescriptor(alias.Address(),&alias)
	if retirement.AccessMethodsCurrent(read.WithState(state)) {t.Fatal("retirement missed incoming table consumer")}
}

func TestAlterAccessMethodPublication(t *testing.T) {
	t.Run("metadata-and-noop",func(t *testing.T) {
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		for _,q:=range []string{"CREATE TABLE method_publish(id int PRIMARY KEY,note text)","CREATE ACCESS METHOD method_publish_heap TYPE TABLE HANDLER heap_tableam_handler","INSERT INTO method_publish VALUES(7,'keep')"} {enginetest.SessionSQL(t,s,q)}
		read:=e.CatalogRead();old:=primaryKeyUnitRelation(t,e,"method_publish");saved:=*old
		store:=e.Storage.Relations[old.Address()];versions,ends:=store.Versions,store.Ends
		idx:=primaryKeyUnitRelation(t,e,"method_publish_pkey");root:=e.Storage.IndexRoots[idx.Address()]
		enginetest.SessionSQL(t,s,"ALTER TABLE method_publish SET ACCESS METHOD method_publish_heap")
		next:=primaryKeyUnitRelation(t,e,"method_publish")
		if next==old || next.Revision()<=old.Revision() || next.TableMethod==old.TableMethod || !reflect.DeepEqual(*old,saved) || read.Get(old.Address())!=old {t.Fatal("immutable method revision transition")}
		if next.RowType!=old.RowType || next.Owner!=old.Owner || &next.Columns[0]!=&old.Columns[0] || &next.Attributes[0]!=&old.Attributes[0] || !reflect.DeepEqual(next.ColumnMeta,old.ColumnMeta) || !reflect.DeepEqual(next.Security,old.Security) || !reflect.DeepEqual(next.Constraints,old.Constraints) {t.Fatal("unrelated definition changed")}
		if e.Storage.Relations[old.Address()]!=store || &store.Versions[0]!=&versions[0] || store.Ends!=ends || e.Storage.IndexRoots[idx.Address()]!=root || e.CatalogRead().Get(idx.Address())!=idx {t.Fatal("logical method transition rewrote physical backing")}
		before:=e.Catalog.State
		enginetest.SessionSQL(t,s,"ALTER TABLE method_publish SET ACCESS METHOD method_publish_heap")
		if e.CatalogRead().Get(old.Address())!=next || e.Catalog.State!=before || e.Storage.Relations[old.Address()]!=store {t.Fatal("no-op published a new definition")}
		mvOld:=*next;mvOld.RelationKind=catalog.MaterializedViewRelation
		mvCurrent:=mvOld;mvCurrent.TableMethod=2
		if catalog.RelationDefiningAuthorityCompatible(&mvOld,&mvCurrent) {t.Fatal("refresh waived method authority")}
		mvCurrent=mvOld;mvCurrent.Populated=!mvOld.Populated
		if !catalog.RelationDefiningAuthorityCompatible(&mvOld,&mvCurrent) {t.Fatal("population-only refresh was made incompatible")}
	})
	t.Run("cancel-before-finish",func(t *testing.T) {
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		for _,q:=range []string{"CREATE TABLE method_cancel(id int)","CREATE ACCESS METHOD method_cancel_heap TYPE TABLE HANDLER heap_tableam_handler"} {enginetest.SessionSQL(t,s,q)}
		before:=e.CatalogRead();old:=primaryKeyUnitRelation(t,e,"method_cancel");store:=e.Storage.Relations[old.Address()]
		ctx,cancel:=context.WithCancel(t.Context());defer cancel()
		op,err:=s.Acquire(ctx);if err!=nil {t.Fatal(err)};defer op.Done()
		scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)}
		_,err=scope.Execute(enginetest.Statement(t,"ALTER TABLE method_cancel SET ACCESS METHOD method_cancel_heap"),0,nil)
		if err!=nil {scope.Finish(err);t.Fatal(err)}
		journal:=scope.PendingCatalog()
		if journal==nil || journal.Descriptors[old.Address()]==nil || journal.Descriptors[old.Address()].(*catalog.Relation).TableMethod==2 {scope.Finish(nil);t.Fatal("missing complete private method candidate")}
		for _,a:=range []ids.ObjectAddress{{ClassID:ids.ProcedureClassID,ObjectID:3},{ClassID:ids.AccessMethodClassID,ObjectID:journal.Descriptors[old.Address()].(*catalog.Relation).TableMethod}} {
			if journal.Originals[a]!=before.Get(a) {scope.Finish(nil);t.Fatal("missing exact method/handler predecessor",a)}
		}
		cancel()
		if err=scope.Finish(nil);!errors.Is(err,context.Canceled) {t.Fatal(err)}
		if e.Catalog.State!=before.State() || e.CatalogRead().Get(old.Address())!=old || e.Storage.Relations[old.Address()]!=store {t.Fatal("canceled validated stage installed")}
	})
	t.Run("outgoing-provider-fence",func(t *testing.T) {
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		for _,q:=range []string{"CREATE TABLE method_fence(id int)","CREATE ACCESS METHOD method_fence_heap TYPE TABLE HANDLER heap_tableam_handler"} {enginetest.SessionSQL(t,s,q)}
		base:=e.CatalogRead();tx:=enginetest.TransactionBegin(t,s);defer enginetest.TransactionEnd(t,s,tx,false)
		enginetest.SessionSQL(t,s,"ALTER TABLE method_fence SET ACCESS METHOD method_fence_heap")
		original,_:=base.Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:3})
		changed:=*original;changed.ObjectRevision++
		stage:=e.BeginCatalogStage(base);defer e.DiscardCatalogStage(stage)
		if err:=stage.Replace(&changed);err!=nil {t.Fatal(err)}
		prepared,err:=e.ValidateCatalogStage(t.Context(),stage);if err!=nil {t.Fatal(err)}
		if _,err=enginetest.PublishCatalog(e,prepared,core.PublicationToken{Epoch:base.Epoch()});err!=nil {t.Fatal(err)}
		e.Mu.Lock();err=e.CatalogPreflightLocked(s.Transactions.Catalog);e.Mu.Unlock()
		var conflict catalog.Conflict
		if !errors.As(err,&conflict) {t.Fatal("stale installed handler admitted",err)}
	})
	t.Run("exact-original-fence",func(t *testing.T) {
		for _,sameIdentity:=range []bool{false,true} {
			e:=engine.New();s:=enginetest.StartSession(t,e,nil)
			enginetest.SessionSQL(t,s,"CREATE TABLE method_original(id int)")
			original:=primaryKeyUnitRelation(t,e,"method_original")
			method:=accessMethodUnit(t,e,"method_original_heap",catalog.HeapAccessMethod,3)
			read:=accessMethodPublish(t,e,method)
			stage:=e.BeginCatalogStage(read)
			next:=*original;next.TableMethod=method.Address().ObjectID;next.ObjectRevision++
			if err:=stage.Replace(&next);err!=nil {t.Fatal(err)}
			prepared,err:=e.ValidateCatalogStage(t.Context(),stage);if err!=nil {t.Fatal(err)}
			if sameIdentity {enginetest.SessionSQL(t,s,"COMMENT ON TABLE method_original IS 'peer'")} else {enginetest.SessionSQL(t,s,"CREATE TABLE method_unrelated(id int)")}
			_,err=enginetest.PublishCatalog(e,prepared,core.PublicationToken{Epoch:read.Epoch()})
			if sameIdentity {
				if err==nil || e.CatalogRead().MustRelation(original.Address()).TableMethod!=2 || e.CatalogRead().MustRelation(original.Address()).Comment!="peer" {t.Fatal("stale exact original overwrote peer",err)}
			} else if err!=nil || e.CatalogRead().MustRelation(original.Address()).TableMethod!=method.Address().ObjectID {t.Fatal("unrelated publication spuriously conflicted",err)}
			if original.TableMethod!=2 || original.Comment!="" {t.Fatal("predecessor mutated")}
			e.DiscardCatalogStage(stage)
		}
	})
}


func TestAlterAccessMethodAdmissionJournal(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE ACCESS METHOD admission_heap TYPE TABLE HANDLER heap_tableam_handler",
		"CREATE TABLE method_admission(id int)",
		"ALTER TABLE method_admission SET ACCESS METHOD admission_heap",
	} { enginetest.SessionSQL(t, s, q) }
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer scope.Finish(nil)
	if err := scope.Gate(enginetest.Statement(t, "INSERT INTO method_admission VALUES(1)")); err != nil { t.Fatal(err) }
	before := scope.PendingCatalog()
	relation := primaryKeyUnitRelation(t, e, "method_admission")
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), relation.Address())
	got, read, err := scope.WriteHost().UniqueParent(ref)
	if err != nil || got != relation || scope.PendingCatalog() != before || scope.Catalog != nil {
		t.Fatal("read admission manufactured private catalog work", err)
	}
	if _, err := visibility.ResolveRelationHeapAccess(read, ref); err != nil {
		t.Fatal("captured physical method did not resolve", err)
	}
}
