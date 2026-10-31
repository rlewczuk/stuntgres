package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTriggerAfterDefinitionKinds(t *testing.T) {
	_, _, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	base := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerInsert).Parts()
	for _, timing := range []stored.TriggerTiming{0, stored.TriggerBefore, stored.TriggerAfter, 99} {
		for _, level := range []stored.TriggerLevel{0, stored.TriggerRow, stored.TriggerStatement, 99} {
			for _, event := range []stored.TriggerEvent{stored.TriggerInsert, stored.TriggerTruncate} {
				p := base
				p.Timing, p.Level, p.Events = timing, level, event
				valid := (timing == stored.TriggerBefore || timing == stored.TriggerAfter) && (level == stored.TriggerStatement || level == stored.TriggerRow && event != stored.TriggerTruncate)
				definition, err := stored.NewTrigger(p)
				if (err == nil) != valid {
					t.Fatalf("kind %d/%d/%d valid=%t err=%v", timing, level, event, valid, err)
				}
				if err == nil && (definition.Parts().Timing != timing || definition.Parts().Level != level || definition.Parts().Events != event) {
					t.Fatal("constructor changed discriminants")
				}
			}
		}
	}
	p := base
	p.Timing, p.Level = stored.TriggerAfter, stored.TriggerStatement
	p.Parent = ids.ObjectAddress{ClassID: 2620, ObjectID: 1}
	if _, err := stored.NewTrigger(p); err == nil {
		t.Fatal("statement partition clone accepted")
	}
}
func TestTriggerAfterRowOwnership(t *testing.T) {
	e, s, read, relation, d := triggerUnitFixture(t, "BEGIN NEW.v:=999; RETURN NEW; END")
	base := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerInsert).Parts()
	base.Timing = stored.TriggerAfter
	a, err := stored.NewTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Name = "z"
	base.OID++
	z, err := stored.NewTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, relation, a, z)
	triggerUnitAdmit(t, s, relation)
	integer, _ := read.LookupType(sqltype.Int4)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err != nil {
		t.Fatal(err)
	}
	row[1] = triggerUnitInt(t, integer, 77)
	before := core.ScopeAfterTestFrames(s)[0]
	if before.Rows != 2 {
		t.Fatal(before)
	}
	for _, image := range before.New {
		if n, _ := image[1].Integer(); n != 5 {
			t.Fatal("queue borrowed producer scratch", n)
		}
	}
	if &before.New[0][0] != &before.New[1][0] {
		t.Fatal("same accepted tuple copied per definition")
	}
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	after := core.ScopeAfterTestFrames(s)[0]
	if after.RowCursor != 2 {
		t.Fatal(after)
	}
	for _, image := range after.New {
		if n, _ := image[1].Integer(); n != 5 {
			t.Fatal("private trigger mutation escaped frame", n)
		}
	}
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	if core.ScopeAfterTestFrames(s)[0].RowCursor != 2 {
		t.Fatal("drain replayed events")
	}
}
func TestTriggerAfterQueueOwnership(t *testing.T) {
	e, s, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	base := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerInsert).Parts()
	base.Timing = stored.TriggerAfter
	r, err := stored.NewTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Name = "s"
	base.OID++
	base.Level = stored.TriggerStatement
	statement, err := stored.NewTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, relation, r, statement)
	triggerUnitAdmit(t, s, relation)
	integer, _ := read.LookupType(sqltype.Int4)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	queue := func() {
		t.Helper()
		if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err != nil {
			t.Fatal(err)
		}
		if err := s.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
			t.Fatal(err)
		}
	}
	queue()
	previous := s.Command
	s.Command = dml.Command{}
	queue()
	s.Command = previous
	if f := core.ScopeAfterTestFrames(s)[0]; f.Rows != 2 || f.Statements != 1 {
		t.Fatal("lexical swap lost owner or statement dedup", f)
	}
	if err := s.WriteHost().CompleteAfterStatement(ref, stored.TriggerInsert); err != nil {
		t.Fatal(err)
	}
	if f := core.ScopeAfterTestFrames(s)[0]; f.Qualified != 1 || f.Skipped != 0 {
		t.Fatal("producer failed to retain qualification", f)
	}
	mark, err := s.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	finish, err := s.BeginRoutineCommand()
	if err != nil {
		t.Fatal(err)
	}
	nested, serial := core.ScopeEffectTestIDs(s)
	queue()
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	if f := core.ScopeAfterTestFrames(s)[0]; f.RowCursor != 0 || f.StatementCursor != 0 {
		t.Fatal("nested completion drained parent", f)
	}
	if _, exists := core.ScopeAfterTestFrames(s)[nested]; exists {
		t.Fatal("nested frame retained")
	}
	queue()
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	if err := s.EndPLException(mark, true, errors.New("caught suffix")); err != nil {
		t.Fatal(err)
	}
	if f := core.ScopeAfterTestFrames(s)[0]; f.Rows != 2 || f.Statements != 1 || f.RowCursor != 0 || f.Qualified != 1 || f.Skipped != 0 {
		t.Fatal("checkpoint failed to restore events, qualification and progress", f)
	}
	_, now := core.ScopeEffectTestIDs(s)
	if now < serial {
		t.Fatal("effect IDs rewound")
	}
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(errors.New("test cleanup")); err == nil {
		t.Fatal("missing cleanup veto")
	}
	if len(core.ScopeAfterTestFrames(s)) != 0 {
		t.Fatal("Finish retained queue images")
	}
}
func TestTriggerAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e, s, read, relation, d := triggerUnitFixtureContext(t, ctx, "BEGIN RETURN NEW; END")
	p := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerInsert).Parts()
	p.Timing = stored.TriggerAfter
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, relation, definition)
	triggerUnitAdmit(t, s, relation)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	integer, _ := read.LookupType(sqltype.Int4)
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	dml.BeginInsertReturning(s.WriteHost(), analyze.Binding{Ref: ref, Relation: relation, Targets: []int{0, 1}})
	if s.InsertReturning == nil {
		t.Fatal("missing incremental INSERT stage")
	}
	defer s.InsertReturning.Rows.Close()
	if _, err := dml.BindUniqueWrite(s.WriteHost(), ref); err != nil {
		t.Fatal(err)
	}
	accepted, stale, err := dml.AppendAcceptedInsert(s.WriteHost(), ref, &index.WriteCandidate{Values: row})
	if err != nil || !accepted || stale {
		t.Fatal(accepted, stale, err)
	}
	if len(s.Stage.Rows[relation.Address()]) != 1 || core.ScopeAfterTestFrames(s)[0].Rows != 1 {
		t.Fatal("cancellation witness lacks accepted row and pending AFTER event")
	}
	cancel()
	if err := s.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation boundary", err)
	}
	if len(e.Storage.Relations[relation.Address()].Versions) != 0 || len(core.ScopeAfterTestFrames(s)) != 0 {
		t.Fatal("canceled AFTER published or retained resources")
	}
	if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err == nil {
		t.Fatal("finished queue admitted event")
	}
}

func TestTriggerAfterExecutableOwnership(t *testing.T) {
	e, s, read, relation, original := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	p := triggerUnitDefinition(t, read, relation, original, "queued", stored.TriggerInsert).Parts()
	p.Timing = stored.TriggerAfter
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, relation, definition)
	triggerUnitAdmit(t, s, relation)
	integer, _ := read.LookupType(sqltype.Int4)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err != nil {
		t.Fatal(err)
	}
	// The event owns the admitted definition, relation and row image, not an
	// executable snapshot. Both this helper and the replacement are newer.
	late := triggerUnitProcedure(t, e, s, "trigger_unit_late", 9500004, integer, proc.OrdinaryInvocation, "BEGIN RETURN 0; END")
	trigger, _ := read.LookupType(2279)
	current := triggerUnitProcedure(t, e, s, original.Name(), original.Address().ObjectID, trigger, proc.DMLTriggerInvocation,
		"BEGIN IF TG_NAME='queued' AND NEW.v=5 THEN PERFORM 1/trigger_unit_late(); END IF; RETURN NEW; END")
	replacement := *current
	replacement.ObjectRevision = original.Revision() + 1
	current = &replacement
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(current.Address(), current)
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	if current == original || current.SQL == original.SQL || e.CatalogRead().Get(current.Address()) != current {
		t.Fatal("replacement did not install distinct immutable executable backing")
	}
	if read.Get(original.Address()) != original || read.Get(late.Address()) != nil {
		t.Fatal("replacement mutated the retained event catalog")
	}
	row[1] = triggerUnitInt(t, integer, 77)
	err = core.ScopeDrainAfterTest(s)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22012" {
		t.Fatal("firing did not combine current executable/catalog with queued trigger/tuple provenance", err)
	}
	frame := core.ScopeAfterTestFrames(s)[0]
	if frame.Rows != 1 || frame.RowCursor != 1 {
		t.Fatal("replacement changed queued event ownership", frame)
	}
	if n, _ := frame.New[0][1].Integer(); n != 5 {
		t.Fatal("replacement invocation changed retained tuple", n)
	}
	if err := s.Finish(errors.New("test cleanup")); err == nil {
		t.Fatal("missing cleanup veto")
	}
}

func TestTriggerAfterCatalogPublication(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			e, s, read, relation, d := triggerUnitFixture(t,
				"BEGIN CREATE OR REPLACE FUNCTION trigger_unit_replaced() RETURNS integer LANGUAGE plpgsql AS $body$BEGIN RETURN 8; END$body$; RETURN NEW; END")
			integer, _ := read.LookupType(sqltype.Int4)
			original := triggerUnitProcedure(t, e, s, "trigger_unit_replaced", 9500004, integer, proc.OrdinaryInvocation, "BEGIN RETURN 7; END")
			p := triggerUnitDefinition(t, read, relation, d, "replace", stored.TriggerInsert).Parts()
			p.Timing = stored.TriggerAfter
			definition, err := stored.NewTrigger(p)
			if err != nil {
				t.Fatal(err)
			}
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, s, relation)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
			if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err != nil {
				t.Fatal(err)
			}
			if err := core.ScopeDrainAfterTest(s); err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().Get(original.Address()) != original {
				t.Fatal("nested trigger utility published before outer completion")
			}
			if s.Catalog == nil {
				t.Fatal("nested trigger utility did not retain a statement-private journal")
			}
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(s.PendingCatalog())
			replacement, _ := private.Get(original.Address()).(*catalog.Procedure)
			e.Mu.Unlock()
			if replacement == nil || replacement == original || replacement.SQL == original.SQL || replacement.SQL.Source != "BEGIN RETURN 8; END" || replacement.Revision() <= original.Revision() {
				t.Fatal("private replacement lost its executable backing or revision", replacement)
			}
			var primary error
			if !commit {
				primary = errors.New("outer command rejected")
			}
			err = s.Finish(primary)
			if !errors.Is(err, primary) {
				t.Fatal("outer completion", err)
			}
			installed := e.CatalogRead().Get(original.Address())
			if commit {
				if installed != replacement {
					t.Fatal("successful outer completion did not publish its replacement")
				}
			} else if installed != original {
				t.Fatal("rejected outer completion published its replacement")
			}
		})
	}
}

func TestTriggerAfterTargetOwnership(t *testing.T) {
	t.Run("accepted_checkpoint_and_producer", func(t *testing.T) {
		e, s, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
		base := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerUpdate).Parts()
		base.Timing = stored.TriggerAfter
		var definitions []stored.Trigger
		for i, name := range []string{"a", "z", "s", "t"} {
			p := base
			p.Name, p.OID = name, base.OID+ids.OID(i)
			if i >= 2 {
				p.Level = stored.TriggerStatement
			}
			definition, err := stored.NewTrigger(p)
			if err != nil {
				t.Fatal(err)
			}
			definitions = append(definitions, definition)
		}
		relation = triggerUnitInstall(t, e, relation, definitions...)
		triggerUnitAdmit(t, s, relation)
		integer, _ := read.LookupType(sqltype.Int4)
		ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
		row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
		queue := func(targets []int32) {
			t.Helper()
			if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, Targets: targets, Old: row, New: row}); err != nil {
				t.Fatal(err)
			}
			if err := s.WriteHost().AfterStatement(ref, stored.TriggerUpdate, targets); err != nil {
				t.Fatal(err)
			}
			if err := s.WriteHost().CompleteAfterStatement(ref, stored.TriggerUpdate); err != nil {
				t.Fatal(err)
			}
		}
		defer core.ScopeDeferAfterProducersTest(s)()
		targets := []int32{relation.Attributes[1], relation.Attributes[0]}
		want := slices.Clone(targets)
		queue(targets)
		before := core.ScopeAfterTestFrames(s)[0]
		if before.Rows != 2 || before.Statements != 2 || len(before.ProducerTargets) != 2 {
			t.Fatal("missing accepted row, registration or producer", before)
		}
		for _, group := range [][][]int32{before.Targets, before.StatementTargets, before.ProducerTargets} {
			for _, retained := range group {
				if !slices.Equal(retained, want) || &retained[0] == &targets[0] {
					t.Fatal("queue did not own exact ordered stable targets", retained, want)
				}
				if &retained[0] != &group[0][0] {
					t.Fatal("same queue call copied targets per definition")
				}
			}
		}
		if &before.StatementTargets[0][0] != &before.ProducerTargets[0][0] {
			t.Fatal("producer copied immutable registration payload")
		}
		targets[0], targets[1] = 99, 100
		for _, group := range [][][]int32{before.Targets, before.StatementTargets, before.ProducerTargets} {
			for _, retained := range group {
				if !slices.Equal(retained, want) {
					t.Fatal("caller mutation reached retained target payload", retained)
				}
			}
		}
		mark, err := s.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		queue([]int32{relation.Attributes[0]})
		if err := core.ScopeDrainAfterTest(s); err != nil {
			t.Fatal(err)
		}
		if live := core.ScopeAfterTestFrames(s)[0]; live.Rows != 4 || live.RowCursor != 4 || live.StatementCursor != 2 {
			t.Fatal("suffix did not advance independent queue progress", live)
		}
		if err := s.EndPLException(mark, true, errors.New("discard queued suffix")); err != nil {
			t.Fatal(err)
		}
		restored := core.ScopeAfterTestFrames(s)[0]
		if restored.Rows != 2 || restored.Statements != 2 || restored.RowCursor != 0 || restored.StatementCursor != 0 || restored.Qualified != 0 || restored.Skipped != 0 || len(restored.ProducerTargets) != 2 {
			t.Fatal("checkpoint did not restore event slices, producers and cursors", restored)
		}
		if restored.RowBacking == before.RowBacking || restored.StatementBacking == before.StatementBacking {
			t.Fatal("checkpoint borrowed mutable event backing")
		}
		for i, group := range [][][]int32{restored.Targets, restored.StatementTargets, restored.ProducerTargets} {
			original := [][][]int32{before.Targets, before.StatementTargets, before.ProducerTargets}[i]
			for j, retained := range group {
				if !slices.Equal(retained, want) || &retained[0] != &original[j][0] {
					t.Fatal("checkpoint copied or changed immutable target payload", retained)
				}
			}
		}
		if err := core.ScopeDrainAfterTest(s); err != nil {
			t.Fatal(err)
		}
		registered := core.ScopeAfterTestFrames(s)[0]
		for i, retained := range registered.StatementTargets {
			if !slices.Equal(retained, want) || &retained[0] != &before.ProducerTargets[i][0] {
				t.Fatal("producer replay failed to retain immutable targets", retained)
			}
		}
	})
	for _, kind := range []string{"filtered", "when_rejected", "nil", "empty"} {
		t.Run(kind, func(t *testing.T) {
			e, s, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
			p := triggerUnitDefinition(t, read, relation, d, "queued", stored.TriggerUpdate).Parts()
			p.Timing = stored.TriggerAfter
			if kind == "filtered" {
				p.UpdateColumns = []int32{relation.Attributes[0]}
			}
			definition, err := stored.NewTrigger(p)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "when_rejected" {
				definition = infoTriggersWhen(t, e, s, read, relation, definition)
			}
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, s, relation)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			integer, _ := read.LookupType(sqltype.Int4)
			row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, -1)}
			targets := []int32{relation.Attributes[1]}
			if kind == "nil" {
				targets = nil
			} else if kind == "empty" {
				targets = []int32{}
			}
			if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, Targets: targets, Old: row, New: row}); err != nil {
				t.Fatal(err)
			}
			frame := core.ScopeAfterTestFrames(s)[0]
			if kind == "filtered" || kind == "when_rejected" {
				if frame.Rows != 0 || len(frame.Targets) != 0 || len(frame.New) != 0 || len(frame.Old) != 0 {
					t.Fatal("rejected queue path retained event payload", frame)
				}
			} else if frame.Rows != 1 || len(frame.Targets) != 1 || len(frame.Targets[0]) != 0 {
				t.Fatal("empty targets gained attribute membership", frame)
			}
		})
	}
}
