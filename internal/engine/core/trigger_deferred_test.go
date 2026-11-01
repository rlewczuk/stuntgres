package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func deferredUnitDefinition(t *testing.T, read catalog.Read, relation *catalog.Relation, procedure *catalog.Procedure) stored.Trigger {
	t.Helper()
	p := triggerUnitDefinition(t, read, relation, procedure, "deferred", stored.TriggerInsert|stored.TriggerUpdate).Parts()
	p.Timing = stored.TriggerAfter
	p.Constraint = catalog.TriggerConstraint{OID: 9500011, Name: "deferred", Deferrable: true, InitiallyDeferred: true}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func deferredUnitAccept(t *testing.T, s *core.StatementScope, read catalog.Read, relation *catalog.Relation, id int64) {
	t.Helper()
	integer, _ := read.LookupType(sqltype.Int4)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	row := []sqltype.Value{triggerUnitInt(t, integer, id), triggerUnitInt(t, integer, id*10)}
	count, err := dml.ApplyInsertValidated(s.ActivateRuntime(s.Op.Context()), s.WriteHost(), ref, index.WriteBatch{Values: [][]sqltype.Value{row}})
	if err != nil || count != 1 {
		t.Fatal("accept physical row", count, err)
	}
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerDeferredQueueOwnership(t *testing.T) {
	e, s, read, relation, procedure := triggerUnitFixture(t, "BEGIN NEW.v:=999; RETURN NEW; END")
	definition := deferredUnitDefinition(t, read, relation, procedure)
	relation = triggerUnitInstall(t, e, relation, definition)
	triggerUnitAdmit(t, s, relation)
	integer, _ := read.LookupType(sqltype.Int4)
	ref := definition.Parts().Relation
	image := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	targets := []int32{2}
	if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, New: image, Old: image, Targets: targets}); err != nil {
		t.Fatal(err)
	}
	if !s.HasPendingConstraintEvents(ref.Address) {
		t.Fatal("unfinished qualified frame is invisible to DDL")
	}
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	slot := core.ScopeIndexesSlot(s)
	before := slot.ConstraintModes
	if len(before.Events) != 1 || before.Events[0].UserTrigger == nil || before.Events[0].ForeignKey != nil || before.Events[0].Index != (ids.ObjectRef{}) {
		t.Fatal("not the common discriminated vector", before)
	}
	payload := before.Events[0].UserTrigger
	image[1], targets[0] = triggerUnitInt(t, integer, 77), 1
	if n, _ := payload.Next[1].Integer(); n != 5 || payload.Targets[0] != 2 || payload.Relation != relation || payload.Read.State() != read.State() && payload.Read.Get(procedure.Address()) != procedure {
		t.Fatal("accepted images, attributes or immutable catalog provenance lost")
	}
	mark, err := s.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	if err := core.ScopeCheckConstraintEvents(s, true, nil); err != nil {
		t.Fatal(err)
	}
	if len(slot.ConstraintModes.Events) != 0 || len(before.Events) != 1 || before.Events[0].UserTrigger != payload {
		t.Fatal("consumption mutated predecessor backing")
	}
	deferredUnitAccept(t, s, read, relation, 2)
	if err := s.EndPLException(mark, true, errors.New("caught suffix")); err != nil {
		t.Fatal(err)
	}
	restored := core.ScopeIndexesSlot(s).ConstraintModes
	if len(restored.Events) != 1 || restored.Events[0].UserTrigger != payload || len(s.Stage.Rows[relation.Address()]) != 0 {
		t.Fatal("exception did not restore consumed eligibility and staged suffix")
	}
	if !restored.Deferred(definition.Parts().Constraint.OID, true, true) {
		t.Fatal("exception lost declared deferred mode")
	}
	deferredUnitAccept(t, s, read, relation, 3)
	current := core.ScopeIndexesSlot(s).ConstraintModes
	if len(current.Events) != 2 || current.Events[0].UserTrigger != payload || before.Events[0].UserTrigger != payload {
		t.Fatal("reappend overwrote restored predecessor")
	}
	if err := s.Finish(errors.New("unit cleanup")); err == nil {
		t.Fatal("cleanup lost primary")
	}
	if core.ScopeIndexesSlot(s) != nil || len(core.ScopeAfterTestFrames(s)) != 0 || s.ForeignActionAuth != nil {
		t.Fatal("finished owner retained event authority or payloads")
	}
}

func TestTriggerDeferredReentrantDrain(t *testing.T) {
	e, s, read, relation, procedure := triggerUnitFixture(t, `DECLARE caught integer:=0; BEGIN
	IF NEW.id=1 THEN
	 BEGIN TRUNCATE trigger_unit_table; EXCEPTION WHEN SQLSTATE '55006' THEN caught:=caught+1; END;
	 IF caught<>1 THEN RAISE EXCEPTION 'current event was hidden from pending guard'; END IF;
	 SET CONSTRAINTS ALL DEFERRED;
	 INSERT INTO trigger_unit_table VALUES(2,20);
	 SET CONSTRAINTS ALL IMMEDIATE;
	 SET CONSTRAINTS ALL DEFERRED;
	END IF;
	RETURN NEW; END`)
	definition := deferredUnitDefinition(t, read, relation, procedure)
	relation = triggerUnitInstall(t, e, relation, definition)
	triggerUnitAdmit(t, s, relation)
	deferredUnitAccept(t, s, read, relation, 1)
	before := core.ScopeIndexesSlot(s).ConstraintModes.Events[0].UserTrigger
	if err := core.ScopeCheckConstraintEvents(s, true, nil); err != nil {
		t.Fatal("real nested SET/caught DDL/tail completion", err)
	}
	modes := core.ScopeIndexesSlot(s).ConstraintModes
	if len(modes.Events) != 0 || !modes.AllSet || !modes.AllDeferred || len(s.Stage.Rows[relation.Address()]) != 2 {
		t.Fatal("replayed current event, lost tail, or overwrote nested SET", modes)
	}
	if n, _ := before.Next[1].Integer(); n != 10 {
		t.Fatal("body mutated retained event", n)
	}
	if s.HasPendingConstraintEvents(relation.Address()) || core.ScopeActiveSQLRoutineDepth(s) != 0 || s.ForeignActionAuth != nil {
		t.Fatal("firing marker/activation failed to unwind")
	}
	if len(e.Storage.Relations[relation.Address()].Versions) != 0 {
		t.Fatal("nested completion published early")
	}
	if err := s.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if len(e.Storage.Relations[relation.Address()].Versions) != 2 {
		t.Fatal("final owner did not publish exactly two real versions")
	}
}

func TestTriggerDeferredExecutableOwnership(t *testing.T) {
	e, s, read, relation, original := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := deferredUnitDefinition(t, read, relation, original)
	relation = triggerUnitInstall(t, e, relation, definition)
	triggerUnitAdmit(t, s, relation)
	deferredUnitAccept(t, s, read, relation, 1)
	payload := core.ScopeIndexesSlot(s).ConstraintModes.Events[0].UserTrigger
	integer, _ := read.LookupType(sqltype.Int4)
	late := triggerUnitProcedure(t, e, s, "deferred_late", 9500004, integer, proc.OrdinaryInvocation, "BEGIN RETURN 0; END")
	trigger, _ := read.LookupType(2279)
	current := triggerUnitProcedure(t, e, s, original.Name(), original.Address().ObjectID, trigger, proc.DMLTriggerInvocation,
		"BEGIN IF TG_NAME='renamed' AND NEW.v=10 THEN PERFORM 1/deferred_late(); END IF; RETURN NEW; END")
	replacement := *current
	replacement.ObjectRevision = original.Revision() + 1
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(current.Address(), &replacement)
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	p := definition.Parts()
	p.Name = "renamed"
	renamed, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, relation, renamed)
	if payload.Read.Get(late.Address()) != nil || payload.Read.Get(original.Address()) != original || payload.Definition.Name() != "deferred" {
		t.Fatal("fixture mutated the admitted definition/catalog")
	}
	mark, err := s.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	err = core.ScopeCheckConstraintEvents(s, true, nil)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22012" {
		t.Fatal("did not combine live trigger name/executable/catalog with accepted image", err)
	}
	if err := s.EndPLException(mark, true, err); err != nil {
		t.Fatal(err)
	}
	triggerUnitInstall(t, e, relation)
	if !s.HasPendingConstraintEvents(relation.Address()) {
		t.Fatal("DROP eagerly removed pending event")
	}
	if err := core.ScopeCheckConstraintEvents(s, true, nil); err != nil || s.HasPendingConstraintEvents(relation.Address()) {
		t.Fatal("dropped OID did not skip before stale function admission", err)
	}
	if err := s.Finish(errors.New("unit cleanup")); err == nil {
		t.Fatal("cleanup lost primary")
	}
}

func TestTriggerDeferredFastPath(t *testing.T) {
	for _, kind := range []string{"none", "irrelevant", "applicable"} {
		t.Run(kind, func(t *testing.T) {
			e, s, read, relation, procedure := triggerUnitFixtureContext(t, context.Background(), "BEGIN RETURN NEW; END")
			if kind != "none" {
				p := deferredUnitDefinition(t, read, relation, procedure).Parts()
				if kind == "irrelevant" {
					p.Events = stored.TriggerDelete
				}
				definition, err := stored.NewTrigger(p)
				if err != nil {
					t.Fatal(err)
				}
				relation = triggerUnitInstall(t, e, relation, definition)
			}
			triggerUnitAdmit(t, s, relation)
			probe := &afterAdmissionProbe{Context: s.Op.Ctx}
			s.Op.Ctx = probe
			deferredUnitAccept(t, s, read, relation, 1)
			if kind == "applicable" {
				if probe.admissions.Load() == 0 || core.ScopeIndexesSlot(s) == nil || len(core.ScopeIndexesSlot(s).ConstraintModes.Events) != 1 {
					t.Fatal("applicable control did not reach synchronized admission")
				}
			} else if probe.admissions.Load() != 0 || core.ScopeIndexesSlot(s) != nil || len(core.ScopeAfterTestFrames(s)) != 0 || !core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Owner)).Zero() {
				t.Fatal("irrelevant write synchronized or enlisted trigger/event state")
			}
			if err := s.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if core.ScopeIndexesSlot(s) != nil || s.Owner.State != session.TxIdle {
				t.Fatal("fast path retained transaction state")
			}
		})
	}
}
