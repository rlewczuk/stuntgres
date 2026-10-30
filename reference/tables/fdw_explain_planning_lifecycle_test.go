package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWExplainPlanningLifecycle(t *testing.T) {
	t.Run("contexts", fdwExplainContextLifecycle)
	t.Run("composite", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
			r.advanced = &fdwAdvancedState{}
			fdwPushdownLive(t,p,b,r,run)
			sentinel := fdwExplainPlanningSentinel(run)
			unchanged := func() {
				sentinel()
				fdwWriteSourceRows(t,p,b,r,fixture.Select(`SELECT "Key","Value" FROM fdw_source."Left input" ORDER BY "Key" NULLS LAST,"Value" NULLS LAST`,[]string{"Key","Value"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(10)},{int64(1),nil},{int64(2),int64(20)},{int64(4),int64(40)},{nil,int64(30)}}))
				fdwWriteSourceRows(t,p,b,r,fixture.Select(`SELECT "Key","Weight" FROM fdw_source."Right input" ORDER BY "Key" NULLS LAST`,[]string{"Key","Weight"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(100)},{int64(1),int64(100)},{int64(3),int64(300)},{nil,int64(900)}}))
			}
			queries := []string{
				"EXPLAIN (VERBOSE,COSTS false) SELECT l.k,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k",
				"EXPLAIN (VERBOSE,COSTS false) SELECT k,count(*) FROM fdw_push_left GROUP BY k",
			}
			for i, query := range queries {
				old := fdwExplainText(t,p,c,query)
				unchanged()
				saved := strings.Join(old,"\n")
				if !b.PG {
					phase := []string{"GetForeignJoinPaths","GetForeignUpperPaths"}[i]
					for _, hook := range []string{phase,"ExplainForeignScan"} {
						for _, action := range []string{"provider","cancel","mapping","role","grant","restore"} {
							fdwGatedPlanning(t,p,b,c,r,query,hook,action,false,unchanged)
							fdwGatedPlanning(t,p,b,c,r,query,hook,action,true,unchanged)
						}
					}
				}
				if strings.Join(old,"\n") != saved { t.Fatal("retired TEXT backing changed") }
				fdwPushdownValue(run,"SELECT count(*) FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k",[]string{"count"},[]string{"INT8"},[][]any{{int64(4)}},false)
			}
		})
	})
	t.Run("rowmark", func(t *testing.T) {
		fdwRowLockConfiguredScenario(t,false,func(w *fdwSQLRowLocker) { w.advanced = &fdwAdvancedState{} },func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *sql.DB,w *fdwSQLRowLocker,run func(fixture.Step)) {
			sentinel := fdwExplainPlanningSentinel(run)
			unchanged := func() {
				sentinel()
				fdwWriteSourceRows(t,p,b,w.fdwSQLReader,fixture.Select("SELECT rid,generation,revision,id,label,flag FROM fdw_source.data ORDER BY rid",[]string{"rid","generation","revision","id","label","flag"},[]string{"INT4","INT4","INT4","INT4","TEXT","BOOL"},[][]any{{int64(1),int64(101),int64(1),int64(1),"one",true},{int64(2),int64(102),int64(1),int64(2),"two",false}}))
			}
			query := "EXPLAIN (VERBOSE,COSTS false) SELECT id FROM fdw_data ORDER BY id FOR UPDATE"
			old := fdwExplainText(t,p,c,query)
			unchanged()
			saved := strings.Join(old,"\n")
			if !b.PG {
				for _, phase := range []string{"GetForeignRowMarkType","ExplainForeignScan"} {
					for _, action := range []string{"provider","cancel","mapping","role","grant","restore"} {
						fdwGatedPlanning(t,p,b,c,w.fdwSQLReader,query,phase,action,false,unchanged)
						fdwGatedPlanning(t,p,b,c,w.fdwSQLReader,query,phase,action,true,unchanged)
					}
				}
			}
			if strings.Join(old,"\n") != saved { t.Fatal("retired marked TEXT backing changed") }
			fdwWriteSourceRows(t,p,b,w.fdwSQLReader,fixture.Select("SELECT rid,generation,revision FROM fdw_source.data ORDER BY rid",[]string{"rid","generation","revision"},[]string{"INT4","INT4","INT4"},[][]any{{int64(1),int64(101),int64(1)},{int64(2),int64(102),int64(1)}}))
			run(fixture.Select("SELECT id FROM fdw_data ORDER BY id FOR UPDATE",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		})
	})
}

// Native provider gates are explicit phases within the shared real-fixture body.
// Peer SQL proves detached planning; public absence of Rows proves atomic rejection.
func fdwGatedPlanning(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fdwSQLReader,query,phase,action string,prepared bool,unchanged func()) {
	t.Helper()
	var dump bytes.Buffer
	if action == "restore" { if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) } }
	var executor fixture.Executor = c
	var statement *sql.Stmt
	if prepared {
		before := r.snapshot()
		var err error
		statement,err = c.PrepareContext(p.Ctx,query)
		if err != nil { t.Fatal(err) }
		defer statement.Close()
		if r.snapshot() != before { t.Fatal("Prepare invoked provider planning") }
		executor = fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
	}
	before := r.snapshot()
	ctx,cancel := context.WithCancel(p.Ctx)
	defer cancel()
	entered,release := make(chan struct{}),make(chan struct{})
	var enterOnce,releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func(){ close(release) }) }
	failure := errors.New("selected foreign planning failed")
	r.setHook(func(ctx context.Context,name string) error {
		if name != phase { return ctx.Err() }
		enterOnce.Do(func(){ close(entered) })
		select { case <-release: if action == "provider" { return failure }; return ctx.Err(); case <-ctx.Done(): return ctx.Err() }
	})
	done := make(chan error,1)
	go func(){
		rows,err := executor.QueryContext(ctx,query)
		if rows != nil { rows.Close(); if err == nil { err = errors.New("stale selected planning published TEXT") } }
		done <- err
	}()
	joined := false
	defer func(){ cancel(); unblock(); if !joined { <-done }; r.setHook(nil) }()
	select { case <-entered: case <-p.Ctx.Done(): t.Fatal("selected planning callback not reached",phase) }
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT 42 AS peer",[]string{"peer"},[]string{"INT4"},[][]any{{int64(42)}}))
	switch action {
	case "cancel": cancel()
	case "provider": unblock()
	case "restore": if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }; unblock()
	case "mapping": fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(ADD user 'postgres')",0)); unblock()
	case "role": fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE ROLE fdw_planning_late",0)); unblock()
	case "grant":
		table := "fdw_data"
		if strings.Contains(query,"fdw_push_left") { table = "fdw_push_left" }
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("GRANT SELECT ON "+table+" TO PUBLIC",0)); unblock()
	}
	var err error
	select { case err = <-done: joined = true; case <-p.Ctx.Done(): t.Fatal("selected planning worker not joined",phase) }
	if action == "cancel" { if !errors.Is(err,context.Canceled) { t.Fatal(err) } } else if action == "provider" { if !errors.Is(err,failure) { t.Fatal(err) } } else { fixture.TransactionOrdinary(t,err,"stuntgres: catalog replaced before publication") }
	r.setHook(nil)
	if action == "mapping" { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(DROP user)",0)) }
	if action == "role" { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("DROP ROLE fdw_planning_late",0)) }
	if action == "grant" {
		table := "fdw_data"
		if strings.Contains(query,"fdw_push_left") { table = "fdw_push_left" }
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("REVOKE SELECT ON "+table+" FROM PUBLIC",0))
	}
	after := r.snapshot()
	if after.begins != before.begins || after.iterations != before.iterations || after.openPools != 0 || after.openRows != 0 { t.Fatal("plain EXPLAIN entered execution or leaked source",before,after) }
	unchanged()
}
