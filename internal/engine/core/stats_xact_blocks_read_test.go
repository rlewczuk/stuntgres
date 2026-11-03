package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func blocksUnitScope(t *testing.T, e *core.Engine, scope *core.StatementScope, ref ids.ObjectRef, want int64) {
	t.Helper()
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	for _, kind := range []functions.StatsXactBlocksKind{functions.XactBlocksFetched, functions.XactBlocksHit} {
		value, err := ctx.StatsXactBlocksValue(kind, uint32(ref.Address.ObjectID), enginetest.Usage(e, sqltype.Int8).Type)
		if err != nil {
			t.Fatal(err)
		}
		n, err := value.Integer()
		if err != nil || n != want {
			t.Fatal("live pending request", kind, n, want, err)
		}
	}
}
func blocksUnitPending(t *testing.T, e *core.Engine, s *session.Session, ref ids.ObjectRef, want int64) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	blocksUnitScope(t, e, scope, ref, want)
}
func blocksUnitPublished(t *testing.T, e *core.Engine, ref ids.ObjectRef, want int64) {
	t.Helper()
	fact := e.Statistics.Read(statistics.Key{Object: ref, Category: statistics.Relation})
	if fact.BlocksFetched != want || fact.BlocksHit != want {
		t.Fatal("published requests", fact, want)
	}
}
func blocksUnitOpen(t *testing.T, capture *bufferCapture) (*exec.State, exec.Stage) {
	t.Helper()
	if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
		t.Fatal(err)
	}
	exec.AdmitScan(capture.root, capture.source)
	state := &exec.State{Source: capture.source, SourceMode: exec.SourceBorrowed, HashFilter: exec.HashQueryFilter(capture.root), Retain: true}
	stage, err := exec.OpenNode(capture.op.Ctx, state, capture.root)
	if err != nil {
		t.Fatal(err)
	}
	return state, stage
}

func TestStatsXactBlocksReadAccounting(t *testing.T) {
	t.Run("invocations_rewind_cache_and_authority", func(t *testing.T) {
		e := bufferUnitEngine(t)
		enginetest.Run(t, e, "CREATE TABLE block_read(k integer)")
		enginetest.Run(t, e, "INSERT INTO block_read VALUES(1),(2)")
		s, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "BEGIN")
		ref := enginetest.RoleTableRef(t, e, "block_read")
		ast, description := enginetest.PreparedDescription(t, e, s, "SELECT k FROM block_read")
		_ = ast
		defer e.RetirePreparedDescription(&description)
		blocksUnitPending(t, e, s, ref, 0)
		statsFunctionExec(t, s, "EXPLAIN SELECT k FROM block_read")
		blocksUnitPending(t, e, s, ref, 0)
		capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_read")
		blocksUnitScope(t, e, capture.scope, ref, 0)
		node := bufferUnitNode(t, e, "block_read")
		bufferUnitFact(t, e, observer, node, 0, 0)
		state, stage := blocksUnitOpen(t, capture)
		defer stage.Close()
		blocksUnitScope(t, e, capture.scope, ref, 1)
		bufferUnitFact(t, e, observer, node, 1, 1)
		if _, err := stage.Rewind(capture.op.Ctx); err != nil {
			t.Fatal(err)
		}
		blocksUnitScope(t, e, capture.scope, ref, 1)
		again, err := exec.OpenNode(capture.op.Ctx, state, capture.root)
		if err != nil {
			t.Fatal(err)
		}
		again.Close()
		blocksUnitScope(t, e, capture.scope, ref, 2)
		bufferUnitFact(t, e, observer, node, 2, 1)
		blocksUnitPublished(t, e, ref, 0)
		lease := capture.source.ReadLease
		stage.Close()
		capture.close(t)
		bufferUnitFact(t, e, observer, node, 2, 0)
		// The detached capture retains no live scope or writer authority.
		lease.Open()
		lease.Close()
		blocksUnitPending(t, e, s, ref, 2)
		blocksUnitPending(t, e, observer, ref, 0)
	})
	for _, kind := range []string{"private", "temp", "rewritten"} {
		t.Run(kind+"_independent_cache_eligibility", func(t *testing.T) {
			e := bufferUnitEngine(t)
			s, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			if kind == "rewritten" {
				statsFunctionExec(t, s, "CREATE TABLE block_private(k integer)", "INSERT INTO block_private VALUES(9)")
			}
			statsFunctionExec(t, s, "BEGIN")
			if kind == "temp" {
				statsFunctionExec(t, s, "CREATE TEMP TABLE block_private(k integer)")
			} else if kind == "private" {
				statsFunctionExec(t, s, "CREATE TABLE block_private(k integer)")
			} else {
				statsFunctionExec(t, s, "TRUNCATE block_private")
			}
			statsFunctionExec(t, s, "INSERT INTO block_private VALUES(1)")
			capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_private")
			ref := capture.source.Ref
			read := capture.source.View.Data().Catalog
			node := read.MustRelation(ref.Address).EffectiveStorageLocator(read)
			if capture.source.ReadLease == nil {
				t.Fatal("private source lost accounting")
			}
			_, stage := blocksUnitOpen(t, capture)
			if err := stage.Drain(capture.op.Ctx); err != nil {
				t.Fatal(err)
			}
			blocksUnitScope(t, e, capture.scope, ref, 1)
			bufferUnitFact(t, e, observer, node, 0, 0)
			stage.Close()
			capture.close(t)
			statsFunctionExec(t, s, "ROLLBACK")
		})
	}
	t.Run("leaf_reachability_and_pruning", func(t *testing.T) {
		e := engine.New()
		for _, q := range []string{"CREATE TABLE block_parent(k integer) PARTITION BY RANGE(k)", "CREATE TABLE block_low PARTITION OF block_parent FOR VALUES FROM(0) TO(2)", "CREATE TABLE block_high PARTITION OF block_parent FOR VALUES FROM(2) TO(4)", "INSERT INTO block_parent VALUES(1),(3)"} {
			enginetest.Run(t, e, q)
		}
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "BEGIN")
		capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_parent WHERE k=1")
		if capture.source.BranchPruner == nil || len(capture.source.Inheritance) != 2 {
			t.Fatal("missing real partition capture")
		}
		_, stage := blocksUnitOpen(t, capture)
		if err := stage.Drain(capture.op.Ctx); err != nil {
			t.Fatal(err)
		}
		blocksUnitScope(t, e, capture.scope, enginetest.RoleTableRef(t, e, "block_low"), 1)
		blocksUnitScope(t, e, capture.scope, enginetest.RoleTableRef(t, e, "block_high"), 0)
		blocksUnitScope(t, e, capture.scope, enginetest.RoleTableRef(t, e, "block_parent"), 0)
		stage.Close()
		capture.close(t)
	})
	t.Run("empty_pruned_and_cancel", func(t *testing.T) {
		e := engine.New()
		for _, q := range []string{"CREATE TABLE block_empty(k integer)", "CREATE TABLE block_nonempty(k integer)", "INSERT INTO block_nonempty VALUES(1)"} {
			enginetest.Run(t, e, q)
		}
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "BEGIN")
		for _, query := range []string{"SELECT k FROM block_empty", "SELECT k FROM block_nonempty WHERE false"} {
			capture := bufferUnitCapture(t, e, s, query)
			_, stage := blocksUnitOpen(t, capture)
			stage.Drain(capture.op.Ctx)
			blocksUnitScope(t, e, capture.scope, capture.source.Ref, 0)
			stage.Close()
			capture.close(t)
		}
		capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_nonempty")
		ctx, cancel := context.WithCancel(capture.op.Ctx)
		cancel()
		state := &exec.State{Source: capture.source, SourceMode: exec.SourceBorrowed, Retain: true}
		stage, err := exec.OpenNode(ctx, state, capture.root)
		stage.Close()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("pre-open cancellation", err)
		}
		blocksUnitScope(t, e, capture.scope, capture.source.Ref, 0)
		_, stage = blocksUnitOpen(t, capture)
		if err := stage.Drain(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal("post-open cancellation", err)
		}
		blocksUnitScope(t, e, capture.scope, capture.source.Ref, 1)
		stage.Close()
		capture.close(t)
	})
	t.Run("sample_error_before_storage_open", func(t *testing.T) {
		e := engine.New()
		for _, query := range []string{"CREATE EXTENSION tsm_system_time", "CREATE TABLE block_sample(k integer)", "INSERT INTO block_sample VALUES(1)"} {
			enginetest.Run(t, e, query)
		}
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "BEGIN")
		capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_sample TABLESAMPLE system_time(NULL)")
		if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
			t.Fatal(err)
		}
		exec.AdmitScan(capture.root, capture.source)
		state := &exec.State{Source: capture.source, SourceMode: exec.SourceBorrowed, Retain: true}
		stage, err := exec.OpenNode(capture.op.Ctx, state, capture.root.(*plan.ProjectNode).Child)
		stage.Close()
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "2202H", Message: "TABLESAMPLE parameter cannot be null"})
		blocksUnitScope(t, e, capture.scope, capture.source.Ref, 0)
		capture.close(t)
	})
	for _, method := range []string{"hash", "bloom"} {
		t.Run("chosen_"+method, func(t *testing.T) {
			e := engine.New()
			if method == "bloom" {
				enginetest.Run(t, e, "CREATE EXTENSION bloom")
			}
			for _, q := range []string{"CREATE TABLE block_index(k integer)", "INSERT INTO block_index VALUES(1),(2)", "CREATE INDEX block_chosen ON block_index USING " + method + "(k)", "CREATE INDEX block_unused ON block_index USING hash((k+1))"} {
				enginetest.Run(t, e, q)
			}
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "SET enable_seqscan=off")
			capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_index WHERE k=1")
			_, stage := blocksUnitOpen(t, capture)
			if capture.source.ScanDecision.Method == plan.FullScan {
				t.Fatal("index fixture selected full scan")
			}
			if err := stage.Drain(capture.op.Ctx); err != nil {
				t.Fatal(err)
			}
			blocksUnitScope(t, e, capture.scope, enginetest.RoleTableRef(t, e, "block_chosen"), 1)
			blocksUnitScope(t, e, capture.scope, enginetest.RoleTableRef(t, e, "block_unused"), 0)
			stage.Close()
			capture.close(t)
		})
	}
	t.Run("denied_RLS_source", func(t *testing.T) {
		e := engine.New()
		for _, q := range []string{"CREATE ROLE block_rls_reader", "GRANT USAGE ON SCHEMA public TO block_rls_reader", "CREATE TABLE block_rls(k integer)", "INSERT INTO block_rls VALUES(1)", "ALTER TABLE block_rls ENABLE ROW LEVEL SECURITY", "GRANT SELECT ON block_rls TO block_rls_reader"} {
			enginetest.Run(t, e, q)
		}
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "SET ROLE block_rls_reader", "BEGIN")
		capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_rls")
		if !capture.source.RlsDenied || capture.source.ReadLease != nil {
			t.Fatal("denied source acquired read eligibility")
		}
		_, stage := blocksUnitOpen(t, capture)
		if err := stage.Drain(capture.op.Ctx); err != nil {
			t.Fatal(err)
		}
		blocksUnitScope(t, e, capture.scope, capture.source.Ref, 0)
		stage.Close()
		capture.close(t)
	})
}
