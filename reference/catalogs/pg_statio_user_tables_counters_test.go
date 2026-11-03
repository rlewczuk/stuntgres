package reference_test

import (
 "database/sql"
 "fmt"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePGStatioUserTablesCounters(t *testing.T) {
 var vectors [][]statioTableRow
	for repeat:=range 2 {t.Run(fmt.Sprint(repeat),func(t *testing.T) {
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
			a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
			statioSequenceExec(t,p,b,a,"CREATE TABLE statio_user_counter(k integer,v integer)")
			statioSequenceExec(t,p,b,a,"ALTER TABLE statio_user_counter SET (autovacuum_enabled=false)")
			statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency=none")
			oid:=statioSequenceOID(t,p,a,"statio_user_counter")
			statioUserTableSeed(t,p,b,oid,"INSERT INTO statio_user_counter VALUES(1,10),(2,20)",2)
			held,heldStmt:=statioUserTableHeld(t,p,observer);defer heldStmt.Close()
			vector:=[]statioTableRow{}
			r:=statioUserTableOne(t,p,b,observer,oid); statioTableHeap(t,b,r,0); statioTableAbsentToast(t,r)
			if r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("no-index SUM is NULL",r)}
			stmt,err:=a.PrepareContext(p.Ctx,"SELECT k FROM statio_user_counter");if err!=nil {t.Fatal(err)};stmt.Close()
			statioTableHeap(t,b,statioUserTableOne(t,p,b,observer,oid),0)
			published:=statioUserTableOne(t,p,b,observer,oid)
			for n:=int64(1);n<=3;n++ {
				timing:="off";if n==2 {timing="on"}
				statioTableWork(t,p,b,[]string{"SET track_io_timing="+timing,"SET track_wal_io_timing="+timing},"SELECT k FROM statio_user_counter ORDER BY k",[][]any{{int64(1)},{int64(2)}})
				got:=statioUserTablePublished(t,p,b,published)
				statioTableHeap(t,b,got,n)
				// Both public routes must expose the independently observed
				// increasing vector, not merely nonnegative physical counters.
				for _,e:=range []fixture.Executor{observer,held} {
					current:=statioUserTableOne(t,p,b,e,oid)
					statioTableHeap(t,b,current,n)
					if current!=got {t.Fatal("current direct/held published vector",got,current)}
				}
				published=got
				got.oid=0;vector=append(vector,got)
			}
			if !b.PG {
				statioTableWork(t,p,b,[]string{"SET track_counts=off"},"SELECT k FROM statio_user_counter ORDER BY k",[][]any{{int64(1)},{int64(2)}})
				statioTableHeap(t,b,statioUserTableOne(t,p,b,observer,oid),3)
				statioTableWork(t,p,b,[]string{"SET track_counts=on"},"SELECT k FROM statio_user_counter ORDER BY k",[][]any{{int64(1)},{int64(2)}})
				statioTableHeap(t,b,statioUserTableOne(t,p,b,observer,oid),4)
			}
			statioSequenceExec(t,p,b,a,"CREATE TABLE statio_user_empty(k integer)")
			empty:=statioSequenceOID(t,p,a,"statio_user_empty")
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_empty",[][]any{})
			statioTableHeap(t,b,statioUserTableOne(t,p,b,observer,empty),0)
			for _,q:=range []string{"CREATE INDEX statio_user_counter_i ON statio_user_counter USING hash(k)","CREATE INDEX statio_user_counter_unused ON statio_user_counter USING hash(v)"} {statioSequenceExec(t,p,b,a,q)}
			r=statioUserTableIndexSum(t,p,b,observer,oid)
			statioTableAbsentToast(t,r)
			if !r.blocks[2].Valid || !r.blocks[3].Valid {t.Fatal("existing zero indexes are numeric",r)}
			if !b.PG && (r.blocks[2] != (sql.NullInt64{Valid:true}) || r.blocks[3] != (sql.NullInt64{Valid:true})) {t.Fatal("existing zero-activity indexes yield numeric zero",r)}
			for n:=range int64(3) {
				statioTableWork(t,p,b,[]string{"SET enable_seqscan=off"},"SELECT k FROM statio_user_counter WHERE k=1",[][]any{{int64(1)}})
				r=statioUserTableIndexSum(t,p,b,held,oid)
				if !b.PG && (r.blocks[2].Int64!=1 || r.blocks[3].Int64!=n) {t.Fatal("representative index page cold/hit",n,r)}
			}
			statioSequenceExec(t,p,b,a,"DROP INDEX statio_user_counter_unused")
			statioUserTableIndexSum(t,p,b,observer,oid)
			statioSequenceExec(t,p,b,a,"DROP INDEX statio_user_counter_i")
			r=statioUserTableIndexSum(t,p,b,observer,oid)
			if r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("last index drop restores NULL SUM",r)}
			if !b.PG {vectors=append(vectors,vector)}
		})}
	})}
 if len(vectors)!=2 || !reflect.DeepEqual(vectors[0],vectors[1]) {t.Fatal("deterministic independent-driver vectors",vectors)}
}

