package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// Exercise real statement admission, not a fixture-injected Runtime.Auth.
func TestPGTriggerCaptureRuntime(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	for _,populated:=range []bool{false,true} {
		if populated {
			for _,q:=range []string{"CREATE TABLE runtime_trigger_owner(a integer)","CREATE FUNCTION runtime_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$","CREATE TRIGGER actual BEFORE UPDATE ON runtime_trigger_owner FOR EACH ROW WHEN(OLD.a>0) EXECUTE FUNCTION runtime_trigger_fn()"} { enginetest.SessionSQL(t,s,q) }
		}
		func() {
			op:=enginetest.TransactionOperation(t,s)
			defer op.Done()
			scope,err:=e.StartStatement(op)
			if err!=nil { t.Fatal(err) }
			defer func(){ if err:=scope.Finish(nil);err!=nil { t.Error(err) } }()
			e.Mu.Lock()
			err=e.CaptureCommentSnapshotLocked(scope)
			e.Mu.Unlock()
			if err!=nil { t.Fatal(err) }
			read:=scope.Inquiry.CommentCatalog
			d:=read.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.TriggerClassID})
			ref:=read.Owner().Domain.Ref(read.Epoch(),d.Address())
			e.Mu.Lock()
			source,err:=core.EngineOpenRelationSourceLocked(e,scope,ref,d.Owner,plan.RowMark{},nil,nil)
			if err!=nil || source==nil { t.Fatal("admitted trigger capture",err) }
			defer source.Close()
			want:=0;if populated { want=1 }
			if source.Ref!=ref || source.Anonymous==nil || len(source.Anonymous)!=want { t.Fatal("owned trigger capture",source) }
			if populated {
				row:=source.Anonymous[0]
				if len(row)!=19 { t.Fatal("trigger width",len(row)) }
				name,err:=row[3].Text();if err!=nil || name!="actual" { t.Fatal("actual definition",name,err) }
				tree,err:=row[16].Tree();if err!=nil { t.Fatal(err) }
				if _,ok:=tree.(stored.TriggerCarrier);!ok { t.Fatal("unchecked runtime tree",tree) }
			}
			stale:=ref;stale.Epoch++
			e.Mu.Lock()
			rejected,err:=core.EngineOpenCatalogSourceLocked(e,scope,stale,d)
			if rejected!=nil || err==nil { t.Fatal("runtime accepted stale trigger source",rejected,err) }
		}()
	}
}
