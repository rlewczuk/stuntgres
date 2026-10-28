package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func TestConstraintEnforcementHistoryOrdering(t *testing.T) {
	for _, tc := range []struct {
		name, actions string
		scheduled     []string
		disabled      bool
	}{
		{"enable-disable", "ALTER CONSTRAINT fk_b ENFORCED,ALTER CONSTRAINT fk_b NOT ENFORCED", []string{"fk_b"}, true},
		{"ordered-misc", "ALTER CONSTRAINT fk_b ENFORCED,ALTER CONSTRAINT fk_a ENFORCED,ALTER CONSTRAINT fk_b ENFORCED", []string{"fk_b", "fk_a"}, false},
		{"add-before-misc", "ALTER CONSTRAINT fk_b ENFORCED,ADD CONSTRAINT added FOREIGN KEY(a) REFERENCES p", []string{"added", "fk_b"}, false},
		{"add-disable", "ADD CONSTRAINT added FOREIGN KEY(a) REFERENCES p,ALTER CONSTRAINT added NOT ENFORCED", []string{"added"}, true},
		{"validate-disable", "VALIDATE CONSTRAINT fk_a,ALTER CONSTRAINT fk_a NOT ENFORCED", []string{"fk_a"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT,b INT,CONSTRAINT fk_a FOREIGN KEY(a) REFERENCES p NOT ENFORCED,CONSTRAINT fk_b FOREIGN KEY(b) REFERENCES p NOT ENFORCED)"} {
				sessionSQL(t, s, q)
			}
			if tc.name == "validate-disable" {
				sessionSQL(t, s, "ALTER TABLE t DROP CONSTRAINT fk_a,ADD CONSTRAINT fk_a FOREIGN KEY(a) REFERENCES p NOT VALID")
			}
			old := alterConstraintDescriptor(t, e, "t")
			original := slices.Clone(old.Constraints.ForeignKeys)
			base := e.Catalog.State
			store := e.Storage.Relations[old.Address()]
			op := alterConstraintOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if !scope.Finished {
					_ = scope.Finish(context.Canceled)
				}
			})
			statements, err := parser.Parse("ALTER TABLE t " + tc.actions)
			if err != nil {
				t.Fatal(err)
			}
			raw := statements[0].(*parser.AlterTable)
			if err := scope.Gate(raw); err != nil {
				t.Fatal(err)
			}
			if err := scope.AcquireRelation(raw, true); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.StatementAuthorizationLocked(op, raw)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			prep, err := tablecmds.PrepareAlterTableActions(e, scope, op, auth, old, scope.LockedRef, raw.Actions)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			predecessor := scope.PendingCatalog()
			scope.Catalog = predecessor.Clone()
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, s)
			scope.Inheritance.Alter = &core.InheritanceAlterState{Stage: stage, InheritanceAlterPlan: table.InheritanceAlterPlan{Root: old, Actions: raw.Actions, Types: prep.Types, Events: map[ids.ObjectAddress][]table.InheritanceAlterEvent{}}}
			e.Mu.Unlock()
			t.Cleanup(func() { scope.Inheritance.Alter = nil; e.DiscardCatalogStage(stage) })
			var physical []tablecmds.AlterTablePhysical
			e.Mu.Lock()
			_, err = tablecmds.AlterTableLocked(e, scope, op, auth, raw, 0, &physical)
			if err != nil {
				t.Fatal(err)
			}
			if len(physical) != 1 {
				t.Fatal("missing authentic work item", len(physical))
			}
			scheduled, changed := tablecmds.AlterForeignKeyWorklist(physical[0])
			var names []string
			for _, work := range scheduled {
				names = append(names, work.Name)
				if work.OID == 0 || work.NotEnforced {
					t.Fatal("not an admitted historical obligation", work)
				}
			}
			if changed || !slices.Equal(names, tc.scheduled) {
				t.Fatal(names, changed)
			}
			next := e.StageRead(stage).MustRelation(old.Address())
			checks, err := table.ResolveAlterForeignKeys(next, scheduled, nil)
			if err != nil || len(checks) != len(scheduled) {
				t.Fatal(checks, err)
			}
			if tc.disabled {
				found := false
				for _, key := range next.Constraints.ForeignKeys {
					if key.Name == scheduled[0].Name {
						found = key.NotEnforced && key.NotValid && key.TriggerOIDs == ([4]ids.OID{})
					}
				}
				if !found {
					t.Fatal("final descriptor lost disabled state")
				}
				unproved := slices.Clone(scheduled)
				unproved[0].Disabled = false
				if _, err := table.ResolveAlterForeignKeys(next, unproved, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("final false state accepted without a genuine disable", err)
				}
			}
			for i, check := range checks {
				if check.Child != next || check.Key.OID != scheduled[i].OID || check.Key.NotEnforced {
					t.Fatal("scan inherited final disabled flag", check)
				}
			}
			stale := slices.Clone(scheduled)
			stale[0].Index = next.Address()
			if _, err := table.ResolveAlterForeignKeys(next, stale, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("provider fence weakened", err)
			}
			invalid := slices.Clone(scheduled)
			invalid[0].NotValid = true
			if _, err := table.ResolveAlterForeignKeys(next, invalid, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("never-admitted invalid work accepted", err)
			}
			if scope.Owner.Catalog != predecessor || e.Catalog.State != base || e.Storage.Relations[old.Address()] != store || !reflect.DeepEqual(old.Constraints.ForeignKeys, original) {
				t.Fatal("private queue published or changed original")
			}
			scope.Inheritance.Alter = nil
			e.DiscardCatalogStage(stage)
			if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			op.Done()
			lockEmpty(t, e)
		})
	}
}
