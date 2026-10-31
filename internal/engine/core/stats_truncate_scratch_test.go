package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func TestStatsSnapshotTruncateScratchOwnership(t *testing.T) {
	t.Run("nested_trigger", statsTruncateCommandOwnership)
	for _, outcome := range []string{"success", "error", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			e, s, p, q := truncateUnitFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, op := truncateUnitScope(t, e, s, ctx)
			if _, err := scope.Execute(truncateUnitAST("p"), 0, nil); err != nil {
				t.Fatal(err)
			}
			outer := core.ScopeTruncateSlot(scope)
			if outer == nil || len(outer.Relations) != 1 || outer.Relations[0].Ref != p {
				t.Fatal("outer manifest not captured")
			}
			backing, store := &outer.Relations[0], outer.Relations[0].Storage
			restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
			if core.ScopeTruncateSlot(scope) != nil {
				t.Fatal("nested command borrowed outer manifest")
			}
			if _, err := scope.Execute(truncateUnitAST("q"), 0, nil); err != nil {
				t.Fatal(err)
			}
			first := core.ScopeTruncateSlot(scope)
			if first == nil || first == outer || len(first.Relations) != 1 || first.Relations[0].Ref != q {
				t.Fatal("nested selection accumulated unrelated outer targets")
			}
			var primary error
			if outcome == "error" {
				_, primary = scope.Execute(truncateUnitAST("missing_truncate_target"), 0, nil)
				if primary == nil {
					t.Fatal("missing target did not fail")
				}
			}
			if outcome == "cancel" {
				cancel()
				primary = op.Context().Err()
				if !errors.Is(primary, context.Canceled) {
					t.Fatal("cancellation did not reach live operation")
				}
			}
			if got := restore(primary); got != primary {
				t.Fatal("restoration replaced primary error", got, primary)
			}
			restored := core.ScopeTruncateSlot(scope)
			if restored != outer || &restored.Relations[0] != backing || restored.Relations[0].Storage != store || restored.Selected[p.Address] != 1 || restored.Selected[q.Address] != 0 {
				t.Fatal("suspended outer pointer/backing changed")
			}
			if outcome == "success" {
				current := scope.Catalog.Storage[q.Address].Replacement
				secondRestore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
				if core.ScopeTruncateSlot(scope) != nil {
					t.Fatal("sequential command retained prior child")
				}
				if _, err := scope.Execute(truncateUnitAST("q"), 0, nil); err != nil {
					t.Fatal(err)
				}
				second := core.ScopeTruncateSlot(scope)
				if second == nil || second == first || second == outer || len(second.Relations) != 1 || second.Relations[0].Storage != current {
					t.Fatal("sequential command reused stale admitted storage")
				}
				if err := secondRestore(nil); err != nil || core.ScopeTruncateSlot(scope) != outer {
					t.Fatal("second unwind lost outer manifest", err)
				}
			}
		})
	}
	t.Run("rotation_discards_suspended_manifest", func(t *testing.T) {
		e, s, _, _ := truncateUnitFixture(t)
		scope, _ := truncateUnitScope(t, e, s, context.Background())
		if err := scope.Gate(&parser.Do{}); err != nil {
			t.Fatal(err)
		}
		if _, err := scope.Execute(truncateUnitAST("p"), 0, nil); err != nil {
			t.Fatal(err)
		}
		if core.ScopeTruncateSlot(scope) == nil {
			t.Fatal("missing old manifest")
		}
		restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
		_, run, control := transactionUnitRunOnScope(t, scope, "BEGIN ROLLBACK; RETURN 7; END")
		previous := scope.TxRef
		if err := run.Host.Transaction(control); err != nil {
			t.Fatal(err)
		}
		if scope.TxRef == previous || core.ScopeTruncateSlot(scope) != nil {
			t.Fatal("rotation retained old transaction manifest")
		}
		if err := restore(nil); err != nil || core.ScopeTruncateSlot(scope) != nil {
			t.Fatal("lexical unwind resurrected retired manifest", err)
		}
	})
	t.Run("actual_stale_storage_rejects", func(t *testing.T) {
		e, s, p, _ := truncateUnitFixture(t)
		scope, _ := truncateUnitScope(t, e, s, context.Background())
		statement := truncateUnitAST("p")
		// StartStatement is provisional: the normal public path gates before
		// relation admission to install the transaction incarnation and ticket.
		if err := scope.Gate(statement); err != nil {
			t.Fatal(err)
		}
		if err := scope.AcquireRelation(statement, true); err != nil {
			t.Fatal(err)
		}
		admitted := core.ScopeTruncateSlot(scope)
		if admitted == nil || len(admitted.Relations) != 1 {
			t.Fatal("missing admitted manifest")
		}
		original := admitted.Relations[0].Storage
		// Real storage-only host publication after admission is not a new SQL
		// command. Keep the exact descriptor and reject the stale store pointer.
		replacement := *original
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		// Match the existing host storage-remint path used by
		// TestAnalyzePreparedAuthorityRejection. AlterRelation instead requires
		// a descriptor revision, which would veto this storage-only fixture.
		stage.RowShapes = map[ids.ObjectAddress]storage.RowShape{p.Address: {
			Original: original, Root: original.Versions, Replacement: &replacement,
			ObservedLength: len(original.Versions), ObservedEnds: original.Ends, Generation: original.Generation,
		}}
		truncateUnitPublish(t, e, stage)
		if e.CatalogRead().MustRelation(p.Address) != base.MustRelation(p.Address) || e.Storage.Relations[p.Address] != &replacement {
			t.Fatal("host publication did not remint storage under the same descriptor")
		}
		err := core.ScopeStageTruncate(scope)
		if !errors.Is(err, ids.ErrCatalogReplaced) || core.ScopeTruncateSlot(scope) != admitted {
			t.Fatal("stale authority was refreshed or accepted", err)
		}
		if finished := scope.Finish(err); !errors.Is(finished, ids.ErrCatalogReplaced) {
			t.Fatal("failed command lost its stale-authority error during unwind", finished)
		}
		scope.Op.Done()
		enginetest.LockEmpty(t, e)
		if e.Storage.Relations[p.Address] != &replacement || original.TruncateLineage != nil || len(original.Versions) != 2 {
			t.Fatal("failed guard changed committed storage")
		}
	})
}
