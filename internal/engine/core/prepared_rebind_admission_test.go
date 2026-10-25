package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

type preparedRebindAdmissionResult struct {
	err     error
	target  ids.ObjectRef
	sources []ids.ObjectRef
	rows    bool
}

func preparedRebindWorker(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, description *core.StatementDescription) (<-chan preparedRebindAdmissionResult, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan preparedRebindAdmissionResult, 1)
	go func() {
		out := preparedRebindAdmissionResult{}
		op, err := s.Acquire(ctx)
		if err == nil {
			scope, er := e.StartStatement(op)
			err = er
			if err == nil {
				scope.Expected = description
				result, er := scope.Execute(ast, 0, nil)
				err = er
				out.rows = result.Rows != nil
				if result.Rows != nil {
					result.Rows.Close()
				}
				out.target = scope.LockedRef
				for _, leaf := range scope.QuerySources {
					out.sources = append(out.sources, leaf.Ref)
				}
				err = scope.Finish(err)
			}
			op.Done()
		}
		out.err = err
		done <- out
	}()
	return done, cancel
}

func preparedRebindJoin(t *testing.T, done <-chan preparedRebindAdmissionResult) preparedRebindAdmissionResult {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(7 * time.Second):
		t.Fatal("prepared worker failed to join")
	}
	return preparedRebindAdmissionResult{}
}

func TestPreparedRebindAdmission(t *testing.T) {
	for _, kind := range []string{"target", "nested_source"} {
		for _, action := range []string{"preexisting_shadow", "shadow_during_wait", "grant_definition", "namespace_while_invalid", "cancel", "reset", "restore"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				e := engine.New()
				caller, holder, ddl := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
				for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE b.t(v INT)", "SET search_path TO a,b"} {
					sessionSQL(t, caller, q)
				}
				query := "INSERT INTO t(v) VALUES(12)"
				if kind == "nested_source" {
					query = "SELECT v FROM (SELECT v FROM t) AS d"
				}
				ast, description := sequencePreparedDescription(t, e, caller, query)
				late := core.PreparedAnalysisTarget(core.DescriptionAnalysis(&description))
				if kind == "nested_source" {
					for _, source := range core.PreparedAnalysisSources(core.DescriptionAnalysis(&description)) {
						late = source.Ref
					}
				}
				var early ids.ObjectRef
				var earlyHolder ids.TxRef
				earlyHeld := false
				createHeldEarly := func() {
					sessionSQL(t, ddl, "CREATE TABLE a.t(v INT)")
					e.Mu.Lock()
					ns := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "a"})
					address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "t"})
					early = e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
					e.Mu.Unlock()
					earlyHolder = transactionBegin(t, ddl)
					if err := e.Locks.Acquire(context.Background(), ddl.Transactions.Ticket, lock.Resource{ObjectRef: early}, lock.AccessExclusive); err != nil {
						t.Fatal(err)
					}
					earlyHeld = true
				}
				defer func() {
					if earlyHeld {
						transactionEnd(t, ddl, earlyHolder, false)
					}
					enginetest.LockEmpty(t, e)
				}()
				if action == "preexisting_shadow" || action == "grant_definition" {
					createHeldEarly()
				}
				if action == "namespace_while_invalid" {
					sessionSQL(t, ddl, "ALTER TABLE b.t ADD unused INT")
				}
				holderRef := transactionBegin(t, holder)
				if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: late}, lock.AccessExclusive); err != nil {
					t.Fatal(err)
				}
				if action == "grant_definition" {
					sessionSQL(t, holder, "ALTER TABLE b.t ALTER v SET NOT NULL")
				}
				done, cancel := preparedRebindWorker(t, e, caller, ast, &description)
				joined := false
				defer func() {
					cancel()
					if !joined {
						preparedRebindJoin(t, done)
					}
				}()
				enginetest.LockPending(t, e, 1)
				before := core.DescriptionAnalysis(&description)
				switch action {
				case "cancel":
					cancel()
				case "reset":
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				case "restore":
					if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE SCHEMA a;CREATE SCHEMA b;CREATE TABLE b.t(v INT);INSERT INTO b.t VALUES(9)")); err != nil {
						t.Fatal(err)
					}
				case "shadow_during_wait":
					createHeldEarly()
				case "namespace_while_invalid":
					sessionSQL(t, ddl, "CREATE SCHEMA changed_while_waiting")
				}
				transactionEnd(t, holder, holderRef, action == "grant_definition")
				out := preparedRebindJoin(t, done)
				joined = true
				cancel()
				if action == "cancel" {
					if !errors.Is(out.err, context.Canceled) || out.rows || core.DescriptionAnalysis(&description) != before {
						t.Fatal("cancellation leaked result/publication", out)
					}
				} else if action == "shadow_during_wait" {
					if !errors.Is(out.err, ids.ErrCatalogReplaced) || out.rows || core.DescriptionAnalysis(&description) != before {
						t.Fatal("new shadow during admission crossed native conflict fence", out)
					}
					transactionRows(t, caller, "SELECT v FROM b.t")
				} else if action == "reset" || action == "restore" {
					if !errors.Is(out.err, ids.ErrCatalogReplaced) || out.rows || core.DescriptionAnalysis(&description) != before {
						t.Fatal("admitted replacement retargeted/published", out)
					}
					if action == "reset" {
						for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE b.t(v INT)"} {
							sessionSQL(t, ddl, q)
						}
					}
					result, err := sequencePreparedExecute(e, caller, ast, &description)
					if err != nil {
						t.Fatal("next builtin statement could not recover", err)
					}
					if result.Rows != nil {
						result.Rows.Close()
					}
				} else {
					if out.err != nil {
						t.Fatal("queued original identity did not complete", out.err)
					}
					if kind == "target" && out.target != late {
						t.Fatal("target retargeted", out.target, late)
					}
					if kind == "nested_source" && (len(out.sources) != 1 || out.sources[0] != late) {
						t.Fatal("nested source retargeted", out.sources, late)
					}
					if action == "namespace_while_invalid" && preparedRebindValid(t, e, caller, core.DescriptionAnalysis(&description)) {
						t.Fatal("older resolution certified under later namespace")
					}
					if action == "grant_definition" {
						if !core.PreparedAnalysisNeedsReanalysis(core.DescriptionAnalysis(&description)) || preparedRebindValid(t, e, caller, core.DescriptionAnalysis(&description)) {
							t.Fatal("grant-time definition evolution certified old resolution")
						}
						transactionEnd(t, ddl, earlyHolder, false)
						earlyHeld = false
						done, cancel = preparedRebindWorker(t, e, caller, ast, &description)
						joined = false
						next := preparedRebindJoin(t, done)
						joined = true
						cancel()
						if next.err != nil || core.PreparedAnalysisNeedsReanalysis(core.DescriptionAnalysis(&description)) {
							t.Fatal("next boundary did not reanalyze normally", next.err)
						}
						if kind == "target" && next.target != early || kind == "nested_source" && (len(next.sources) != 1 || next.sources[0] != early) {
							t.Fatal("next boundary kept pre-invalidation late identity", next)
						}
					}
				}
				if action == "cancel" || action == "reset" || action == "restore" {
					enginetest.LockEmpty(t, e)
				}
			})
		}
	}
}
