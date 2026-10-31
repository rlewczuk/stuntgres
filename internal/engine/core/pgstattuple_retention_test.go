package core_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func heapPredecessorCount(root *storage.HeapPredecessor) int {
	n := 0
	for ; root != nil; root = root.Next { n++ }
	return n
}

func TestPgstattupleHeapRetention(t *testing.T) {
	t.Run("nested_write_owners",heapUnitNestedRetention)
	t.Run("projection_rewrite_truncate",heapUnitHeapReplacements)
	t.Run("logical_replacement",heapUnitLogicalReplacement)
	e := engine.New()
	pgstatindexUnitInstalled(t,e)
	for _,q:=range []string{"CREATE TABLE retained_heap(n integer,s text)","INSERT INTO retained_heap VALUES(1,'A')","CREATE INDEX retained_index ON retained_heap(lower(s)) INCLUDE(n) WHERE n>0"} { enginetest.Run(t,e,q) }
	base:=pgstatindexCaptureFixture(t,e,nil,"retained_index")
	original:=base.Store.Versions[0]
	horizon := sessionStart(t,e,nil)
	sessionSQL(t,horizon,"BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t,horizon,"SELECT * FROM retained_heap")
	s:=sessionStart(t,e,nil)
	for _,q:=range []string{"BEGIN","UPDATE retained_heap SET n=2,s='B'","SAVEPOINT retained_mark"} { sessionSQL(t,s,q) }
	first:=s.Transactions.Writes[base.Parent.Address()][0]
	if first.PrivateHistory==nil || first.PrivateHistory.Version.Version!=original.Version || &first.PrivateHistory.Version.Values[0]!=&original.Values[0] { t.Fatal("first actual predecessor was not retained by immutable shared payload") }
	sessionSQL(t,s,"UPDATE retained_heap SET n=3,s='C'")
	second:=s.Transactions.Writes[base.Parent.Address()][0]
	if heapPredecessorCount(second.PrivateHistory)!=2 || second.PrivateHistory.Version.Version!=first.Version || second.PrivateHistory.Next!=first.PrivateHistory { t.Fatal("successor did not link actual O(1) immutable predecessor root") }
	discarded:=second.Version
	sessionSQL(t,s,"ROLLBACK TO retained_mark")
	if got:=s.Transactions.Writes[base.Parent.Address()][0]; got.Version!=first.Version || got.PrivateHistory!=first.PrivateHistory { t.Fatal("savepoint reconstructed or stranded predecessor root") }
	sessionSQL(t,s,"UPDATE retained_heap SET n=4,s='D'")
	last:=s.Transactions.Writes[base.Parent.Address()][0]
	if last.PrivateHistory.Version.Version!=first.Version || last.PrivateHistory.Next!=first.PrivateHistory || second.PrivateHistory.Version.Version!=first.Version { t.Fatal("append after rollback mutated an old capture") }
	if err:=preparedUnitControl(t,e,s,parser.KindPrepareTransaction,"retained"); err!=nil { t.Fatal(err) }
	e.Mu.Lock()
	entry:=core.EnginePreparedRef(e).Lookup("retained")
	if entry==nil || entry.Writes[base.Parent.Address()][0].PrivateHistory!=last.PrivateHistory || s.Transactions.Writes!=nil { e.Mu.Unlock(); t.Fatal("PREPARE did not transfer the real root once") }
	e.Mu.Unlock()
	if s.TransactionState()!=session.TxIdle { t.Fatal("PREPARE left an active origin") }
	finisher:=sessionStart(t,e,nil)
	if err:=preparedUnitControl(t,e,finisher,parser.KindCommitPrepared,"retained"); err!=nil { t.Fatal(err) }
	after:=pgstatindexCaptureFixture(t,e,nil,"retained_index")
	if after.Store.Versions[0].PrivateHistory!=nil || heapPredecessorCount(after.Store.Retained)!=2 || base.Store.Retained!=nil || base.Store.Versions[0].Version!=original.Version { t.Fatal("committed heads leaked private pins or mutated old physical capture") }
	for prior:=after.Store.Retained; prior!=nil; prior=prior.Next {
		if prior.End==0 || prior.EndFullXID==0 || prior.Version.Born==0 || prior.Version.IndexAdmission!=nil || prior.Version.IndexOutputs!=nil || prior.Version.Version==discarded { t.Fatal("publication stamped invalid or discarded physical version",prior) }
	}
	if last.PrivateHistory.Version.Born!=0 || last.PrivateHistory.Next!=first.PrivateHistory { t.Fatal("publication mutated prepared private root") }
	if len(after.Root.Entries)!=1 || len(base.Root.Entries)!=1 || after.Root.Entries[0].Version!=after.Store.Versions[0].Version || base.Root.Entries[0].Version!=original.Version { t.Fatal("publication lost current index membership or mutated saved root") }
	enginetest.Run(t,e,"VACUUM retained_heap")
	protected:=pgstatindexCaptureFixture(t,e,nil,"retained_index")
	if protected.Store.Retained!=after.Store.Retained || len(protected.Root.Entries)!=1 {
		t.Fatal("VACUUM retired history still protected by an actual reader")
	}
	sessionSQL(t,horizon,"ROLLBACK")
	enginetest.Run(t,e,"VACUUM retained_heap")
	vacuumed:=pgstatindexCaptureFixture(t,e,nil,"retained_index")
	if vacuumed.Store.Retained!=nil || len(vacuumed.Root.Entries)!=1 || heapPredecessorCount(after.Store.Retained)!=2 || len(after.Root.Entries)!=1 { t.Fatal("VACUUM mutated an old root or failed to retire eligible heap history") }
}

func TestPgstattupleHeapPublication(t *testing.T) {
	e:=engine.New()
	pgstatindexUnitInstalled(t,e)
	for _,q:=range []string{"CREATE TABLE retained_a(n integer)","CREATE TABLE retained_b(n integer)","INSERT INTO retained_a VALUES(1)","INSERT INTO retained_b VALUES(1)","CREATE INDEX retained_ai ON retained_a(n)","CREATE INDEX retained_bi ON retained_b(n)"} { enginetest.Run(t,e,q) }
	s,finisher:=sessionStart(t,e,nil),sessionStart(t,e,nil)
	for _,q:=range []string{"BEGIN","UPDATE retained_a SET n=2","UPDATE retained_a SET n=3","UPDATE retained_b SET n=2"} { sessionSQL(t,s,q) }
	if err:=preparedUnitControl(t,e,s,parser.KindPrepareTransaction,"veto"); err!=nil { t.Fatal(err) }
	beforeA,beforeB:=pgstatindexCaptureFixture(t,e,nil,"retained_ai"),pgstatindexCaptureFixture(t,e,nil,"retained_bi")
	entry:=core.EnginePreparedRef(e).Lookup("veto")
	privateA:=entry.Writes[beforeA.Parent.Address()][0].PrivateHistory
	ctx,cancel:=context.WithCancel(context.Background())
	op,err:=finisher.Acquire(ctx)
	if err!=nil { t.Fatal(err) }
	cancel()
	err=core.EngineCompletePrepared(e,op,"veto",true)
	op.Done()
	if !errors.Is(err,context.Canceled) { t.Fatal(err) }
	e.Mu.Lock()
	frontier:=e.Visibility.CommitSeq()
	e.Visibility.SetCommitSeqLocked(ids.CommitSeq(math.MaxUint64))
	e.Mu.Unlock()
	if err:=preparedUnitControl(t,e,finisher,parser.KindCommitPrepared,"veto"); err==nil { t.Fatal("overflow publication was accepted") }
	e.Mu.Lock()
	e.Visibility.SetCommitSeqLocked(frontier)
	e.Mu.Unlock()
	for _,before:=range []struct{ name string; snapshot storage.Relation; root *storage.IndexRoot }{{"retained_ai",beforeA.Store,beforeA.Root},{"retained_bi",beforeB.Store,beforeB.Root}} {
		after:=pgstatindexCaptureFixture(t,e,nil,before.name)
		if !storage.SameVersionRoot(after.Store.Versions,before.snapshot.Versions) || after.Store.Retained!=before.snapshot.Retained || after.Store.Ends!=before.snapshot.Ends || after.Store.Generation!=before.snapshot.Generation || after.Root!=before.root { t.Fatal("all-relations veto changed a committed physical root") }
	}
	if entry.Writes[beforeA.Parent.Address()][0].PrivateHistory!=privateA || privateA.Version.Born!=0 { t.Fatal("veto changed prepared actual private history") }
	if err:=preparedUnitControl(t,e,finisher,parser.KindCommitPrepared,"veto"); err!=nil { t.Fatal(err) }
	if heapPredecessorCount(pgstatindexCaptureFixture(t,e,nil,"retained_ai").Store.Retained)!=2 || heapPredecessorCount(pgstatindexCaptureFixture(t,e,nil,"retained_bi").Store.Retained)!=1 { t.Fatal("retry lost or duplicated actual predecessors") }
	if core.EnginePreparedRef(e).Len()!=0 { t.Fatal("successful prepared publication was not consumed") }
}

// Physical index statistics intentionally follow current logical membership,
// unlike the real heap history retained for logical live/dead classification.
func TestPgstattupleHeapIndexSimulation(t *testing.T) {
	for _, tc := range []struct {
		method, definition string
		initial, afterDelete int64
	}{
		{"btree", "USING btree(lower(s)) INCLUDE(n) WHERE n>0", 2, 1},
		{"hash", "USING hash(n)", 3, 2},
	} {
		t.Run(tc.method, func(t *testing.T) {
			e := engine.New()
			for _, q := range []string{
				"CREATE TABLE simulation_heap(n integer,s text)",
				"INSERT INTO simulation_heap VALUES(1,'A'),(2,'B'),(0,'C')",
				"CREATE INDEX simulation_index ON simulation_heap "+tc.definition,
			} { enginetest.Run(t,e,q) }
			capture := func() index.PgstattupleStatsCapture { return pgstatindexCaptureFixture(t,e,nil,"simulation_index") }
			assert := func(c index.PgstattupleStatsCapture, count int64) storage.HeapStatistics {
				t.Helper()
				stats, err := index.CaptureTupleStatistics(context.Background(),c,false)
				if err!=nil || stats.TupleCount!=count || stats.DeadCount!=0 || stats.DeadLen!=0 || stats.TableLen%8192!=0 || stats.FreeSpace<0 || stats.FreeSpace>stats.TableLen {
					t.Fatal("current-root simulation",stats,err)
				}
				pages, err := index.CaptureTupleStatistics(context.Background(),c,true)
				if err!=nil || pages.TableLen!=stats.TableLen || count>0 && stats.TupleLen<=0 { t.Fatal("inconsistent simulated layout",stats,pages,err) }
				return stats
			}
			before := capture()
			initial := assert(before,tc.initial)
			for _, q := range []string{"UPDATE simulation_heap SET n=4,s='D' WHERE n=1","UPDATE simulation_heap SET n=5,s='E' WHERE n=4"} { enginetest.Run(t,e,q) }
			assert(capture(),tc.initial)
			enginetest.Run(t,e,"DELETE FROM simulation_heap WHERE n=5")
			assert(capture(),tc.afterDelete)
			enginetest.Run(t,e,"DELETE FROM simulation_heap")
			assert(capture(),0)
			enginetest.Run(t,e,"VACUUM simulation_heap")
			assert(capture(),0)
			if old:=assert(before,tc.initial); old!=initial { t.Fatal("publication or maintenance mutated a saved capture") }
		})
	}
}
