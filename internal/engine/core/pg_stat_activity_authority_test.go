package core_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
)

func activityAssertHorizon(t *testing.T, e *core.Engine, monitor *session.Session, token ids.SessionToken, ref ids.TxRef, want uint64) {
	t.Helper()
	e.Mu.Lock()
	advertised := e.Visibility.Entries[ref].ActivityFullXmin
	e.Mu.Unlock()
	if advertised != want {
		t.Fatal("advertised accepted horizon", advertised, want)
	}
	rows := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT backend_xmin FROM pg_stat_activity WHERE pid=%d", token.ID()))
	if len(rows) != 1 {
		t.Fatal("actual horizon row membership", rows)
	}
	if want == 0 {
		if !rows[0][0].IsNull() {
			t.Fatal("unaccepted horizon rendered", rows)
		}
	} else if xmin, err := rows[0][0].XID(); err != nil || xmin != uint32(want) {
		t.Fatal("actual accepted low32 horizon", xmin, want, err)
	}
}

func activitySafeCandidateAuthority(t *testing.T) {
	for _, mode := range []string{"safe", "reject", "cancel", "epoch"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			monitor := enginetest.StartSession(t, e, nil)
			writer := serializableUnitNode(t, e, false)
			// Control the owning SSI graph just as its existing safe-snapshot units do;
			// the monitored reader below still executes a genuinely admitted SQL command.
			e.Mu.Lock()
			e.Visibility.SetLastAssignedXIDLocked(1<<32 | 80)
			if err := e.AssignTransactionXIDLocked(writer.Ref); err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			e.Mu.Unlock()
			if mode == "reject" {
				out := serializableUnitNode(t, e, false)
				e.Mu.Lock()
				if err := e.AssignTransactionXIDLocked(out.Ref); err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				e.Visibility.EndTransactionIdentityLocked(out.Ref, true)
				writer.DidWrite = true
				writer.Outgoing[out] = struct{}{}
				out.Incoming[writer] = struct{}{}
				e.Visibility.SerializableCommittedLocked(out.Ref, e.Visibility.CommitSeq())
				e.Mu.Unlock()
			}
			target := enginetest.StartSession(t, e, nil)
			activityOwnerRun(t, e, target, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE")
			ref := target.Transactions.Ref
			e.Mu.Lock()
			node := e.Visibility.Serializable.Nodes[ref]
			e.Mu.Unlock()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			joined := false
			go func() { done <- activityOwnerExecute(e, target, ctx, "SELECT 1 /* accepted safe activity snapshot */") }()
			defer func() {
				cancel()
				if !joined {
					<-done
				}
			}()
			serializableUnitAwaitCandidate(t, e, node)
			e.Mu.Lock()
			candidate := target.Transactions.CurrentSnapshot
			installed := target.Transactions.SnapshotSet
			e.Mu.Unlock()
			if candidate == nil || installed {
				t.Fatal("unresolved actual candidate was installed", candidate, installed)
			}
			activityAssertHorizon(t, e, monitor, target.SessionToken, ref, 0)
			// Race an actual advertisement reader across rejection/recapture, so a
			// prematurely published rejected xmin is itself an observable failure.
			stop, started, probed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			rejectedPublished := false
			go func() {
				defer close(probed)
				first := true
				for {
					e.Mu.Lock()
					xmin := e.Visibility.Entries[ref].ActivityFullXmin
					e.Mu.Unlock()
					if mode == "reject" && xmin == candidate.Xmin() {
						rejectedPublished = true
					}
					if first {
						close(started)
						first = false
					}
					select {
					case <-stop:
						return
					default:
						runtime.Gosched()
					}
				}
			}()
			<-started
			probeStopped := false
			stopProbe := func() {
				if !probeStopped {
					close(stop)
					<-probed
					probeStopped = true
				}
			}
			defer stopProbe()
			switch mode {
			case "cancel":
				cancel()
			case "epoch":
				changes, err := e.ReplaceCatalog(t.Context(), e, e.CatalogRead())
				if err != nil || !changes.Replacement {
					t.Fatal("actual candidate epoch fence", changes, err)
				}
			default:
				e.Mu.Lock()
				commit := mode == "reject"
				if commit {
					e.Visibility.SerializableCommittedLocked(writer.Ref, e.Visibility.CommitSeq())
				}
				e.Visibility.EndTransactionIdentityLocked(writer.Ref, commit)
				core.EngineEndSerializableLocked(e, writer.Ref, commit)
				e.Mu.Unlock()
			}
			err := <-done
			joined = true
			stopProbe()
			if rejectedPublished {
				t.Fatal("rejected safe candidate was advertised")
			}
			if mode == "cancel" || mode == "epoch" {
				want := error(context.Canceled)
				if mode == "epoch" {
					want = ids.ErrCatalogReplaced
				}
				if !errors.Is(err, want) {
					t.Fatal("candidate ordinary failure boundary", err, want)
				}
				if target.Transactions.CurrentSnapshot != nil || target.Transactions.SnapshotSet {
					t.Fatal("failed candidate retained owner snapshot")
				}
				activityAssertHorizon(t, e, monitor, target.SessionToken, ref, 0)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			accepted := target.Transactions.CurrentSnapshot
			if accepted == nil || !target.Transactions.SnapshotSet || !node.Exempt {
				t.Fatal("actual accepted snapshot missing", accepted)
			}
			if mode == "safe" && accepted != candidate {
				t.Fatal("safe candidate was not retained")
			}
			if mode == "reject" && (accepted == candidate || accepted.Xmin() <= candidate.Xmin()) {
				t.Fatal("unsafe candidate was not replaced with a distinct public horizon", candidate, accepted)
			}
			activityAssertHorizon(t, e, monitor, target.SessionToken, ref, accepted.Xmin())
			activityOwnerRun(t, e, target, "ROLLBACK")
			activityAssertHorizon(t, e, monitor, target.SessionToken, ref, 0)
		})
	}
}

func activityNestedHorizonAuthority(t *testing.T) {
	for _, volatility := range []expr.Volatility{expr.Stable, expr.Volatile} {
		for _, primary := range []error{nil, errors.New("reached nested failure"), context.Canceled} {
			t.Run(fmt.Sprintf("%v/%v", volatility, primary), func(t *testing.T) {
				e := engine.New()
				writer := enginetest.StartSession(t, e, nil)
				target := enginetest.StartSession(t, e, nil)
				monitor := enginetest.StartSession(t, e, nil)
				activityOwnerRun(t, e, writer, "CREATE TABLE activity_nested_horizon(v integer)", "BEGIN", "INSERT INTO activity_nested_horizon VALUES(7)")
				activityOwnerRun(t, e, target, "BEGIN")
				op, _, err := target.AcquireCommand(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				op.OriginalSQL, op.SQLAvailable = "SELECT v FROM activity_nested_horizon", true
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				finished := false
				defer func() {
					if !finished {
						scope.Finish(nil)
					}
				}()
				if err := scope.Gate(enginetest.Statement(t, op.OriginalSQL)); err != nil {
					t.Fatal(err)
				}
				if err := core.ScopeSnapshotDataPhase(scope); err != nil {
					t.Fatal(err)
				}
				outer := core.ScopeCurrentSnapshot(scope)
				if outer == nil {
					t.Fatal("missing actual outer accepted snapshot")
				}
				activityAssertHorizon(t, e, monitor, target.SessionToken, scope.TxRef, outer.Xmin())
				activityOwnerRun(t, e, writer, "COMMIT")
				leave := core.ActivateRoutineCommandPolicy(scope, &catalog.Procedure{Volatility: volatility}, false)
				restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
				if err := core.ScopeSnapshotDataPhase(scope); err != nil {
					t.Fatal(err)
				}
				child := core.ScopeCurrentSnapshot(scope)
				if volatility == expr.Volatile {
					if child == outer || child.Xmin() <= outer.Xmin() {
						t.Fatal("volatile child did not accept distinct public horizon", outer, child)
					}
				} else if child != outer {
					t.Fatal("stable child did not inherit actual outer horizon")
				}
				activityAssertHorizon(t, e, monitor, target.SessionToken, scope.TxRef, child.Xmin())
				if err := restore(primary); err != primary {
					t.Fatal("nested unwind remapped primary", err, primary)
				}
				leave()
				if core.ScopeCurrentSnapshot(scope) != outer {
					t.Fatal("nested unwind lost exact outer snapshot")
				}
				activityAssertHorizon(t, e, monitor, target.SessionToken, scope.TxRef, outer.Xmin())
				ref := scope.TxRef
				result := scope.Finish(primary)
				finished = true
				if result != primary {
					t.Fatal("outer finish remapped primary", result, primary)
				}
				activityAssertHorizon(t, e, monitor, target.SessionToken, ref, 0)
			})
		}
	}
}

func activityRoleAuthority(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, owner, "CREATE ROLE activity_authority_login", "CREATE ROLE activity_authority_stats", "CREATE ROLE activity_authority_other", "GRANT pg_read_all_stats TO activity_authority_stats")
	readRoles := func() authorization.Read {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
	}
	roles := readRoles()
	target, _ := roles.Lookup("activity_authority_login")
	stats, _ := roles.Lookup("activity_authority_stats")
	other, _ := roles.Lookup("activity_authority_other")
	token := owner.SessionToken
	fact := e.Statistics.CaptureActivities()[token]
	fact.Principal = target.Ref
	producer, ok := pgcatalog.ProducerFor(pgcatalog.StatActivityOID)
	if !ok {
		t.Fatal("missing activity producer")
	}
	input := func(observer authorization.PrincipalRef) pgcatalog.Input {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		read := e.Catalog.Current()
		source, ok := read.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatActivityOID})
		if !ok {
			t.Fatal("missing registered activity source")
		}
		return pgcatalog.Input{Context: t.Context(), Owner: &e.Catalog.Owner, Catalog: read, Source: source, Ref: e.Catalog.Owner.Domain.Ref(read.Epoch(), source.Address()), Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, read), ActivityObserver: observer, Activities: map[ids.SessionToken]statistics.Activity{token: fact}}
	}
	activityOwnerRun(t, e, owner, "ALTER ROLE activity_authority_login RENAME TO activity_authority_renamed")
	rows, err := producer(input(owner.CountedAuthenticated))
	if err != nil || len(rows) != 1 {
		t.Fatal("present renamed producer target", rows, err)
	}
	if name, err := rows[0][5].Text(); err != nil || name != "activity_authority_renamed" {
		t.Fatal("producer current name", name, err)
	}
	activityOwnerRun(t, e, owner, "DROP ROLE activity_authority_renamed")
	if _, err := readRoles().InheritsRole(owner.CountedAuthenticated, target.Ref); err == nil {
		t.Fatal("global strict stale-target authority weakened")
	}
	for _, observer := range []authorization.PrincipalRef{owner.CountedAuthenticated, stats.Ref, other.Ref} {
		rows, err := producer(input(observer))
		if err != nil || len(rows) != 1 || !rows[0][5].IsNull() || rows[0][5].Type().ID() != sqltype.Name {
			t.Fatal("absent same-domain joined target", rows, err)
		}
		text, err := rows[0][20].Text()
		if err != nil {
			t.Fatal(err)
		}
		if observer == other.Ref {
			if text != "<insufficient privilege>" || !rows[0][10].IsNull() || !rows[0][16].IsNull() || !rows[0][21].IsNull() {
				t.Fatal("absent target leaked ordinary protected fields", rows)
			}
		} else if text != fact.Query || rows[0][10].IsNull() || rows[0][16].IsNull() || rows[0][21].IsNull() {
			t.Fatal("absent target suppressed stats privilege", rows)
		}
	}
	for _, malformed := range []authorization.PrincipalRef{{}, (&authorization.Domain{}).Principal(target.Ref.ID())} {
		bad := input(owner.CountedAuthenticated)
		corrupt := fact
		corrupt.Principal = malformed
		bad.Activities[token] = corrupt
		if _, err := producer(bad); err == nil {
			t.Fatal("malformed target principal accepted", malformed)
		}
	}
	if _, err := producer(input(target.Ref)); err == nil {
		t.Fatal("stale current observer accepted")
	}
}
