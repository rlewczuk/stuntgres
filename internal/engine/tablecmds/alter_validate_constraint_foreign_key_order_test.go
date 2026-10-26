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

func TestAlterValidateConstraintForeignKeyOrdering(t *testing.T) {
	for _, tc := range []struct {
		name, actions       string
		scheduled, resolved []string
		changed             bool
	}{
		{"reverse-misc", "VALIDATE CONSTRAINT fk_b, VALIDATE CONSTRAINT fk_a, VALIDATE CONSTRAINT fk_b", []string{"fk_b", "fk_a"}, []string{"fk_b", "fk_a"}, false},
		{"add-first", "ADD CONSTRAINT fk_new FOREIGN KEY(c) REFERENCES p, VALIDATE CONSTRAINT fk_a", []string{"fk_new", "fk_a"}, []string{"fk_new", "fk_a"}, false},
		{"misc-first", "VALIDATE CONSTRAINT fk_a, ADD CONSTRAINT fk_new FOREIGN KEY(c) REFERENCES p", []string{"fk_new", "fk_a"}, []string{"fk_new", "fk_a"}, false},
		{"not-valid-enters-at-misc", "ADD CONSTRAINT fk_new FOREIGN KEY(c) REFERENCES p NOT VALID, VALIDATE CONSTRAINT fk_b, VALIDATE CONSTRAINT fk_new, VALIDATE CONSTRAINT fk_a", []string{"fk_b", "fk_new", "fk_a"}, []string{"fk_b", "fk_new", "fk_a"}, false},
		{"changed-value-prefix", "ALTER a TYPE BIGINT, VALIDATE CONSTRAINT fk_b, VALIDATE CONSTRAINT fk_a", []string{"fk_b", "fk_a"}, []string{"fk_a", "fk_b"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
			sessionSQL(t, s, "CREATE TABLE t(a INT,b INT,c INT)")
			sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT fk_a FOREIGN KEY(a) REFERENCES p NOT VALID, ADD CONSTRAINT fk_b FOREIGN KEY(b) REFERENCES p NOT VALID")
			old := alterConstraintDescriptor(t, e, "t")
			oldKeys := slices.Clone(old.Constraints.ForeignKeys)
			before, store := e.Catalog.State, e.Storage.Relations[old.Address()]
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
			preparation, err := tablecmds.PrepareAlterTableActions(e, scope, op, auth, old, scope.LockedRef, raw.Actions)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			// Retain the genuine sole-stage collector boundary, as hierarchy
			// orchestration does, so the actual scheduled payload is observable
			// before metadata or physical publication. No scan is substituted.
			// The shared-stage caller, not the re-entered controller, owns this
			// statement-private journal. Mirror alterInheritanceLocked's clone
			// before exposing its shared stage; do not borrow a nil TX predecessor.
			e.Mu.Lock()
			predecessor := scope.PendingCatalog()
			scope.Catalog = predecessor.Clone()
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, s)
			scope.Inheritance.Alter = &core.InheritanceAlterState{Stage: stage, InheritanceAlterPlan: table.InheritanceAlterPlan{
				Root: old, Actions: raw.Actions, Types: preparation.Types, Events: map[ids.ObjectAddress][]table.InheritanceAlterEvent{},
			}}
			e.Mu.Unlock()
			t.Cleanup(func() { scope.Inheritance.Alter = nil; e.DiscardCatalogStage(stage) })
			var physical []tablecmds.AlterTablePhysical
			e.Mu.Lock()
			_, err = tablecmds.AlterTableLocked(e, scope, op, auth, raw, 0, &physical)
			if err != nil {
				t.Fatal(err)
			}
			if len(physical) != 1 {
				t.Fatal("missing actual work item", len(physical))
			}
			if scope.Owner.Catalog != predecessor {
				t.Fatal("collector installed its private journal before Finish")
			}
			scheduled, changed := tablecmds.AlterForeignKeyWorklist(physical[0])
			names := make([]string, len(scheduled))
			for i, key := range scheduled {
				names[i] = key.Name
			}
			if !slices.Equal(names, tc.scheduled) || changed != tc.changed {
				t.Fatal("lost provider-phase queue", names, changed)
			}
			next := e.StageRead(stage).MustRelation(old.Address())
			var prefix []table.ForeignKeyCheck
			if changed {
				prefix = table.ReferencingForeignKeys(e.StageRead(stage), next.Address())
			}
			checks, err := table.ResolveAlterForeignKeys(next, scheduled, prefix)
			if err != nil {
				t.Fatal(err)
			}
			resolved := make([]string, len(checks))
			for i, check := range checks {
				resolved[i] = check.Key.Name
				if check.Child != next || check.Key.NotValid || check.Key.OID == 0 {
					t.Fatal("not authentic final work", check)
				}
			}
			if !slices.Equal(resolved, tc.resolved) {
				t.Fatal("resolver resorted scheduled work", resolved, tc.resolved)
			}
			// Resolver-level duplicates, including an independently required
			// prefix, retain their earliest scheduled positions without mutation.
			duplicates := append(slices.Clone(scheduled), scheduled...)
			again, err := table.ResolveAlterForeignKeys(next, duplicates, checks[:1])
			if err != nil || len(again) != len(checks) {
				t.Fatal("worklist duplicate changed cardinality", again, err)
			}
			for i := range checks {
				if again[i].Key.OID != checks[i].Key.OID || again[i].Child != checks[i].Child {
					t.Fatal("dedup changed first position")
				}
			}
			stale := slices.Clone(scheduled)
			stale[0].OID = 0
			if _, err := table.ResolveAlterForeignKeys(next, stale, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("missing stable identity accepted", err)
			}
			stale = slices.Clone(scheduled)
			stale[0].Index = next.Address()
			if _, err := table.ResolveAlterForeignKeys(next, stale, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("replaced supporting definition accepted", err)
			}
			if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || !reflect.DeepEqual(old.Constraints.ForeignKeys, oldKeys) {
				t.Fatal("queue cooking changed borrowed originals")
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
