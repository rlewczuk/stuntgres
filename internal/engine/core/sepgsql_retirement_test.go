package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func sepgsqlRetirementStage(t *testing.T, setup ...string) (*core.Engine, *core.StatementScope, *core.CatalogStage, ids.ObjectAddress) {
	t.Helper()
	e := sepgsqlPhaseEngine(t)
	enginetest.Run(t, e, "CREATE TABLE retirement_receipt(a integer,b integer)")
	for _, query := range setup {
		enginetest.Run(t, e, query)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled) })
	if err = scope.Gate(enginetest.Statement(t, "DROP TABLE retirement_receipt")); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	stage.DependencyPath, _ = core.ReferencePath(auth)
	address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "retirement_receipt"})
	e.Mu.Unlock()
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return e, scope, stage, address
}

func TestSepgsqlSimulationRetirement(t *testing.T) {
	t.Run("deduplicated_observation_marks", func(t *testing.T) {
		_, scope, stage, address := sepgsqlRetirementStage(t)
		drop := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
		before := scope.MarkSepgsqlObservations()
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{address}, nil); err != nil {
			t.Fatal(err)
		}
		first, ok := core.SepgsqlRetirementCompletion(scope, address)
		if !ok || first == 0 {
			t.Fatal("missing completed whole retirement")
		}
		mark := scope.MarkSepgsqlObservations()
		column := address
		column.SubID = 1
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{column}, nil); err != nil {
			t.Fatal(err)
		}
		second, ok := core.SepgsqlRetirementCompletion(scope, column)
		after := scope.MarkSepgsqlObservations()
		if !ok || second <= first || after.Observations != mark.Observations {
			t.Fatal("column retirement must share whole-drop observation but own a new completion", mark, after, first, second)
		}
		// Reusing A during failed inner work does not advance its completion.
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{address}, nil); err != nil {
			t.Fatal(err)
		}
		scope.RestoreSepgsqlObservations(mark)
		if got, ok := core.SepgsqlRetirementCompletion(scope, address); !ok || got != first {
			t.Fatal("restore discarded older/reused receipt")
		}
		if _, ok := core.SepgsqlRetirementCompletion(scope, column); ok {
			t.Fatal("deduplicated failed-action receipt survived")
		}
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{column}, nil); err != nil {
			t.Fatal(err)
		}
		third, _ := core.SepgsqlRetirementCompletion(scope, column)
		if third <= second {
			t.Fatal("rollback rewound completion sequence")
		}
		nested := scope.MarkSepgsqlObservations()
		other := address
		other.SubID = 2
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{other}, nil); err != nil {
			t.Fatal(err)
		}
		scope.RestoreSepgsqlObservations(nested)
		if _, ok := core.SepgsqlRetirementCompletion(scope, other); ok {
			t.Fatal("nested newer receipt survived")
		}
		if got, ok := core.SepgsqlRetirementCompletion(scope, column); !ok || got != third {
			t.Fatal("nested restore removed lawful outer receipt")
		}
		scope.RestoreSepgsqlObservations(before)
		if _, ok := core.SepgsqlRetirementCompletion(scope, address); ok {
			t.Fatal("outer restore retained receipt")
		}
	})
	t.Run("stage_bound_duplicate_and_owned_lineage", func(t *testing.T) {
		e, scope, stage, address := sepgsqlRetirementStage(t)
		drop := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
		before := scope.MarkSepgsqlObservations()
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{address}, nil); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		foreign := e.BeginCatalogStage(auth.Catalog)
		foreign.AuthorizeLocked(auth, scope.Op.Session)
		e.Mu.Unlock()
		defer e.DiscardCatalogStage(foreign)
		other := core.StageDropProvider(scope.Op.Context(), foreign, scope, nil, nil)
		if _, err := core.ConsumeSepgsqlRetirement(other, address); err != ids.ErrCatalogReplaced {
			t.Fatal("another stage adopted receipt", err)
		}
		if consumed, err := core.ConsumeSepgsqlRetirement(drop, address); err != nil || !consumed {
			t.Fatal(consumed, err)
		}
		if _, err := core.ConsumeSepgsqlRetirement(drop, address); err != ids.ErrCatalogReplaced {
			t.Fatal("duplicate live effect consumed receipt", err)
		}
		scope.RestoreSepgsqlObservations(before)
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{address}, nil); err != nil {
			t.Fatal(err)
		}
		if err := drop.Remove(stage.BaseRef(address)); err != nil {
			t.Fatal("own removal lost captured authority", err)
		}
		if err := drop.Remove(stage.BaseRef(address)); err != nil {
			t.Fatal("same proven already-removed effect lost lineage", err)
		}
		if stage.Get(address) != nil || stage.Base.Get(address) == nil {
			t.Fatal("real removal or immutable predecessor changed")
		}
	})
	t.Run("peer_label_stale_without_policy_replay", func(t *testing.T) {
		e, scope, stage, address := sepgsqlRetirementStage(t)
		drop := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{address}, nil); err != nil {
			t.Fatal(err)
		}
		enginetest.Run(t, e, "SECURITY LABEL ON TABLE retirement_receipt IS 'S'")
		err := core.ValidateSepgsqlRetirementPredecessors(scope)
		var conflict catalog.Conflict
		if !errors.As(err, &conflict) {
			t.Fatal("peer label was reallowed or stale predecessor accepted", err)
		}
		if stage.Removed(address) || e.CatalogRead().Get(address) == nil {
			t.Fatal("stale validation published a retirement prefix")
		}
	})
	t.Run("completion_exhaustion", func(t *testing.T) {
		_, scope, stage, address := sepgsqlRetirementStage(t)
		core.SetSepgsqlRetirementCompletion(scope, ^uint64(0))
		drop := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
		if err := drop.PrepareSepgsqlRetirements([]ids.ObjectAddress{address}, nil); err == nil {
			t.Fatal("completion exhaustion accepted")
		}
		if _, ok := core.SepgsqlRetirementCompletion(scope, address); ok {
			t.Fatal("failed completion installed receipt")
		}
	})
}

// Compose the real preparation, removal, dependency and publication owners.
// The public Labels fixture retains the full transaction/subject SQL sequence;
// this witness distinguishes the first failing owner from its later final fence.
func TestSepgsqlSimulationRetirementPublication(t *testing.T) {
	for _, history := range []bool{false, true} {
		name := "original_shape"
		var setup []string
		if history {
			name = "renamed_dropped_column"
			setup = []string{
				"SECURITY LABEL ON TABLE retirement_receipt IS 'S'",
				"SECURITY LABEL ON COLUMN retirement_receipt.b IS 'S'",
				"SECURITY LABEL ON COLUMN retirement_receipt.tableoid IS 'S'",
				"ALTER TABLE retirement_receipt RENAME COLUMN b TO renamed",
				"ALTER TABLE retirement_receipt DROP COLUMN renamed",
			}
		}
		t.Run(name, func(t *testing.T) {
			for _, change := range []string{"own_removal", "peer_relabel", "peer_descriptor", "cancel"} {
				t.Run(change, func(t *testing.T) {
					e, scope, stage, address := sepgsqlRetirementStage(t, setup...)
					before := e.CatalogRead()
					original := before.MustRelation(address)
					column := address
					column.SubID = 2
					key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
					columnKey := catalog.SecurityLabelKey{Object: column, Provider: "selinux"}
					if history && (original.HasAttribute(2) || before.State().SecurityLabel(columnKey) != nil) {
						t.Fatal("real column history did not retire its stable attribute and label")
					}
					drop := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
					plan, err := drop.PrepareExpressionDependencies([]ids.ObjectAddress{address}, parser.DropRestrict, ids.ObjectAddress{})
					if err != nil {
						t.Fatal("prepare genuine dependency and retirement cohort:", err)
					}
					completion, ok := core.SepgsqlRetirementCompletion(scope, address)
					if !ok || completion == 0 || e.StageRead(stage).Get(address) != original || stage.Removed(address) {
						t.Fatal("preparation did not retain the live source and completed retirement", completion, ok)
					}
					if err := core.ValidateSepgsqlRetirementPredecessors(scope); err != nil {
						t.Fatal("prepared live predecessors:", err)
					}
					if err := stage.Remove(stage.BaseRef(address)); err != nil {
						t.Fatal("consume genuine root retirement:", err)
					}
					if err := drop.ApplyExpressionDependencies(plan); err != nil {
						t.Fatal("apply captured dependency phase:", err)
					}
					if err := drop.ApplyOwnedExpressionDependencies(plan); err != nil {
						t.Fatal("apply captured owned phase:", err)
					}
					if consumed, err := core.ConsumeSepgsqlRetirement(drop, address); err != nil || !consumed {
						t.Fatal("real removal did not retain its stage-bound consumed receipt:", consumed, err)
					}
					if next, ok := core.SepgsqlRetirementCompletion(scope, address); !ok || next != completion {
						t.Fatal("own removal reminted or lost its completed retirement", completion, next, ok)
					}
					if stage.Get(address) != nil || !stage.Removed(address) || stage.Base.Get(address) != original || e.CatalogRead().Get(address) != original {
						t.Fatal("private removal changed or lost its committed predecessor")
					}
					if err := core.ValidateSepgsqlRetirementPredecessors(scope); err != nil {
						t.Fatal("own removal invalidated the committed MAC predecessor:", err)
					}
					prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
					if err != nil {
						t.Fatal("validate real removed candidate:", err)
					}
					if err := core.ValidateSepgsqlRetirementPredecessors(scope); err != nil {
						t.Fatal("candidate validation invalidated the committed MAC predecessor:", err)
					}
					switch change {
					case "peer_relabel":
						enginetest.Run(t, e, "SECURITY LABEL ON TABLE retirement_receipt IS 'U'")
					case "peer_descriptor":
						enginetest.Run(t, e, "COMMENT ON TABLE retirement_receipt IS 'peer successor'")
					case "cancel":
						ctx, cancel := context.WithCancel(scope.Op.Context())
						scope.Op.Ctx = ctx
						cancel()
					}
					current := e.CatalogRead()
					_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: before.Epoch()})
					if change == "own_removal" {
						if err != nil {
							t.Fatal("publish genuine own-removal lineage:", err)
						}
						if e.CatalogRead().Get(address) != nil || e.CatalogRead().State().SecurityLabel(key) != nil {
							t.Fatal("successful publication retained the removed relation or label")
						}
						if err := core.ValidateSepgsqlRetirementPredecessors(scope); err != nil {
							t.Fatal("own publication did not advance only its predecessor fence:", err)
						}
						scope.CompleteUtility()
						if err := scope.Finish(nil); err != nil {
							t.Fatal("finish genuinely published retirement:", err)
						}
					} else {
						if change == "cancel" {
							if !errors.Is(err, context.Canceled) {
								t.Fatal("canceled retirement published:", err)
							}
						} else {
							var conflict catalog.Conflict
							if !errors.As(err, &conflict) {
								t.Fatal("peer successor was adopted or reallowed:", err)
							}
						}
						if e.CatalogRead().State() != current.State() || current.Get(address) == nil {
							t.Fatal("rejected retirement published a private removal prefix")
						}
					}
					if before.Get(address) != original || before.State().SecurityLabel(key) == nil {
						t.Fatal("retirement changed the retained immutable catalog")
					}
				})
			}
		})
	}
}
