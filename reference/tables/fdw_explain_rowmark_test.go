package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWExplainRowMarkPlans(t *testing.T) { fdwExplainRowMarkScenario(t) }

func fdwExplainRowMarkScenario(t *testing.T) {
	for _, sibling := range []stuntgres.ForeignRowMarkType{stuntgres.ForeignRowMarkCopy, stuntgres.ForeignRowMarkReference} {
		t.Run(map[stuntgres.ForeignRowMarkType]string{stuntgres.ForeignRowMarkCopy: "copy", stuntgres.ForeignRowMarkReference: "reference"}[sibling], func(t *testing.T) {
			fdwRowLockConfiguredScenario(t, false, func(w *fdwSQLRowLocker) { w.sibling = sibling; w.advanced = &fdwAdvancedState{} }, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, w *fdwSQLRowLocker, run func(fixture.Step)) {
				sentinel := fdwExplainPlanningSentinel(run)
				unchanged := func() {
					sentinel()
					fdwWriteSourceRows(t, p, b, w.fdwSQLReader, fixture.Select("SELECT rid,generation,revision,id,label,flag FROM fdw_source.data ORDER BY rid", []string{"rid", "generation", "revision", "id", "label", "flag"}, []string{"INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL"}, [][]any{{int64(1), int64(101), int64(1), int64(1), "one", true}, {int64(2), int64(102), int64(1), int64(2), "two", false}}))
				}
				for _, suffix := range []string{"FOR UPDATE", "FOR SHARE", "FOR UPDATE NOWAIT", "FOR SHARE SKIP LOCKED"} {
					transactions := w.transactions
					w.markRequestHook = func(request stuntgres.ForeignRowMarkRequest) {
						strength, wait := stuntgres.ForeignLockUpdate, stuntgres.ForeignLockBlock
						if strings.Contains(suffix, "SHARE") { strength = stuntgres.ForeignLockShare }
						if strings.Contains(suffix, "NOWAIT") { wait = stuntgres.ForeignLockNoWait }
						if strings.Contains(suffix, "SKIP LOCKED") { wait = stuntgres.ForeignLockSkipLocked }
						if request.Strength != strength || request.WaitPolicy != wait || request.Transaction != nil || request.Characteristics != nil { t.Error("effective mark planning differs", request) }
					}
					w.setHook(func(ctx context.Context, method string) error {
						if method == "BeginForeignScan" || method == "BeginForeignQueryScan" || strings.Contains(method, "Refetch") || strings.Contains(method, "Recheck") || strings.Contains(method, "BeginForeignTransaction") { t.Errorf("EXPLAIN entered runtime %s", method) }
						return ctx.Err()
					})
					query := "SELECT id FROM fdw_data ORDER BY id "+suffix
					fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) "+query, func(lines []string) {
						text := strings.Join(lines, "\n")
						if !strings.Contains(text, "Remote SQL:") { t.Fatal(text) }
						if !b.PG {
							w.advanced.mu.Lock(); request := w.advanced.scanRequests[len(w.advanced.scanRequests)-1]; w.advanced.mu.Unlock()
							want := stuntgres.ForeignRowMarkUpdate
							if strings.Contains(suffix, "SHARE") { want = stuntgres.ForeignRowMarkShare }
							fdwAssertMarkedRequest(t, request, want)
							if !strings.Contains(text, "Row identity: rid, generation, revision") { t.Fatal("identity not explained", text) }
						}
						if !b.PG && w.transactions != transactions { t.Fatal("plain EXPLAIN enlisted a transaction") }
						unchanged()
					})
					w.setHook(nil)
					step := fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
					run(step); run(fixture.Prepared(step)); fdwPushdownHeld(t, p, b, c, query, step, step)
					unchanged()
				}
				w.markRequestHook = nil
				query := "SELECT a.id,b.id FROM fdw_data a JOIN fdw_data b ON a.id=b.id ORDER BY a.id FOR UPDATE OF a"
				fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) "+query, func(lines []string) {
					if !b.PG {
						w.advanced.mu.Lock(); requests := append([]stuntgres.ForeignRequest{}, w.advanced.scanRequests...); w.advanced.mu.Unlock()
						fdwAssertMarkedRequest(t, requests[len(requests)-2], stuntgres.ForeignRowMarkUpdate)
						fdwAssertMarkedRequest(t, requests[len(requests)-1], sibling)
					}
					unchanged()
				})
				step := fixture.Select(query, []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
				run(step); run(fixture.Prepared(step)); fdwPushdownHeld(t, p, b, c, query, step, step)
				// A source tuple lock must not impede metadata planning, and EXPLAIN
				// must not retain locks after the independent holder releases it.
				holder, err := source.BeginTx(p.Ctx, nil)
				if err != nil { t.Fatal(err) }
				defer holder.Rollback()
				rows, err := holder.QueryContext(p.Ctx, "SELECT rid FROM fdw_source.data WHERE rid=1 FOR UPDATE")
				if err != nil { t.Fatal(err) }
				if !rows.Next() { t.Fatal("source holder did not capture its row", rows.Err()) }
				var rid int64
				if err := rows.Scan(&rid); err != nil || rid != 1 { t.Fatal("source holder identity",rid,err) }
				rows.Close()
				fdwExplainText(t, p, c, "EXPLAIN (VERBOSE,COSTS false) SELECT id FROM fdw_data FOR UPDATE")
				if err := holder.Rollback(); err != nil { t.Fatal(err) }
				witness, err := source.BeginTx(p.Ctx, nil)
				if err != nil { t.Fatal(err) }; defer witness.Rollback()
				rows, err = witness.QueryContext(p.Ctx, "SELECT rid FROM fdw_source.data FOR UPDATE NOWAIT")
				if err != nil { t.Fatal("plain EXPLAIN leaked source lock", err) }
				for rows.Next() { if err := rows.Scan(&rid); err != nil { t.Fatal(err) } }
				if err := rows.Err(); err != nil { t.Fatal("plain EXPLAIN leaked source lock",err) }
				rows.Close()
				if err := witness.Rollback(); err != nil { t.Fatal(err) }
				unchanged()
			})
		})
	}
}

func fdwAssertMarkedRequest(t *testing.T, request stuntgres.ForeignRequest, want stuntgres.ForeignRowMarkType) {
	t.Helper()
	if request.Transaction != nil || request.Characteristics != nil || request.RowMark == nil || request.RowMark.Type != want || !reflect.DeepEqual(request.RequiredAttributes, []int32{1,2,3}) { t.Fatal("not the effective complete-tuple planning request", request) }
	if want == stuntgres.ForeignRowMarkCopy {
		if request.Identity != nil { t.Fatal("COPY has identity", request) }
	} else if request.Identity == nil || !reflect.DeepEqual(request.Identity.Names, []string{"rid", "generation", "revision"}) { t.Fatal("missing genuine identity", request) }
}

func TestReferenceFDWExplainIntegratedFeatureSmoke(t *testing.T) {
	t.Run("composite", fdwExplainCompositeScenario)
	t.Run("rowmark", fdwExplainRowMarkScenario)
}
