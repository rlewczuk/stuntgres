package core_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func bloomSetup(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	for _, sql := range []string{
		"CREATE EXTENSION bloom", "CREATE TABLE bloom_rows(i integer,s text)",
		"INSERT INTO bloom_rows VALUES(7,'a'),(7,'a'),(7,NULL),(NULL,'a'),(NULL,NULL),(8,'b'),(8,'a')",
		"CREATE INDEX bloom_rows_idx ON bloom_rows USING bloom(i,s) WITH(length=1,col1=1,col2=1)",
	} { enginetest.Run(t, e, sql) }
	return e
}

func bloomSignatures(root *storage.IndexRoot) [][]uint16 {
	out := make([][]uint16, len(root.Bloom.Entries))
	for i, entry := range root.Bloom.Entries { out[i] = slices.Clone(entry.Signature) }
	return out
}

func bloomAssertRoot(t *testing.T, e *core.Engine, name string, count int) *storage.IndexRoot {
	t.Helper()
	d, root := bloomRoot(t, e, name)
	store := e.Storage.Relations[d.SQLIndex.Parent]
	if len(root.Entries) != count || root.Bloom.Storage != store || root.Bloom.Generation != store.Generation || root.Bloom.Ends != store.Ends || root.Bloom.Prefix != len(store.Versions) {
		t.Fatal("incomplete publication authority", root, store)
	}
	for i, entry := range root.Entries {
		if entry.Position < 0 || entry.Position >= len(store.Versions) { t.Fatal("position", entry) }
		row := store.Versions[entry.Position]
		if row.Row != entry.Row || row.Version != entry.Version || len(root.Bloom.Entries[i].Signature) != root.Bloom.Options.Words {
			t.Fatal("signature not owned by exact heap version", entry)
		}
	}
	return root
}

func TestBloomSignature(t *testing.T) {
	e := bloomSetup(t)
	_, root := bloomRoot(t, e, "bloom_rows_idx")
	if root.Bloom.Options.Words != 1 || len(root.Entries) != 7 { t.Fatal("effective length or membership", root.Bloom) }
	for i, entry := range root.Entries {
		want := make([]uint16, root.Bloom.Options.Words)
		for column, value := range entry.Keys {
			if value.IsNull() { continue }
			hash, err := root.Bloom.Bindings[column].Provider.Hash(context.Background(), value)
			if err != nil { t.Fatal(err) }
			// Independent arithmetic spelling of the release recurrence and
			// ordinal-seed mixing, not a call to the signature producer.
			seed := (int64(column)+1)*16807%2147483647-1
			seed = int64((uint32(hash)^uint32(seed))%2147483646)+1
			for range root.Bloom.Options.Bits[column] {
				seed = seed*16807%2147483647
				bit := int(seed-1)%(len(want)*16)
				want[bit/16] |= 1<<uint(bit%16)
			}
		}
		if !slices.Equal(want, root.Bloom.Entries[i].Signature) { t.Fatal("wrong real signature", i, want, root.Bloom.Entries[i]) }
	}
	if root.Bloom.Entries[4].Signature[0] != 0 || root.Bloom.Entries[3].Signature[0] == 0 || &root.Bloom.Entries[0].Signature[0] == &root.Bloom.Entries[1].Signature[0] {
		t.Fatal("NULL attribute or duplicate signature ownership collapsed")
	}
	for _, tc := range []struct{ length, words int }{{7,1},{16,1},{17,2},{4096,256}} {
		previous := root
		before := bloomSignatures(previous)
		enginetest.Run(t,e,fmt.Sprintf("ALTER INDEX bloom_rows_idx SET(length=%d,col32=4095)",tc.length))
		_,root=bloomRoot(t,e,"bloom_rows_idx")
		if root.Bloom.Options.Words!=tc.words || root.Bloom.Options.Bits[31]!=4095 || !reflect.DeepEqual(before,bloomSignatures(previous)) { t.Fatal("option rebuild mutated predecessor or used lexical bits",tc,root.Bloom) }
	}
	enginetest.Run(t,e,"ALTER INDEX bloom_rows_idx RESET(length,col32)")
	_,reset:=bloomRoot(t,e,"bloom_rows_idx")
	if reset.Bloom.Options.Words!=5 || reset.Bloom.Options.Bits[31]!=2 { t.Fatal("RESET did not atomically rebuild defaults") }
}

func TestBloomRootPublication(t *testing.T) {
	e:=bloomSetup(t)
	old:=bloomAssertRoot(t,e,"bloom_rows_idx",7)
	before:=bloomSignatures(old)
	enginetest.Run(t,e,"INSERT INTO bloom_rows VALUES(9,'z')")
	inserted:=bloomAssertRoot(t,e,"bloom_rows_idx",8)
	for i:=range old.Entries {
		if &inserted.Bloom.Entries[i].Signature[0]!=&old.Bloom.Entries[i].Signature[0] { t.Fatal("append rehashed/copied unchanged versions") }
	}
	enginetest.Run(t,e,"UPDATE bloom_rows SET i=9 WHERE i=7")
	updated:=bloomAssertRoot(t,e,"bloom_rows_idx",8)
	for _,entry:=range updated.Entries {
		for _,prior:=range old.Entries {
			if entry.Row==prior.Row && entry.Keys[0]!=prior.Keys[0] && entry.Version==prior.Version { t.Fatal("updated signature kept ended version") }
		}
	}
	enginetest.Run(t,e,"DELETE FROM bloom_rows WHERE i=8")
	retained:=bloomAssertRoot(t,e,"bloom_rows_idx",6)
	if !reflect.DeepEqual(before,bloomSignatures(old)) { t.Fatal("DML mutated old root") }
	s:=enginetest.StartSession(t,e,nil)
	tx:=enginetest.TransactionBegin(t,s)
	enginetest.SessionSQL(t,s,"INSERT INTO bloom_rows VALUES(9,'private')")
	enginetest.SessionSQL(t,s,"SAVEPOINT bloom_save")
	enginetest.SessionSQL(t,s,"DELETE FROM bloom_rows WHERE i=9")
	enginetest.SessionSQL(t,s,"ROLLBACK TO bloom_save")
	if _,current:=bloomRoot(t,e,"bloom_rows_idx");current!=retained { t.Fatal("private writes changed published signatures") }
	enginetest.TransactionEnd(t,s,tx,false)
	if _,current:=bloomRoot(t,e,"bloom_rows_idx");current!=retained { t.Fatal("rollback changed root") }
	enginetest.Run(t,e,"ALTER TABLE bloom_rows ADD COLUMN extra integer")
	layout:=bloomAssertRoot(t,e,"bloom_rows_idx",6)
	if layout.Bloom.Storage==nil { t.Fatal("layout dropped signatures") }
	enginetest.Run(t,e,"ALTER TABLE bloom_rows ALTER COLUMN s TYPE varchar(20)")
	typed:=bloomAssertRoot(t,e,"bloom_rows_idx",6)
	if typed.Bloom.Bindings[1].Usage.Type.BaseType().ID()!=sqltype.VarChar || typed==layout { t.Fatal("TYPE failed to rebind/rebuild") }
	enginetest.Run(t,e,"REINDEX INDEX bloom_rows_idx")
	reindexed:=bloomAssertRoot(t,e,"bloom_rows_idx",6)
	if reindexed==typed || reindexed.Construction==typed.Construction { t.Fatal("REINDEX did not build real signatures") }
	enginetest.Run(t,e,"VACUUM bloom_rows")
	vacuumed := bloomAssertRoot(t,e,"bloom_rows_idx",6)
	if vacuumed == reindexed || vacuumed.Bloom.Generation <= reindexed.Bloom.Generation || vacuumed.Construction != reindexed.Construction {
		t.Fatal("VACUUM failed to rebind immutable signatures to changed heap authority")
	}
	for i := range reindexed.Entries {
		if reindexed.Entries[i].Row != vacuumed.Entries[i].Row || reindexed.Entries[i].Version != vacuumed.Entries[i].Version || &reindexed.Bloom.Entries[i].Signature[0] != &vacuumed.Bloom.Entries[i].Signature[0] {
			t.Fatal("VACUUM rehashed or changed unchanged version signatures")
		}
	}
	rootForParent := func(name string, count int) *storage.IndexRoot {
		parent := enginetest.RoleTableRef(t, e, name).Address
		for _, descriptor := range e.CatalogRead().Relations() {
			if descriptor.SQLIndex != nil && descriptor.SQLIndex.Parent == parent {
				return bloomAssertRoot(t, e, descriptor.Name(), count)
			}
		}
		t.Fatal("copied parent has no bloom index", name)
		return nil
	}
	enginetest.Run(t, e, "CREATE TABLE bloom_like (LIKE bloom_rows INCLUDING INDEXES)")
	emptyCopy := rootForParent("bloom_like", 0)
	enginetest.Run(t, e, "INSERT INTO bloom_like VALUES(11,'copy',NULL)")
	filledCopy := rootForParent("bloom_like", 1)
	if filledCopy == emptyCopy || filledCopy.Bloom.Storage == reindexed.Bloom.Storage || len(emptyCopy.Entries) != 0 {
		t.Fatal("LIKE retained source/empty signature authority")
	}
	for _, sql := range []string{
		"CREATE TABLE bloom_partition(i integer,s text) PARTITION BY RANGE(i)",
		"CREATE TABLE bloom_partition_a PARTITION OF bloom_partition FOR VALUES FROM(0) TO(10)",
		"CREATE INDEX bloom_partition_idx ON bloom_partition USING bloom(i,s) WITH(length=1,col2=1)",
		"INSERT INTO bloom_partition VALUES(7,'a'),(7,'a')",
	} {
		enginetest.Run(t, e, sql)
	}
	partitioned := enginetest.RoleTableRef(t, e, "bloom_partition_idx")
	if e.Storage.IndexRoots[partitioned.Address] != nil { t.Fatal("virtual bloom index has physical signatures") }
	leaf := rootForParent("bloom_partition_a", 2)
	if leaf.Bloom.Options.Words != 1 || leaf.Bloom.Options.Bits[1] != 1 {
		t.Fatal("partition propagation discarded signature options")
	}
	bloomAssertRoot(t, e, "bloom_rows_idx", 6)
}

// This is a genuine command-admitted force preference, not a candidate/root
// injection. The captured current root still has to pass ordinary admission.
func bloomCapture(t *testing.T,e *core.Engine,sql string)(*plan.Query,*exec.Source,func()){
	t.Helper()
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"SET enable_seqscan=off")
	op,_,err:=s.AcquireCommand(context.Background());if err!=nil{t.Fatal(err)}
	query,source,release:=enginetest.HashCaptureQuery(t,e,op,sql)
	done:=func(){release();op.Done()}
	t.Cleanup(done)
	return query,source,done
}

func TestBloomVisibleCapture(t *testing.T){
	e:=bloomSetup(t)
	_,old:=bloomRoot(t,e,"bloom_rows_idx")
	query,source,release:=bloomCapture(t,e,"SELECT i,s FROM bloom_rows WHERE i=7 AND s='a'")
	if len(source.BloomIndexes)!=1 || source.BloomIndexes[0].Root!=old || len(source.HashIndexes)!=0 { t.Fatal("not captured from actual production root",source.BloomIndexes) }
	enginetest.Run(t,e,"UPDATE bloom_rows SET i=9 WHERE i=7")
	enginetest.Run(t,e,"DELETE FROM bloom_rows WHERE i=8")
	stage:=hashFilterStage(t,query,source,nil)
	if source.ScanDecision.Method!=plan.BloomCandidates || stage.HashCandidates==nil || stage.HashCandidates.Bloom!=old.Bloom { t.Fatal("retained source reread latest root or fell back",source.ScanDecision) }
	if err:=stage.Drain(context.Background());err!=nil || stage.Count()!=2 { t.Fatal("old visibility/recheck",stage.Count(),err) }
	stage.Close();release()
	freshQuery,freshSource,freshRelease:=bloomCapture(t,e,"SELECT i,s FROM bloom_rows WHERE i=7")
	fresh:=hashFilterStage(t,freshQuery,freshSource,nil)
	if fresh.HashCandidates==nil || fresh.HashCandidates.Bloom==old.Bloom {t.Fatal("fresh source used old authority")}
	if err:=fresh.Drain(context.Background());err!=nil || fresh.Count()!=0{t.Fatal("ended visible version returned",fresh.Count(),err)}
	fresh.Close();freshRelease()
	for _,q:=range []string{"SELECT i FROM bloom_rows WHERE i=9 OR s='a'","SELECT i FROM bloom_rows WHERE i>7","SELECT i FROM bloom_rows WHERE i IS NULL"}{
		query,source,release:=bloomCapture(t,e,q)
		stage:=hashFilterStage(t,query,source,nil)
		if source.ScanDecision.Method!=plan.FullScan || stage.HashCandidates!=nil{t.Fatal("unsupported query pruning admitted",q)}
		stage.Close();release()
	}
	// Generation, end-root and historical/private mismatches are rejected at
	// capture, not transformed into an authoritative empty candidate set.
	d,_:=bloomRoot(t,e,"bloom_rows_idx")
	store:=e.Storage.Relations[d.SQLIndex.Parent]
	for _,mutate:=range []func(){func(){store.Generation++},func(){store.Ends=&storage.DeletionRoot{}}}{
		generation,ends:=store.Generation,store.Ends
		e.Mu.Lock();mutate();e.Mu.Unlock()
		_,source,release:=bloomCapture(t,e,"SELECT i FROM bloom_rows WHERE i=9")
		if len(source.BloomIndexes)!=0{t.Fatal("mismatched committed authority admitted")}
		release()
		e.Mu.Lock();store.Generation,store.Ends=generation,ends;e.Mu.Unlock()
	}
	s:=enginetest.StartSession(t,e,nil)
	tx:=enginetest.TransactionBegin(t,s)
	enginetest.SessionSQL(t,s,"INSERT INTO bloom_rows VALUES(9,'private')")
	op:=enginetest.TransactionOperation(t,s)
	_,private,closePrivate:=enginetest.HashCaptureQuery(t,e,op,"SELECT i FROM bloom_rows WHERE i=9")
	if len(private.BloomIndexes)!=0{t.Fatal("private view acquired current root")}
	closePrivate();op.Done();enginetest.TransactionEnd(t,s,tx,false)
	reader := enginetest.StartSession(t, e, nil)
	snapshot := enginetest.TransactionBegin(t, reader)
	enginetest.SessionSQL(t, reader, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
	pinned := enginetest.SessionSQL(t, reader, "SELECT i FROM bloom_rows WHERE i=9")
	if pinned.Rows != nil { pinned.Rows.Close() }
	enginetest.Run(t, e, "UPDATE bloom_rows SET i=10 WHERE i=9")
	read := enginetest.TransactionOperation(t, reader)
	historicalQuery, historical, closeHistorical := enginetest.HashCaptureQuery(t, e, read, "SELECT i FROM bloom_rows WHERE i=9")
	if len(historical.BloomIndexes) != 0 || historical.View.Data().FrontierAt(d.SQLIndex.Parent) >= e.Visibility.CommitSeq() {
		t.Fatal("historical view used current-root coverage")
	}
	historicalStage := hashFilterStage(t, historicalQuery, historical, nil)
	if historicalStage.HashCandidates != nil { t.Fatal("historical view pruned current signatures") }
	if err := historicalStage.Drain(context.Background()); err != nil || historicalStage.Count() != 3 {
		t.Fatal("historical fallback lost retained visible versions", historicalStage.Count(), err)
	}
	historicalStage.Close()
	closeHistorical()
	read.Done()
	enginetest.TransactionEnd(t, reader, snapshot, false)
}
