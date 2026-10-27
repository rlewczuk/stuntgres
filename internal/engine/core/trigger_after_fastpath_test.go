package core_test

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
)

// Observe the existing activity/admission context check, not queue allocation:
// queueAfter used to do all of its synchronized work even with no definitions.
// The applicable-trigger controls prove that the observer reaches that path.
type afterAdmissionProbe struct {
	context.Context
	admissions atomic.Int64
}

func (p *afterAdmissionProbe) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".(*statementHost).queueAfter") {
			p.admissions.Add(1)
			break
		}
		if !more { break }
	}
	return p.Context.Err()
}

func TestTriggerAfterCascadeDeleteFastPath(t *testing.T) {
	for _, kind := range []string{"none", "irrelevant", "applicable"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			session := sessionStart(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE p(id integer PRIMARY KEY)",
				"CREATE TABLE c(pid integer REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)",
				"INSERT INTO p VALUES(1)",
				"INSERT INTO c VALUES(1),(1)",
			} { sessionSQL(t, session, q) }
			scope := foreignActionUnitScope(t, e, session, "UPDATE p SET id=2")
			named, physical := scope.LockedRef, scope.LockedStorage
			jobs, auth, err := dml.PrepareForeignActions(scope.WriteHost())
			if err != nil { t.Fatal(err) }
			job := foreignActionUnitJob(t, jobs, "c")
			ref := e.Catalog.Domain.Ref(auth.Access().Catalog.Epoch(), job.Child.Address())
			children, err := scope.CaptureForeignKeyRows(ref, ids.ObjectAddress{})
			if err != nil || len(children.Committed) != 2 { t.Fatal(err, children) }
			if err := core.ScopeAdmitForeignAction(scope, ref, job, true); err != nil { t.Fatal(err) }
			if kind != "none" {
				// Change only trigger metadata after admission. The relevance gate
				// must inspect the current validated descriptor, not the retained
				// admission's earlier, trigger-free relation descriptor.
				trigger, _ := e.CatalogRead().LookupType(2279)
				d := triggerUnitProcedure(t, e, scope, "fastpath_trigger", 9500003, trigger, proc.DMLTriggerInvocation, "BEGIN RETURN OLD; END")
				read := e.CatalogRead()
				p := triggerUnitDefinition(t, read, job.Child, d, "r", stored.TriggerInsert).Parts()
				p.Timing = stored.TriggerAfter
				if kind == "applicable" { p.Events = stored.TriggerDelete }
				definition, err := stored.NewTrigger(p)
				if err != nil { t.Fatal(err) }
				triggerUnitInstall(t, e, job.Child, definition)
			}
			probe := &afterAdmissionProbe{Context: scope.Op.Ctx}
			scope.Op.Ctx = probe
			previous := scope.WriteHost().SwapForeignActionAuthority(auth.ActingAs(job.Child.Owner))
			defer scope.WriteHost().SwapForeignActionAuthority(previous)
			core.ScopeForeignActions(scope).Settling = true
			defer func() { core.ScopeForeignActions(scope).Settling = false }()
			for _, old := range children.Committed {
				if err := scope.ForeignKeyGrant(ref, old.Row, lock.ForUpdate, children.Storage); err != nil { t.Fatal(err) }
				if err := core.WriteStageDeleteForeignAction(&scope.Stage, ref, old); err != nil { t.Fatal(err) }
			}
			admission := core.ScopeForeignActionsOrZero(scope).Admissions[ref.Address]
			if !admission.Mutated || len(scope.Stage.Deletes[ref.Address].Rows) != 2 || scope.LockedRef != named || scope.LockedStorage != physical {
				t.Fatal("witness lacks accepted child deletions under the original initiating admission")
			}
			calls := probe.admissions.Load()
			if kind == "applicable" {
				if calls == 0 || core.ScopeAfterTestFrames(scope)[0].Rows != 2 { t.Fatal("applicable control missed AFTER admission", calls) }
			} else if calls != 0 {
				t.Fatal("trigger-free/irrelevant cascading DELETE entered AFTER admission", calls)
			}
		})
	}
}

func TestTriggerAfterTruncateFastPath(t *testing.T) {
	for _, kind := range []string{"none", "irrelevant", "applicable"} {
		t.Run(kind, func(t *testing.T) {
			e, scope, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NULL; END")
			probe := &afterAdmissionProbe{Context: scope.Op.Ctx}
			scope.Op.Ctx = probe
			if kind != "none" {
				p := triggerUnitDefinition(t, read, relation, d, "s", stored.TriggerInsert).Parts()
				p.Timing, p.Level = stored.TriggerAfter, stored.TriggerStatement
				if kind == "applicable" { p.Events = stored.TriggerTruncate }
				definition, err := stored.NewTrigger(p)
				if err != nil { t.Fatal(err) }
				relation = triggerUnitInstall(t, e, relation, definition)
			}
			original := e.Storage.Relations[relation.Address()]
			if _, err := scope.Execute(enginetest.Statement(t, "TRUNCATE public.trigger_unit_table"), 0, nil); err != nil { t.Fatal(err) }
			change, ok := scope.PendingCatalog().Storage[relation.Address()]
			if !ok || change.Replacement == original || change.Replacement.Generation != original.Generation+1 || e.Storage.Relations[relation.Address()] != original {
				t.Fatal("witness lacks an actual private, unpublished TRUNCATE replacement")
			}
			calls := probe.admissions.Load()
			if kind == "applicable" {
				frame := core.ScopeAfterTestFrames(scope)[0]
				if calls == 0 || frame.Statements != 1 || frame.Qualified != 1 { t.Fatal("empty TRUNCATE lost applicable statement admission", calls, frame) }
			} else if calls != 0 {
				t.Fatal("trigger-free/irrelevant TRUNCATE entered AFTER admission", calls)
			}
		})
	}
}
