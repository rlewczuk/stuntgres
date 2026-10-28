package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestAlterPersistencePostLockFence(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		for _,descriptorChange:=range []bool{false,true} {
			name:=map[bool]string{false:"direct",true:"prepared"}[prepared]+"/"+map[bool]string{false:"latest-generator",true:"descriptor-conflict"}[descriptorChange]
			t.Run(name,func(t *testing.T) {
				e:=engine.New()
				holder,worker:=enginetest.StartSession(t,e,nil),enginetest.StartSession(t,e,nil)
				enginetest.SessionSQL(t,holder,"CREATE TABLE t(id int)")
				cache:="1";if descriptorChange {cache="5"}
				enginetest.SessionSQL(t,holder,"CREATE SEQUENCE owned CACHE "+cache+" OWNED BY t.id")
				sequenceScalar(t,worker,"SELECT nextval('owned')",1)
				_,description:=sequencePreparedDescription(t,e,worker,"SELECT nextval('owned')")
				cell:=description.Sequences
				constant:=&cell.Constants[0]
				accepted:=*constant
				statement:=enginetest.Statement(t,"ALTER TABLE t SET UNLOGGED")
				var expected *core.Description
				if prepared {
					captured,preparedDescription:=sequencePreparedDescription(t,e,worker,"ALTER TABLE t SET UNLOGGED")
					statement,expected=captured,&preparedDescription
				}
				sequenceRef:=roleTableRef(t,e,"owned")
				tableRef:=roleTableRef(t,e,"t")
				tableOriginal:=e.Catalog.State.Get(tableRef.Address)
				sequenceOriginal:=e.Catalog.State.Get(sequenceRef.Address)
				heap:=e.Storage.Relations[tableRef.Address]
				generator:=e.Storage.Sequences[sequenceRef.Address]
				memory:=core.PayloadOf(worker).Sequences.Memory(sequenceRef.Address)
				memoryBefore:=*memory
				tx:=enginetest.TransactionBegin(t,holder)
				if descriptorChange {enginetest.SessionSQL(t,holder,"ALTER SEQUENCE owned CACHE 2")} else {sequenceScalar(t,holder,"SELECT nextval('owned')",2)}
				ctx,cancel:=context.WithCancel(context.Background())
				// Execute the retained description through the incumbent prepared
				// statement-scope helper; LockAsync's prepare flag only Describes.
				operation:=ownedLockAsync(e,worker,ctx,statement,expected)
				finished:=make(chan struct{})
				observed:=make(chan error,1)
				var failure error
				go func(){
					failure=<-operation
					observed<-failure
					close(finished)
				}()
				ended:=false
				t.Cleanup(func(){
					cancel()
					if !ended {enginetest.TransactionEnd(t,holder,tx,false)}
					// The observer may consume its completion error on failure.
					// This independent close still joins the actual worker once.
					select {case <-finished:case <-time.After(5*time.Second):t.Error("persistence worker did not join")}
				})
				ownedLockQueued(t,e,sequenceRef,lock.AccessExclusive,observed)
				// This demand occurs only after the table worker has captured the
				// original descriptor and actually queued for the owned AX grant.
				if !descriptorChange {sequenceScalar(t,holder,"SELECT nextval('owned')",3)}
				generatorBefore:=*generator
				var winner catalog.Descriptor
				winnerGenerator:=generator
				if descriptorChange {
					winner=holder.Transactions.Catalog.Descriptors[sequenceRef.Address]
					winnerGenerator=holder.Transactions.Catalog.Generators[sequenceRef.Address]
					if winner==nil || winnerGenerator==nil {t.Fatal("missing holder's private winner")}
				}
				enginetest.TransactionEnd(t,holder,tx,true)
				ended=true
				select {case <-finished:case <-time.After(5*time.Second):t.Fatal("persistence worker did not finish")}
				if descriptorChange {
					var conflict catalog.Conflict
					if !errors.As(failure,&conflict) {t.Fatal("missing ordinary post-grant conflict",failure)}
					if e.Catalog.State.Get(tableRef.Address)!=tableOriginal || e.Catalog.State.Get(sequenceRef.Address)!=winner || e.Storage.Relations[tableRef.Address]!=heap || e.Storage.Sequences[sequenceRef.Address]!=winnerGenerator || winnerGenerator.Last!=5 || !winnerGenerator.Called || *generator!=generatorBefore || *memory!=memoryBefore {t.Fatal("rejected stale stage changed committed winner/table/storage/cache")}
				} else {
					if failure!=nil {t.Fatal(failure)}
					replacement:=e.Storage.Sequences[sequenceRef.Address]
					next:=e.Catalog.State.MustRelation(sequenceRef.Address)
					if replacement==generator || replacement.Last!=3 || !replacement.Called || replacement.Generation==generatorBefore.Generation || *generator!=generatorBefore || next.SequencePlanStamp==sequenceOriginal.(*catalog.Relation).SequencePlanStamp || next.Sequence.Persistence!="unlogged" || *memory!=memoryBefore || e.Storage.Relations[tableRef.Address]!=heap {t.Fatal("post-grant latest generator capture/lazy adoption/heap identity")}
				}
				if description.Sequences!=cell || &cell.Constants[0]!=constant || !reflect.DeepEqual(*constant,accepted) {t.Fatal("table worker published into retained accepted sequence authority")}
				if !descriptorChange {
					sequenceScalar(t,worker,"SELECT nextval('owned')",4)
					if memory.Generation()!=e.Storage.Sequences[sequenceRef.Address].Generation {t.Fatal("sequence demand failed lazy generation adoption")}
					if *generator!=generatorBefore {t.Fatal("replacement demand mutated the retired generator")}
				}
				enginetest.LockEmpty(t,e)
			})
		}
	}
}
