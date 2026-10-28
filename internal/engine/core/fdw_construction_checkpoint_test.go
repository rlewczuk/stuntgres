package core_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
)

func TestFDWForeignConstructionCheckpoint(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "pending_suffix"
		if completed {
			name = "completed_suffix"
		}
		t.Run(name, func(t *testing.T) {
			e, scope, audit := foreignConstructionUnitScope(t)
			mark, err := scope.BeginPLException()
			if err != nil {
				t.Fatal(err)
			}
			stage, jobs, relation := foreignConstructionUnitStage(t, e, scope, "checkpoint_suffix")
			token, err := scope.StageForeignConstruction(stage, relation.Address(), []int32{relation.Attributes[0]})
			if err != nil {
				t.Fatal(err)
			}
			if completed {
				foreignConstructionUnitComplete(t, scope, token, slices.Clone(jobs))
			} else if err := fdw.RunValidations(scope.Op.Context(), scope, jobs[:1]); err != nil {
				t.Fatal(err)
			}
			if err := scope.EndPLException(mark, true, errors.New("caught suffix")); err != nil {
				t.Fatal(err)
			}
			if core.ScopePendingForeignConstructions(scope) != 0 || core.ForeignConstructionRetainsState(token) {
				t.Fatal("rollback retained failed-suffix construction ownership")
			}
			if err := token.Advance(0, nil); err == nil || err.Error() != "stuntgres: inactive foreign construction" {
				t.Fatal("rolled-back token remained callable", err)
			}
			if foreignConstructionUnitRead(e, scope).Get(relation.Address()) != nil || len(scope.Stage.Rows[audit]) != 0 {
				t.Fatal("rollback retained suffix metadata or audit writes")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal("recovered statement could not finish", err)
			}
			if e.CatalogRead().Get(relation.Address()) != nil || len(e.Storage.Relations[audit].Versions) != 0 {
				t.Fatal("recovered statement published suffix effects")
			}
		})
	}
	for _, completed := range []bool{false, true} {
		name := "restore_advanced_prefix"
		if completed {
			name = "restore_completed_prefix"
		}
		t.Run(name, func(t *testing.T) {
			e, scope, audit := foreignConstructionUnitScope(t)
			stage, jobs, relation := foreignConstructionUnitStage(t, e, scope, "checkpoint_prefix")
			token, err := scope.StageForeignConstruction(stage, relation.Address(), []int32{relation.Attributes[0]})
			if err != nil {
				t.Fatal(err)
			}
			if err := fdw.RunValidations(scope.Op.Context(), scope, jobs[:1]); err != nil {
				t.Fatal(err)
			}
			if err := token.Advance(0, nil); err != nil {
				t.Fatal(err)
			}
			prior := foreignConstructionUnitRead(e, scope).MustRelation(relation.Address())
			mark, err := scope.BeginPLException()
			if err != nil {
				t.Fatal(err)
			}
			column, options, err := fdw.PrepareColumnValidation(foreignConstructionUnitRead(e, scope), jobs[1])
			if err != nil {
				t.Fatal(err)
			}
			if err := fdw.RunValidations(scope.Op.Context(), scope, []fdw.Validation{column}); err != nil {
				t.Fatal(err)
			}
			if err := token.Advance(column.Attribute, options); err != nil {
				t.Fatal(err)
			}
			if completed {
				if err := token.Complete(); err != nil {
					t.Fatal(err)
				}
			}
			if err := scope.EndPLException(mark, true, errors.New("caught after prefix progress")); err != nil {
				t.Fatal(err)
			}
			if core.ScopePendingForeignConstructions(scope) != 1 || !core.ForeignConstructionRetainsState(token) || foreignConstructionUnitRead(e, scope).Get(relation.Address()) != prior || len(scope.Stage.Rows[audit]) != 1 {
				t.Fatal("rollback did not restore the pending prefix and its exact predecessor")
			}
			if err := token.Complete(); err == nil || err.Error() != "stuntgres: incomplete foreign column construction" {
				t.Fatal("rollback did not restore prefix phase progress", err)
			}
			foreignConstructionUnitComplete(t, scope, token, slices.Clone(jobs[1:]))
			if err := scope.Finish(nil); err != nil {
				t.Fatal("restored prefix could not complete", err)
			}
			final := e.CatalogRead().MustRelation(relation.Address())
			if final.RowType != relation.RowType || final.Foreign == nil || len(final.Foreign.Columns[relation.Attributes[0]]) != 1 || len(e.Storage.Relations[audit].Versions) != 2 {
				t.Fatal("restored prefix lost final identity, options or audit effects")
			}
		})
	}
	t.Run("nested_rollback_membership", func(t *testing.T) {
		e, scope, audit := foreignConstructionUnitScope(t)
		outerStage, outerJobs, outerRelation := foreignConstructionUnitStage(t, e, scope, "checkpoint_outer")
		outer, err := scope.StageForeignConstruction(outerStage, outerRelation.Address(), []int32{outerRelation.Attributes[0]})
		if err != nil {
			t.Fatal(err)
		}
		outerMark, err := scope.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		innerStage, innerJobs, innerRelation := foreignConstructionUnitStage(t, e, scope, "checkpoint_inner")
		inner, err := scope.StageForeignConstruction(innerStage, innerRelation.Address(), []int32{innerRelation.Attributes[0]})
		if err != nil {
			t.Fatal(err)
		}
		innerMark, err := scope.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		foreignConstructionUnitComplete(t, scope, inner, slices.Clone(innerJobs))
		if err := scope.EndPLException(innerMark, true, errors.New("caught inner")); err != nil {
			t.Fatal(err)
		}
		if core.ScopePendingForeignConstructions(scope) != 2 || !core.ForeignConstructionRetainsState(inner) || !core.ForeignConstructionRetainsState(outer) {
			t.Fatal("inner rollback lost prefix construction membership")
		}
		if err := scope.EndPLException(outerMark, true, errors.New("caught outer")); err != nil {
			t.Fatal(err)
		}
		if core.ScopePendingForeignConstructions(scope) != 1 || core.ForeignConstructionRetainsState(inner) || !core.ForeignConstructionRetainsState(outer) || foreignConstructionUnitRead(e, scope).Get(innerRelation.Address()) != nil || len(scope.Stage.Rows[audit]) != 0 {
			t.Fatal("outer rollback did not invalidate its suffix while preserving the earlier guard")
		}
		foreignConstructionUnitComplete(t, scope, outer, slices.Clone(outerJobs))
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if e.CatalogRead().Get(innerRelation.Address()) != nil || e.CatalogRead().Get(outerRelation.Address()) == nil || len(e.Storage.Relations[audit].Versions) != 2 {
			t.Fatal("nested rollback did not publish only the recovered prefix")
		}
	})
	t.Run("successful_release_keeps_completion", func(t *testing.T) {
		e, scope, audit := foreignConstructionUnitScope(t)
		stage, jobs, relation := foreignConstructionUnitStage(t, e, scope, "checkpoint_released")
		token, err := scope.StageForeignConstruction(stage, relation.Address(), []int32{relation.Attributes[0]})
		if err != nil {
			t.Fatal(err)
		}
		mark, err := scope.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		foreignConstructionUnitComplete(t, scope, token, slices.Clone(jobs))
		if err := scope.EndPLException(mark, false, nil); err != nil {
			t.Fatal(err)
		}
		if core.ScopePendingForeignConstructions(scope) != 0 || core.ForeignConstructionRetainsState(token) {
			t.Fatal("successful checkpoint release resurrected a completed token")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if e.CatalogRead().Get(relation.Address()) == nil || len(e.Storage.Relations[audit].Versions) != 2 {
			t.Fatal("successful checkpoint release lost completed effects")
		}
	})
}
