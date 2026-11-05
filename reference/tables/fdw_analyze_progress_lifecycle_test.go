package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeProgressLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			t.Run("direct", func(t *testing.T) { fdwAnalyzeProgressDirectFailures(t, prepared) })
			t.Run("domain", func(t *testing.T) { fdwAnalyzeProgressDomainFailure(t, prepared) })
			t.Run("inherited", func(t *testing.T) { fdwAnalyzeProgressInheritedFailure(t, prepared) })
		})
	}
}

func fdwAnalyzeProgressDirectFailures(t *testing.T, prepared bool) {
	t.Helper()
	fdwAnalyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwAnalyzeDirectSetup(t, p, b, r, run)
		fdwAnalyzeProgressDirectRun(t, p, b, c, r, prepared, "")
		fdwAnalyzeProgressDirectProducts(run)
		id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_direct")
		joint := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
		expression := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression")
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET b=b+10 WHERE tag='rare'", 300)
		retained := func() {
			t.Helper()
			fdwAnalyzeProgressAbsent(t, p, b, id)
			fdwAnalyzeRelationPopulation(run, "fdw_direct", 1000)
			if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint || fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
				t.Fatal("unfinished direct command published typed completion")
			}
		}
		// Genuine mapping lookup fails at acquisition, on both backends.
		var owner string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
		failure := fixture.ErrorDiagnostic("ANALYZE fdw_direct", fdwDiagnostic("42704", `user mapping not found for user "`+owner+`", server "fdw_live"`))
		failure.Prepared, failure.ErrorStage = prepared, "execute"
		run(failure)
		retained()
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
		if b.PG {
			return
		}
		// Capability is not admission. Even an actual unfinished negotiation
		// must not manufacture a progress row or completed products.
		gate := fdwAnalyzeProgressHold(t, p, b, c, r, "ANALYZE fdw_direct", "AnalyzeForeignTable", prepared, nil)
		fdwAnalyzeProgressAbsent(t, p, b, id)
		gate.cancel()
		fdwAnalyzeProgressCanceled(t, b, gate.join())
		gate.release()
		retained()
		// Native owner mappings normally have no options; return to that exact
		// state so the controlled ADD/DROP below changes real authority.
		run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(DROP user)", 0))
		for _, action := range []string{"cancel", "provider", "mapping"} {
			providerError := errors.New("analyze progress acquired source failure")
			var injected error
			if action == "provider" {
				injected = providerError
			}
			gate := fdwAnalyzeProgressHold(t, p, b, c, r, "ANALYZE fdw_direct", "AcquireSampleRows", prepared, injected)
			fdwAnalyzeProgressObserve(t, p, b, id, "acquiring sample rows", 0, 0, 0, 0, 0, 0, 0, gate.finished)
			if action == "cancel" {
				gate.cancel()
			} else if action == "mapping" {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(ADD user 'postgres')", 0))
			}
			if action != "cancel" {
				gate.release()
			}
			out := gate.join()
			gate.release()
			if action == "cancel" {
				fdwAnalyzeProgressCanceled(t, b, out)
			} else {
				if out.Result != nil || out.Rows != nil {
					t.Fatal("failed acquisition returned completion", action, out)
				}
				if action == "provider" {
					if !errors.Is(out.Err, providerError) {
						t.Fatal("provider failure provenance", out.Err)
					}
				} else {
					fixture.TransactionOrdinary(t, out.Err, "stuntgres: catalog replaced before publication")
				}
			}
			if action == "mapping" {
				run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(DROP user)", 0))
			}
			retained()
			fdwAnalyzeProgressResources(t, r)
		}
		// Corrupt one genuine source transport cell, not a canned sample.
		t.Cleanup(func() { r.mu.Lock(); r.rowTransform = nil; r.mu.Unlock() })
		r.mu.Lock()
		r.rowTransform = func(_ context.Context, row stuntgres.ForeignRow) (stuntgres.ForeignRow, error) {
			row[0] = "not an integer"
			return row, nil
		}
		r.mu.Unlock()
		bad := fixture.ErrorDiagnostic("ANALYZE fdw_direct", fdwDiagnostic("22P02", `invalid input syntax for type integer: "not an integer"`))
		bad.Prepared, bad.ErrorStage = prepared, "execute"
		run(bad)
		r.mu.Lock()
		r.rowTransform = nil
		r.mu.Unlock()
		retained()
		fdwAnalyzeProgressResources(t, r)
		// A MAINTAIN actor loses authority while real acquisition is detached.
		run(fixture.Exec("CREATE ROLE fdw_other", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_other", 0))
		run(fixture.Exec("GRANT MAINTAIN ON fdw_direct TO fdw_other", 0))
		run(fixture.Exec("SET ROLE fdw_other", 0))
		gate = fdwAnalyzeProgressHold(t, p, b, c, r, "ANALYZE fdw_direct", "AcquireSampleRowsResult", prepared, nil)
		fdwAnalyzeProgressObserve(t, p, b, id, "acquiring sample rows", 0, 0, 0, 0, 0, 0, 0, gate.finished)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("REVOKE MAINTAIN ON fdw_direct FROM fdw_other", 0))
		gate.release()
		out := gate.join()
		if out.Result != nil || out.Rows != nil {
			t.Fatal("authority rejection published completion", out)
		}
		fixture.TransactionOrdinary(t, out.Err, "stuntgres: catalog replaced before publication")
		run(fixture.Exec("RESET ROLE", 0))
		retained()
		// A real replacement does not carry old callback authority or unfinished
		// typed products into the restored catalog. Source SQL remains independent.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		gate = fdwAnalyzeProgressHold(t, p, b, c, r, "ANALYZE fdw_direct", "AcquireSampleRowsResult", prepared, nil)
		fdwAnalyzeProgressObserve(t, p, b, id, "acquiring sample rows", 0, 0, 0, 0, 0, 0, 0, gate.finished)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		gate.release()
		out = gate.join()
		if out.Result != nil || out.Rows != nil {
			t.Fatal("replacement accepted old command completion", out)
		}
		fixture.TransactionOrdinary(t, out.Err, "stuntgres: catalog replaced before publication")
		fdwAnalyzeProgressAbsent(t, p, b, id)
		// Native Dump/Restore transports prior immutable typed summaries, not
		// maintenance or the unfinished acquired sample. The saved joint3 must
		// remain stale despite the actual source's new joint6 until fresh ANALYZE.
		if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint || fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
			t.Fatal("replacement lost prior typed summaries or accepted unfinished work")
		}
		fdwAnalyzeRelationPopulation(run, "fdw_direct", -1)
		fdwAnalyzeProgressResources(t, r)
		fdwAnalyzeProgressDirectRun(t, p, b, c, r, prepared, "")
		fdwAnalyzeDirectJoint(run, 6)
	})
}

func fdwAnalyzeProgressInheritedFailure(t *testing.T, prepared bool) {
	t.Helper()
	fdwAnalyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwAnalyzeMixedSetup(t, p, b, r, run, "ordinary", 10, 30)
		run(fixture.Exec("CREATE STATISTICS fdw_analyze_joint(ndistinct,dependencies,mcv) ON id,flag FROM fdw_analyze_parent", 0))
		run(fixture.Exec("CREATE STATISTICS fdw_analyze_expression ON (id+1) FROM fdw_analyze_parent", 0))
		run(fixture.Exec("CREATE ROLE fdw_owner", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_owner", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_analyze_foreign OWNER TO fdw_owner", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_owner SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
		run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
		joint := fdwAnalyzeJointPayloads(t, p, c)
		expression := fdwAnalyzeDomainExpressions(t, p, c)
		before := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
		run(fixture.Exec("INSERT INTO fdw_analyze_parent VALUES"+fdwAnalyzeValues(20, 3, "new", false), 20))
		run(fixture.Exec("DROP USER MAPPING FOR fdw_owner SERVER fdw_live", 0))
		id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_analyze_parent")
		failure := fixture.ErrorDiagnostic("ANALYZE ONLY fdw_analyze_parent", fdwDiagnostic("42704", `user mapping not found for user "fdw_owner", server "fdw_live"`))
		failure.Prepared, failure.ErrorStage = prepared, "execute"
		run(failure)
		fdwAnalyzeProgressAbsent(t, p, b, id)
		fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 30)
		after := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+1)
		if !after.at.After(before.at) {
			t.Fatal("inherited failure retracted completed local maintenance", before, after)
		}
		if !reflect.DeepEqual(fdwAnalyzeJointPayloads(t, p, c), joint) || !reflect.DeepEqual(fdwAnalyzeDomainExpressions(t, p, c), expression) {
			t.Fatal("inherited failure published unfinished local or inherited typed products")
		}
		fdwAnalyzeBaselineFacts(t, p, b, c, run)
		fdwAnalyzeLocalBaselineFacts(t, p, b, c, run, "ordinary", 30)
		if !b.PG {
			fdwAnalyzeProgressResources(t, r)
		}
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_owner SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
		step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
		step.Prepared = prepared
		run(step)
		fdwAnalyzeProgressAbsent(t, p, b, id)
		fdwAnalyzeDomainJointGroups(run, 2, 3)
	})
}
