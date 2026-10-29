package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func autoincUnitFixture(t *testing.T,args string) (*core.Engine,*session.Operation,*core.StatementScope,catalog.Read,*catalog.Relation) {
	t.Helper()
	e:=engine.New()
	op,done:=enginetest.RoleOperation(t,e,"postgres");t.Cleanup(done)
	for _,q:=range []string{"CREATE EXTENSION autoinc","CREATE SEQUENCE s START 10","CREATE SEQUENCE s2 START 100","CREATE TABLE t(id integer,k integer)","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc("+args+")"} { enginetest.RoleExecute(t,e,op,q) }
	s,err:=e.StartStatement(op);if err!=nil { t.Fatal(err) };t.Cleanup(func(){s.Finish(nil)})
	read:=e.CatalogRead()
	relation:=read.MustRelation(enginetest.RoleTableRef(t,e,"t").Address)
	return e,op,s,read,relation
}

func autoincUnitUnwound(t *testing.T,s *core.StatementScope,integer *sqltype.Descriptor) {
	t.Helper()
	v,err:=s.WriteHost().TriggerDepthValue(integer);n,numberErr:=v.Integer()
	if err!=nil || numberErr!=nil || n!=0 || core.ScopeActiveSQLRoutineDepth(s)!=0 { t.Fatal("native trigger frame leaked",n,err,numberErr) }
}

func TestAutoincTriggerFrames(t *testing.T) {
	for _,tc:=range []struct{name,args string;input *int64;want int64;code string}{
		{"nonzero","'id','s'",new(int64(7)),7,""},
		{"duplicate-null","'id','s','id','s2'",nil,100,""},
		{"duplicate-zero","'id','s','id','s2'",new(int64(0)),100,""},
		{"arguments","",nil,0,"XX000"},
		{"later-attribute","'id','s','missing','s2'",nil,0,"09000"},
		{"later-sequence","'id','s','k','missing'",nil,0,"42P01"},
	} {
		t.Run(tc.name,func(t *testing.T) {
			e,op,s,read,relation:=autoincUnitFixture(t,tc.args)
			triggerUnitAdmit(t,s,relation)
			integer,_:=read.LookupType(sqltype.Int4)
			row:=[]sqltype.Value{sqltype.Null(integer),sqltype.Null(integer)}
			if tc.input!=nil { row[0]=triggerUnitInt(t,integer,*tc.input) }
			original:=row[0]
			result,err:=s.WriteHost().BeforeRow(dml.BeforeRow{Relation:read.Owner().Domain.Ref(read.Epoch(),relation.Address()),Event:dml.BeforeInsert,New:row})
			if tc.code!="" {
				var diagnostic *sqlstate.Error
				if !errors.As(err,&diagnostic) || diagnostic.Code!=tc.code { t.Fatal(err) }
			} else {
				if err!=nil || !result.Proceed { t.Fatal(result,err) }
				n,err:=result.Row[0].Integer();if err!=nil || n!=tc.want { t.Fatal(n,err) }
				if tc.name=="nonzero" && &result.Row[0]!=&row[0] { t.Fatal("nonzero path copied tuple") }
				if tc.name!="nonzero" && &result.Row[0]==&row[0] { t.Fatal("replacement borrowed producer tuple") }
			}
			if row[0]!=original { t.Fatal("input tuple mutated") }
			for i,name:=range []string{"s","s2"} {
				address:=read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:relation.Namespace(),Name:name})
				e.Mu.Lock()
				memory:=core.PayloadOf(op.Session).Sequences.Memory(address)
				var value int64;var valid bool
				if memory!=nil { value,valid=memory.Current() }
				e.Mu.Unlock()
				demanded:=tc.name!="nonzero" && tc.name!="arguments" && (i==0 || tc.name=="duplicate-null" || tc.name=="duplicate-zero")
				want:=int64(10);if i==1 { want=100 }
				if valid!=demanded || valid && value!=want { t.Fatal("allocation/session effect",tc.name,name,value,valid) }
			}
			autoincUnitUnwound(t,s,integer)
		})
	}
	t.Run("after-and-statement",func(t *testing.T) {
		for _,level:=range []stored.TriggerLevel{stored.TriggerRow,stored.TriggerStatement} {
			t.Run(string(rune('0'+level)),func(t *testing.T) {
				e,_,s,read,relation:=autoincUnitFixture(t,"'id','s'")
				p:=stored.TriggersOf(relation)[0].Parts();p.Timing=stored.TriggerAfter;p.Level=level
				definition,err:=stored.NewTrigger(p);if err!=nil { t.Fatal(err) }
				relation=triggerUnitInstall(t,e,relation,definition)
				triggerUnitAdmit(t,s,relation)
				ref:=read.Owner().Domain.Ref(read.Epoch(),relation.Address())
				integer,_:=read.LookupType(sqltype.Int4)
				if level==stored.TriggerStatement {
					err=s.WriteHost().AfterStatement(ref,stored.TriggerInsert,nil)
					if err==nil { err=s.WriteHost().CompleteAfterStatement(ref,stored.TriggerInsert) }
				} else { err=s.WriteHost().AfterRow(dml.BeforeRow{Relation:ref,Event:dml.BeforeInsert,New:[]sqltype.Value{sqltype.Null(integer),sqltype.Null(integer)}}) }
				if err==nil { err=core.ScopeDrainAfterTest(s) }
				var diagnostic *sqlstate.Error
				want:="must be fired before event";if level==stored.TriggerStatement { want="must be fired for row" }
				if !errors.As(err,&diagnostic) || diagnostic.Code!="XX000" || diagnostic.Message!=want { t.Fatal(err) }
				autoincUnitUnwound(t,s,integer)
			})
		}
	})
	t.Run("interrupted-demand",autoincUnitInterruptedDemand)
}

func autoincUnitInterruptedDemand(t *testing.T) {
	e,_,s,read,relation:=autoincUnitFixture(t,"'id','s'")
	triggerUnitAdmit(t,s,relation)
	integer,_:=read.LookupType(sqltype.Int4)
	sequence:=read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:relation.Namespace(),Name:"s"})
	holderOp,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
	holder,err:=e.StartStatement(holderOp);if err!=nil { t.Fatal(err) };defer holder.Finish(nil)
	if err:=holder.Gate(enginetest.Statement(t,"ALTER SEQUENCE s RESTART WITH 90"));err!=nil { t.Fatal(err) }
	if err:=holder.AcquireSequence(read.Owner().Domain.Ref(read.Epoch(),sequence),lock.AccessExclusive,true);err!=nil { t.Fatal(err) }
	ctx,cancel:=context.WithCancel(context.Background());defer cancel()
	// A separate real operation owns cancellation; retain the already-admitted
	// statement only for its independent nonzero tuple/lock-free witness.
	result,err:=s.WriteHost().BeforeRow(dml.BeforeRow{Relation:read.Owner().Domain.Ref(read.Epoch(),relation.Address()),Event:dml.BeforeInsert,New:[]sqltype.Value{triggerUnitInt(t,integer,7),sqltype.Null(integer)}})
	if err!=nil || !result.Proceed { t.Fatal(err) }
	s.Finish(nil)
	waitOp,waitDone,err:=enginetest.Operation(e,ctx);if err!=nil { t.Fatal(err) };defer waitDone()
	waitScope,err:=e.StartStatement(waitOp);if err!=nil { t.Fatal(err) };defer waitScope.Finish(nil)
	triggerUnitAdmit(t,waitScope,relation)
	completed:=make(chan error,1)
	go func(){ _,err:=waitScope.WriteHost().BeforeRow(dml.BeforeRow{Relation:read.Owner().Domain.Ref(read.Epoch(),relation.Address()),Event:dml.BeforeInsert,New:[]sqltype.Value{sqltype.Null(integer),sqltype.Null(integer)}});completed<-err }()
	joined:=false
	defer func(){
		cancel()
		if !joined {
			holder.Finish(nil)
			select { case <-completed: case <-time.After(5*time.Second): t.Error("allocation worker did not exit after cancellation and lock release") }
		}
	}()
	enginetest.LockPending(t,e,1)
	cancel()
	select {
	case err:=<-completed:
		joined=true
		if !errors.Is(err,context.Canceled) { t.Fatal(err) }
	case <-time.After(5*time.Second):t.Fatal("canceled allocation did not finish")
	}
	autoincUnitUnwound(t,waitScope,integer)
}
