package statistics_test

import (
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"sync"
	"testing"
	"time"
)

func databaseResetKeys() (statistics.Key, statistics.Key, statistics.Key) {
	d := new(ids.Domain)
	return statistics.Key{Object: d.Ref(0, ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 10}), Category: statistics.Database}, statistics.Key{Object: d.Ref(3, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20}), Category: statistics.Relation}, statistics.Key{Object: d.Ref(3, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 30}), Category: statistics.Function}
}
func databaseResetSeed(p *statistics.Provider, values map[statistics.Key]statistics.Fact, at time.Time) {
	var w statistics.Writer
	for key, fact := range values {
		w.Add(p, key, fact)
	}
	w.Flush(p, at, true)
}
func databaseResetVector() statistics.DatabaseFact {
	return statistics.DatabaseFact{Commits: 1, Rollbacks: 2, BlocksRead: 3, BlocksHit: 4, Returned: 5, Fetched: 6, Inserts: 7, Updates: 8, Deletes: 9, Conflicts: 10, TempFiles: 11, TempBytes: 12, Deadlocks: 13, ChecksumFailures: 14, ChecksumLastFailure: time.Unix(50, 0), ResetAt: time.Unix(60, 0), ReadTime: 15, WriteTime: 16, SessionTime: 17, ActiveTime: 18, IdleTransactionTime: 19, Sessions: 20, Abandoned: 21, Fatal: 22, Killed: 23, WorkersPlanned: 24, WorkersLaunched: 25}
}
func databaseResetRelationVector() statistics.Fact {
	return statistics.Fact{Inserts: 1, Updates: 2, Deletes: 3, Live: 4, Dead: 5, Modifications: 6, InsertsSince: 7, IndexBlocksRead: 8, IndexBlocksHit: 9, VacuumCount: 10, AnalyzeCount: 11, LastVacuum: time.Unix(10, 0), LastAnalyze: time.Unix(11, 0), IndexScans: 12, IndexEntries: 13, IndexFetches: 14, LastIndexScan: time.Unix(12, 0), SeqScans: 15, SeqTuples: 16, TableIndexFetches: 17, LastSeqScan: time.Unix(13, 0), Truncated: true, VacuumTime: 18, AnalyzeTime: 19, SequencePageAccesses: 20, BlocksFetched: 21, BlocksHit: 22}
}
func TestStatsProviderResetDatabase(t *testing.T) {
	var p statistics.Provider
	db, a, f := databaseResetKeys()
	shared := a
	shared.Object = shared.Object.Domain().Ref(3, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1260})
	t0, t1 := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC()
	p.Replace(t0, map[statistics.Key]statistics.Fact{db: {Database: databaseResetVector()}, a: databaseResetRelationVector(), f: {Calls: 3, TotalTime: 4, SelfTime: 2}, shared: {Inserts: 29}})
	controls := make(map[statistics.Key]statistics.Fact)
	for _, category := range []statistics.Category{statistics.IO, statistics.WAL, statistics.ReplicationSlot, statistics.Subscription} {
		key := a
		key.Category = category
		databaseResetSeed(&p, map[statistics.Key]statistics.Fact{key: {Operations: 31, SpillTxns: 37, ApplyErrorCount: 41}}, t0)
		controls[key] = p.Read(key)
	}
	token := db.Object.Domain().Session(1)
	p.BeginBackend(token, 1)
	var fixedWriter statistics.Writer
	for kind := statistics.SharedKind(0); kind < statistics.SharedKindCount; kind++ {
		fixedWriter.Shared(&p, kind, sharedVector(31))
	}
	fixedWriter.SLRU(&p, statistics.Transaction, slruVector(37))
	fixedWriter.Backend(&p, token, statistics.Fact{Operations: 41}, statistics.Fact{Operations: 43})
	fixedWriter.Flush(&p, t0, true)
	p.RegisterActivity(token, statistics.Activity{})
	activityBefore := p.CaptureActivities()
	var fixedBefore [statistics.SharedKindCount]statistics.SharedFact
	for kind := range fixedBefore {
		fixedBefore[kind] = p.ReadShared(statistics.SharedKind(kind))
	}
	slruBefore := p.ReadSLRU(statistics.Transaction)
	backendBefore, _ := p.ReadBackend(token)
	sharedBefore := p.Read(shared)
	generation := p.Generation()
	p.ResetDatabase(db, 3, []ids.OID{1260}, t1)
	if got := p.Read(db).Database; got != (statistics.DatabaseFact{ResetAt: t1}) {
		t.Fatal("database fields", got)
	}
	for _, key := range []statistics.Key{a, f} {
		got := p.Read(key)
		if got != (statistics.Fact{ResetAt: t0, Generation: generation}) {
			t.Fatal("object fields", key, got)
		}
	}
	if p.Read(shared) != sharedBefore || p.Generation() != generation {
		t.Fatal("shared/generation changed")
	}
	for key, want := range controls {
		if p.Read(key) != want {
			t.Fatal("category changed", key)
		}
	}
	for kind, want := range fixedBefore {
		if p.ReadShared(statistics.SharedKind(kind)) != want {
			t.Fatal("fixed family changed", kind)
		}
	}
	if got, ok := p.ReadBackend(token); !ok || got != backendBefore || p.ReadSLRU(statistics.Transaction) != slruBefore || !reflect.DeepEqual(p.CaptureActivities(), activityBefore) {
		t.Fatal("backend/activity/SLRU changed")
	}
	var empty statistics.Provider
	if !empty.Read(db).Database.ResetAt.IsZero() {
		t.Fatal("startup is not reset")
	}
	empty.ResetDatabase(db, 3, nil, t1)
	if empty.Read(db).Database != (statistics.DatabaseFact{ResetAt: t1}) {
		t.Fatal("absent database timestamp")
	}
	t.Run("concurrent_capture", func(t *testing.T) {
		var wait sync.WaitGroup
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				databaseResetSeed(&p, map[statistics.Key]statistics.Fact{a: {Inserts: 5}, db: {Database: statistics.DatabaseFact{ResetAt: t0}}}, t0)
				p.ResetDatabase(db, 3, nil, t1)
			}
		}()
		for range 100 {
			var r statistics.Reader
			r.Configure("snapshot", 1)
			aFact := r.Read(&p, a, t0)
			dbFact := r.Read(&p, db, t0).Database
			if !(aFact.Inserts == 0 && dbFact.ResetAt == t1 || aFact.Inserts == 5 && dbFact.ResetAt == t0) {
				t.Error("torn object/timestamp", aFact, dbFact)
			}
		}
		wait.Wait()
	})
}
func TestStatsProviderResetObject(t *testing.T) {
	var p statistics.Provider
	db, a, f := databaseResetKeys()
	b := a
	b.Object = b.Object.Domain().Ref(3, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 21})
	t1 := time.Unix(200, 0)
	p.Replace(time.Time{}, map[statistics.Key]statistics.Fact{db: {Database: databaseResetVector()}, a: databaseResetRelationVector(), b: {IndexScans: 7}, f: {Calls: 3, TotalTime: 4, SelfTime: 2}})
	beforeDB, beforeB, beforeF := p.Read(db).Database, p.Read(b), p.Read(f)
	p.ResetObject(a.Object, a.Category, &db, t1)
	if got := p.Read(a); got != (statistics.Fact{Generation: p.Generation()}) {
		t.Fatal("relation fields", got)
	}
	beforeDB.ResetAt = t1
	if p.Read(db).Database != beforeDB || p.Read(b) != beforeB || p.Read(f) != beforeF {
		t.Fatal("single isolation")
	}
	p.ResetObject(f.Object, f.Category, &db, t1)
	if p.Read(f) != (statistics.Fact{Generation: p.Generation()}) {
		t.Fatal("function fields")
	}
	before := reflect.ValueOf(&p).Elem().FieldByName("facts").Len()
	absent := a.Object.Domain().Ref(3, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 0xffffffff})
	p.ResetObject(absent, statistics.Relation, &db, t1)
	if reflect.ValueOf(&p).Elem().FieldByName("facts").Len() != before {
		t.Fatal("absent allocated object")
	}
}
func TestStatsProviderResetDatabasePending(t *testing.T) {
	var p statistics.Provider
	db, a, f := databaseResetKeys()
	t0, t1 := time.Unix(100, 0), time.Unix(101, 0)
	p.Reset(t0)
	var w statistics.Writer
	w.Add(&p, a, statistics.Fact{Inserts: 5})
	w.Flush(&p, t0, true)
	w.Add(&p, a, statistics.Fact{Inserts: 2})
	w.Add(&p, db, statistics.Fact{Database: statistics.DatabaseFact{Commits: 2}})
	token := db.Object.Domain().Session(1)
	p.BeginBackend(token, 1)
	w.Shared(&p, statistics.Archiver, sharedVector(3))
	w.SLRU(&p, statistics.Transaction, slruVector(4))
	w.Backend(&p, token, statistics.Fact{Operations: 5}, statistics.Fact{Operations: 6})
	retiredKey := a
	retiredKey.Object = a.Object.Domain().Ref(3, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 99})
	p.Retire(retiredKey)
	usage, ok := w.BeginFunction(&p, f)
	if !ok {
		t.Fatal("function admission")
	}
	wholeWriter := w
	pendingA := reflect.ValueOf(w).FieldByName("pending").MapIndex(reflect.ValueOf(a))
	pendingDB := reflect.ValueOf(w).FieldByName("pending").MapIndex(reflect.ValueOf(db))
	writerBefore := reflect.ValueOf(w)
	pending := writerBefore.FieldByName("pending").UnsafePointer()
	last := writerBefore.FieldByName("last")
	generation := p.Generation()
	retired := reflect.ValueOf(&p).Elem().FieldByName("retired").UnsafePointer()
	p.ResetDatabase(db, 3, nil, t1)
	if !w.Pending() || p.Read(a).Inserts != 0 || p.Generation() != generation || reflect.ValueOf(&w).Elem().FieldByName("pending").UnsafePointer() != pending || !reflect.ValueOf(&w).Elem().FieldByName("last").Equal(last) || reflect.ValueOf(&p).Elem().FieldByName("retired").UnsafePointer() != retired {
		t.Fatal("pending owner changed")
	}
	if !reflect.DeepEqual(w, wholeWriter) || !reflect.ValueOf(w).FieldByName("pending").MapIndex(reflect.ValueOf(a)).Equal(pendingA) || !reflect.ValueOf(w).FieldByName("pending").MapIndex(reflect.ValueOf(db)).Equal(pendingDB) {
		t.Fatal("writer scalar/partition/baseline changed")
	}
	w.EndFunction(&p, usage, 3*time.Millisecond)
	w.Flush(&p, t1, true)
	if p.Read(a).Inserts != 2 || p.Read(db).Database.Commits != 2 || p.Read(f).Calls != 1 || p.Read(f).TotalTime != 3*time.Millisecond {
		t.Fatal("legitimate late deltas", p.Read(a), p.Read(db), p.Read(f))
	}
	w.Add(&p, a, statistics.Fact{Inserts: 9})
	p.Reset(t1)
	w.Flush(&p, t1, true)
	if p.Read(a).Inserts != 0 {
		t.Fatal("native replacement accepted stale batch")
	}
}
func TestStatsProviderResetDatabaseReaders(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			var p statistics.Provider
			db, a, _ := databaseResetKeys()
			t0, t1 := time.Unix(100, 0), time.Unix(200, 0)
			databaseResetSeed(&p, map[statistics.Key]statistics.Fact{a: {Inserts: 5}, db: {Database: statistics.DatabaseFact{Commits: 7, ResetAt: t0}}}, t0)
			var r statistics.Reader
			r.Configure(mode, 1)
			oldA, oldDB := r.Read(&p, a, t0), r.Read(&p, db, t0)
			stamp := r.Timestamp()
			p.ResetDatabase(db, 3, nil, t1)
			if r.Timestamp() != stamp {
				t.Fatal("reset changed snapshot stamp")
			}
			if mode == "none" {
				if r.Read(&p, a, t1).Inserts != 0 || r.Read(&p, db, t1).Database.ResetAt != t1 {
					t.Fatal("none stale")
				}
			} else if r.Read(&p, a, t1) != oldA || r.Read(&p, db, t1) != oldDB {
				t.Fatal("retained capture changed")
			}
			r.Clear()
			if r.Read(&p, a, t1).Inserts != 0 || r.Read(&p, db, t1).Database != (statistics.DatabaseFact{ResetAt: t1}) {
				t.Fatal("clear stale")
			}
		})
	}
}
func TestStatsProviderResetDatabaseIdentity(t *testing.T) {
	var p statistics.Provider
	db, a, f := databaseResetKeys()
	t0, t1 := time.Unix(100, 0), time.Unix(200, 0)
	foreign, old, wrongClass, wrongCategory := a, a, a, a
	foreign.Object = new(ids.Domain).Ref(3, a.Object.Address)
	old.Object = a.Object.Domain().Ref(2, a.Object.Address)
	address := a.Object.Address
	address.ClassID = ids.ProcedureClassID
	wrongClass.Object = a.Object.Domain().Ref(3, address)
	wrongCategory.Category = statistics.IO
	keys := []statistics.Key{a, foreign, old, wrongClass, wrongCategory}
	for _, key := range keys {
		databaseResetSeed(&p, map[statistics.Key]statistics.Fact{key: {Inserts: 5, Operations: 7}}, t0)
	}
	controls := make(map[statistics.Key]statistics.Fact)
	for _, key := range keys[1:] {
		controls[key] = p.Read(key)
	}
	p.ResetDatabase(db, 3, nil, t1)
	for key, want := range controls {
		if p.Read(key) != want {
			t.Fatal("foreign authority changed", key)
		}
	}
	var late statistics.Writer
	late.Add(&p, a, statistics.Fact{Inserts: 2})
	usage, _ := late.BeginFunction(&p, f)
	p.Retire(a)
	p.Retire(f)
	p.ResetObject(a.Object, a.Category, &db, t1)
	p.ResetObject(f.Object, f.Category, &db, t1)
	late.Flush(&p, t1, true)
	if p.Read(a).Inserts != 0 {
		t.Fatal("retired resurrected")
	}
	late.EndFunction(&p, usage, time.Millisecond)
	late.Flush(&p, t1, true)
	if p.Read(f).Calls != 0 || reflect.ValueOf(&p).Elem().FieldByName("retired").Len() != 2 {
		t.Fatal("retirement authority lost")
	}
}
