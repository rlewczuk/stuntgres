package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgIndexPrivateRollback(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	a,peer := fixture.LockConnections(t,p.Ctx,b.DB)
	run,other := transactionalIndexRun(t,p,b,a),transactionalIndexRun(t,p,b,peer)
	s := pgIndexLogical("pgi_private",nil)
	stmt,err := a.PrepareContext(p.Ctx,s.Query); if err != nil { t.Fatal(err) }; defer stmt.Close()
	held := fixture.HeldStatement{Executor:a,Statement:stmt,SQL:s.Query}
	check := func(rows [][]any) { s.Rows = rows; run(s); fixture.RunStep(t,p.Ctx,held,b.Recorder,s) }
	check(nil)
	run(fixture.Exec("BEGIN",0))
	run(fixture.Exec("CREATE INDEX pgi_private ON pgi_t((a+1)) WHERE a>0",0))
	row := pgIndexOrdinary(1,1,"0","0","1978","0","(a + 1)","(a > 0)")
	check([][]any{row}); other(pgIndexLogical("pgi_private",nil))
	var oid int64
	if err := a.QueryRowContext(p.Ctx,"SELECT 'pgi_private'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 { t.Fatal(oid,err) }
	run(fixture.Exec("SAVEPOINT mark",0)); run(fixture.Exec("DROP INDEX pgi_private",0)); check(nil)
	run(fixture.Exec("ROLLBACK TO mark",0)); check([][]any{row})
	run(fixture.Select("SELECT indexrelid=$1::oid AS same FROM pg_index WHERE indexrelid='pgi_private'::regclass",[]string{"same"},[]string{"BOOL"},[][]any{{true}},oid))
	run(fixture.Exec("ROLLBACK",0)); check(nil); other(pgIndexLogical("pgi_private",nil))
}

func TestReferencePGIndexTransactions(t *testing.T) {
	fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		a,peer := fixture.LockConnections(t,p.Ctx,b.DB)
		run,other := transactionalIndexRun(t,p,b,a),transactionalIndexRun(t,p,b,peer)
		run(fixture.Exec("CREATE TABLE pgi_t(a INT,b INT,c INT)",0))
		pgIndexPrivateRollback(t,p,b)
		for _,q := range []string{"BEGIN","CREATE INDEX pgi_tx ON pgi_t((a+1)) INCLUDE(c) WHERE a>0"} { run(fixture.Exec(q,0)) }
		row := pgIndexOrdinary(2,1,"0 3","0","1978","0","(a + 1)","(a > 0)")
		pgIndexBoth(run,pgIndexLogical("pgi_tx",[][]any{row})); other(pgIndexLogical("pgi_tx",nil))
		var oid int64
		if err := a.QueryRowContext(p.Ctx,"SELECT 'pgi_tx'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 { t.Fatal(oid,err) }
		run(fixture.Exec("COMMIT",0)); pgIndexBoth(other,pgIndexLogical("pgi_tx",[][]any{row}))
		byOID := pgIndexLogical("pgi_tx",[][]any{row})
		byOID.Query = strings.Replace(byOID.Query,"c.relname=$1","i.indexrelid=$1::oid",1); byOID.Args = []any{oid}
		stmt,err := peer.PrepareContext(p.Ctx,byOID.Query); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:peer,Statement:stmt,SQL:byOID.Query}
		checkOID := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,held,b.Recorder,s) }
		checkOID(byOID)
		var retained *sql.Rows
		var retainedWant fixture.Step
		if !b.PG {
			q := "SELECT * FROM pg_catalog.pg_index WHERE indexrelid='pgi_tx'::regclass"
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			baseline, err := peer.QueryContext(ctx, q)
			if err != nil { t.Fatal(err) }
			want := fixture.AnyValueConsume(t, baseline, b.Recorder, q, pgIndexColumns, pgIndexTypes)
			retainedWant = fixture.Select(q, pgIndexColumns, pgIndexTypes, want)
			retained, err = peer.QueryContext(ctx, q)
			if err != nil { t.Fatal(err) }
			defer retained.Close()
		}
		for _,q := range []string{"BEGIN","SAVEPOINT mark","ALTER INDEX pgi_tx RENAME TO pgi_renamed"} { run(fixture.Exec(q,0)) }
		pgIndexBoth(run,pgIndexLogical("pgi_tx",nil)); pgIndexBoth(run,pgIndexLogical("pgi_renamed",[][]any{row})); checkOID(byOID)
		pgIndexBoth(other,pgIndexLogical("pgi_tx",[][]any{row}))
		pgIndexBoth(other,pgIndexLogical("pgi_renamed",nil))
		run(fixture.Exec("ROLLBACK TO mark",0)); pgIndexBoth(run,pgIndexLogical("pgi_tx",[][]any{row})); run(fixture.Exec("COMMIT",0))
		run(fixture.Exec("ALTER INDEX pgi_tx RENAME TO pgi_renamed",0)); run(fixture.Prepared(fixture.Exec("REINDEX INDEX pgi_renamed",0))); checkOID(byOID)
		run(fixture.Exec("ALTER TABLE pgi_t RENAME COLUMN a TO renamed_a",0))
		row = pgIndexOrdinary(2,1,"0 3","0","1978","0","(renamed_a + 1)","(renamed_a > 0)"); byOID.Rows = [][]any{row}; checkOID(byOID)
		if retained != nil { fixture.RangeConsumeHeld(t, retained, retainedWant) }
		for _,prepared := range []bool{false,true} {
			s := fixture.ImplicitError("DROP INDEX pgi_renamed,pgi_missing",fixture.Diagnostic{Code:"42704",Message:`index "pgi_missing" does not exist`}); s.Prepared = prepared; run(s); checkOID(byOID)
			s = fixture.ImplicitError("ALTER TABLE pgi_t RENAME COLUMN absent TO bad",fixture.Diagnostic{Code:"42703",Message:`column "absent" does not exist`}); s.Prepared = prepared; run(s); checkOID(byOID)
		}
		run(fixture.Exec("INSERT INTO pgi_t VALUES(1,1,1),(1,2,2)",2))
		for _,prepared := range []bool{false,true} {
			s := fixture.ErrorDiagnostic("CREATE UNIQUE INDEX pgi_failed ON pgi_t(renamed_a)",fixture.ConcurrentIndexUnique("pgi_t","pgi_failed","Key (renamed_a)=(1) is duplicated.",true)); s.Prepared,s.ErrorStage = prepared,"execute"; run(s)
			pgIndexBoth(run,pgIndexLogical("pgi_failed",nil)); checkOID(byOID)
		}
		run(fixture.Exec("DROP INDEX pgi_renamed",0)); byOID.Rows = nil; checkOID(byOID)
		run(fixture.Exec("CREATE INDEX pgi_renamed ON pgi_t((renamed_a+1)) INCLUDE(c) WHERE renamed_a>0",0))
		var fresh int64
		if err := a.QueryRowContext(p.Ctx,"SELECT 'pgi_renamed'::regclass::oid::bigint").Scan(&fresh); err != nil || fresh <= 0 || fresh == oid { t.Fatal("recreated identity",oid,fresh,err) }
		checkOID(byOID); byOID.Args,byOID.Rows = []any{fresh},[][]any{row}; checkOID(byOID)
		pgIndexBoth(run,pgIndexLogical("pgi_renamed",[][]any{row}))
	})
}

func TestReferencePGIndexPartitions(t *testing.T) {
	for _,recursive := range []bool{false,true} {
		name := "attach"; if recursive { name = "recursive" }
		t.Run(name,func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				a,peer := fixture.LockConnections(t,p.Ctx,b.DB)
				run,other := transactionalIndexRun(t,p,b,a),transactionalIndexRun(t,p,b,peer)
				for _,q := range []string{"CREATE TABLE pgi_parent(n INT) PARTITION BY RANGE(n)","CREATE TABLE pgi_leaf PARTITION OF pgi_parent FOR VALUES FROM(0) TO(10)"} { run(fixture.Exec(q,0)) }
				// Keep both sessions' parameterized metadata statements through every
				// transition; fresh preparation alone cannot prove execution recapture.
				hold := func(conn *sql.Conn) fixture.HeldStatement {
					q := pgIndexLogical("pgi_parent_ix",nil).Query
					stmt,err := conn.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }
					return fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:q}
				}
				ownerHeld,peerHeld := hold(a),hold(peer)
				defer ownerHeld.Statement.Close(); defer peerHeld.Statement.Close()
				check := func(held fixture.HeldStatement,name string,rows [][]any) {
					s := pgIndexLogical(name,rows)
					pgIndexBoth(transactionalIndexRun(t,p,b,held.Executor),s)
					fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				}
				check(ownerHeld,"pgi_parent_ix",nil); check(peerHeld,"pgi_parent_ix",nil)
				parent := pgIndexOrdinary(1,1,"1","0","1978","0",nil,nil)
				parent[4] = true
				if recursive { run(fixture.Exec("CREATE UNIQUE INDEX pgi_parent_ix ON pgi_parent(n)",0)) } else {
					run(fixture.Exec("CREATE UNIQUE INDEX pgi_parent_ix ON ONLY pgi_parent(n)",0)); parent[10] = false
					check(ownerHeld,"pgi_parent_ix",[][]any{parent}); check(peerHeld,"pgi_parent_ix",[][]any{parent})
					run(fixture.Exec("CREATE UNIQUE INDEX pgi_leaf_ix ON pgi_leaf(n)",0)); run(fixture.Prepared(fixture.Exec("ALTER INDEX pgi_parent_ix ATTACH PARTITION pgi_leaf_ix",0))); parent[10] = true
				}
				check(ownerHeld,"pgi_parent_ix",[][]any{parent}); check(peerHeld,"pgi_parent_ix",[][]any{parent})
				var parentOID,childOID int64; var child string
				if err := a.QueryRowContext(p.Ctx,`SELECT p.oid::bigint,c.oid::bigint,c.relname FROM pg_class p JOIN pg_inherits h ON h.inhparent=p.oid JOIN pg_class c ON c.oid=h.inhrelid WHERE p.oid='pgi_parent_ix'::regclass`).Scan(&parentOID,&childOID,&child); err != nil || parentOID <= 0 || childOID <= 0 || parentOID == childOID { t.Fatal("index graph",parentOID,childOID,err) }
				leaf := pgIndexOrdinary(1,1,"1","0","1978","0",nil,nil)
				leaf[4] = true
				check(ownerHeld,child,[][]any{leaf}); check(peerHeld,child,[][]any{leaf})
				edges := fixture.Select(`SELECT p.relkind,c.relkind,pi.indrelid='pgi_parent'::regclass AS parent,ci.indrelid='pgi_leaf'::regclass AS leaf,h.inhseqno,pi.indisvalid,ci.indisvalid FROM pg_inherits h JOIN pg_class p ON p.oid=h.inhparent JOIN pg_class c ON c.oid=h.inhrelid JOIN pg_index pi ON pi.indexrelid=p.oid JOIN pg_index ci ON ci.indexrelid=c.oid WHERE p.oid=$1::oid AND c.oid=$2::oid`,[]string{"relkind","relkind","parent","leaf","inhseqno","indisvalid","indisvalid"},[]string{"CHAR","CHAR","BOOL","BOOL","INT4","BOOL","BOOL"},[][]any{{"I","i",true,true,int64(1),true,true}},parentOID,childOID)
				pgIndexBoth(run,edges)
				inventory := fixture.Select("SELECT indexrelid::bigint FROM pg_index WHERE indexrelid=$1::oid OR indexrelid=$2::oid ORDER BY indexrelid",[]string{"indexrelid"},[]string{"INT8"},[][]any{{parentOID},{childOID}},parentOID,childOID); inventory.Bag = true
				pgIndexBoth(run,inventory)
				for _,end := range []string{"ROLLBACK","COMMIT"} {
					run(fixture.Exec("BEGIN",0)); run(fixture.Exec("DROP INDEX pgi_parent_ix",0))
					empty := inventory; empty.Rows = nil; pgIndexBoth(run,empty); pgIndexBoth(other,inventory)
					check(ownerHeld,"pgi_parent_ix",nil); check(ownerHeld,child,nil)
					check(peerHeld,"pgi_parent_ix",[][]any{parent}); check(peerHeld,child,[][]any{leaf})
					pgIndexBoth(other,edges)
					run(fixture.Exec(end,0))
					if end == "ROLLBACK" {
						pgIndexBoth(run,edges); pgIndexBoth(other,edges)
						pgIndexBoth(run,inventory); pgIndexBoth(other,inventory)
						check(ownerHeld,"pgi_parent_ix",[][]any{parent}); check(ownerHeld,child,[][]any{leaf})
						check(peerHeld,"pgi_parent_ix",[][]any{parent}); check(peerHeld,child,[][]any{leaf})
					} else {
						pgIndexBoth(run,empty); pgIndexBoth(other,empty)
						noEdges := edges; noEdges.Rows = nil; pgIndexBoth(run,noEdges); pgIndexBoth(other,noEdges)
						check(ownerHeld,"pgi_parent_ix",nil); check(ownerHeld,child,nil)
						check(peerHeld,"pgi_parent_ix",nil); check(peerHeld,child,nil)
					}
				}
			})
		})
	}
}
