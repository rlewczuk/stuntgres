package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func transitionRuntimeFixture(t *testing.T, rowDefinitions int) (*core.Engine, *core.StatementScope, catalog.Read, *catalog.Relation, ids.ObjectRef) {
	t.Helper()
	e, s, read, relation, procedure := triggerUnitFixture(t, "BEGIN PERFORM count(*) FROM x; RETURN NULL; END")
	base := triggerUnitDefinition(t, read, relation, procedure, "s", stored.TriggerInsert).Parts()
	base.Timing, base.Level, base.NewTable = stored.TriggerAfter, stored.TriggerStatement, "x"
	statement, err := stored.NewTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	definitions := []stored.Trigger{statement}
	for i := range rowDefinitions {
		p := base
		p.OID += ids.OID(i + 1)
		p.Name, p.Level = string(rune('a'+i)), stored.TriggerRow
		definition, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		definitions = append(definitions, definition)
	}
	relation = triggerUnitInstall(t, e, relation, definitions...)
	triggerUnitAdmit(t, s, relation)
	return e, s, read, relation, read.Owner().Domain.Ref(read.Epoch(), relation.Address())
}

func transitionRuntimeRow(t *testing.T, read catalog.Read, id, value int64) []sqltype.Value {
	t.Helper()
	integer, _ := read.LookupType(sqltype.Int4)
	return []sqltype.Value{triggerUnitInt(t, integer, id), triggerUnitInt(t, integer, value)}
}

func transitionRuntimeQueue(t *testing.T, s *core.StatementScope, ref ids.ObjectRef, row []sqltype.Value) {
	t.Helper()
	if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err != nil {
		t.Fatal(err)
	}
}

func transitionRuntimeExpose(t *testing.T, s *core.StatementScope, ref ids.ObjectRef) {
	t.Helper()
	if err := s.WriteHost().CompleteAfterStatement(ref, stored.TriggerInsert); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteHost().ExposeAfterStatement(ref, stored.TriggerInsert); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerTransitionCaptureOwnership(t *testing.T) {
	_, s, read, _, ref := transitionRuntimeFixture(t, 2)
	if err := s.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	row := transitionRuntimeRow(t, read, 1, 5)
	transitionRuntimeQueue(t, s, ref, row)
	transitionRuntimeQueue(t, s, ref, row)
	row[1] = transitionRuntimeRow(t, read, 0, 77)[1]
	frame := core.ScopeAfterTestFrames(s)[0]
	if len(frame.Segments) != 1 || frame.Rows != 4 || frame.Statements != 1 {
		t.Fatal(frame)
	}
	segment := frame.Segments[0]
	if len(segment.New) != 2 || len(segment.Old) != 0 || len(segment.Schema) != 2 {
		t.Fatal(segment)
	}
	for i, image := range segment.New {
		if value, _ := image[1].Integer(); value != 5 {
			t.Fatal("borrowed producer scratch", value)
		}
		if &image[0] != &frame.New[i*2][0] || &image[0] != &frame.New[i*2+1][0] {
			t.Fatal("copied accepted payload per consumer")
		}
	}
	for _, identity := range frame.RowSegments {
		if identity != segment.Identity {
			t.Fatal("ROW events do not share one accepted bag")
		}
	}
	if frame.StatementSegments[0] != segment.Identity {
		t.Fatal("STATEMENT event has a different bag")
	}
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	if !core.ScopeAfterTestFrames(s)[0].Segments[0].Frozen {
		t.Fatal("invocation failed to expose set")
	}
	for _, oldOnly := range []bool{true, false} {
		t.Run(map[bool]string{true: "old-only-update", false: "new-only-update"}[oldOnly], func(t *testing.T) {
			e, scope, read, relation, procedure := triggerUnitFixture(t, "BEGIN RETURN NULL; END")
			p := triggerUnitDefinition(t, read, relation, procedure, "s", stored.TriggerUpdate).Parts()
			p.Timing, p.Level = stored.TriggerAfter, stored.TriggerStatement
			if oldOnly {
				p.OldTable = "x"
			} else {
				p.NewTable = "x"
			}
			definition, err := stored.NewTrigger(p)
			if err != nil {
				t.Fatal(err)
			}
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, scope, relation)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			old, next := transitionRuntimeRow(t, read, 1, 5), transitionRuntimeRow(t, read, 1, 6)
			if err := scope.WriteHost().AfterStatement(ref, stored.TriggerUpdate, nil); err != nil {
				t.Fatal(err)
			}
			if err := scope.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, Old: old, New: next}); err != nil {
				t.Fatal(err)
			}
			frame := core.ScopeAfterTestFrames(scope)[0]
			segment := frame.Segments[0]
			if frame.Rows != 0 || frame.Statements != 1 {
				t.Fatal("statement-only capture invented ROW events", frame)
			}
			image, unused, borrowed := segment.New, segment.Old, next
			if oldOnly {
				image, unused, borrowed = segment.Old, segment.New, old
			}
			if len(image) != 1 || unused != nil || &image[0][0] == &borrowed[0] {
				t.Fatal("one-sided ownership", segment)
			}
			old[1], next[1] = sqltype.Value{}, sqltype.Value{}
			want := int64(6)
			if oldOnly {
				want = 5
			}
			if got, _ := image[0][1].Integer(); got != want {
				t.Fatal("one-sided set retained producer scratch", got)
			}
		})
	}
	for _, side := range []string{"OLD", "NEW"} {
		for _, condition := range []string{"false", "NULL", "true"} {
			t.Run(side+"-scalar-"+condition, func(t *testing.T) {
				e := engine.New()
				session := enginetest.StartSession(t, e, nil)
				for _, query := range []string{
					"CREATE TABLE t(id integer,v integer)", "INSERT INTO t VALUES(1,5)",
					"CREATE FUNCTION tt_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
					"CREATE TRIGGER s AFTER UPDATE ON t REFERENCING " + side + " TABLE x EXECUTE FUNCTION tt_body()",
					"CREATE TRIGGER r AFTER UPDATE ON t FOR EACH ROW WHEN(" + condition + ") EXECUTE FUNCTION tt_body()",
				} {
					enginetest.SessionSQL(t, session, query)
				}
				scope := foreignActionUnitScope(t, e, session, "UPDATE t SET v=6")
				frame := core.ScopeAfterTestFrames(scope)[0]
				segment := frame.Segments[0]
				image, unused := segment.New, segment.Old
				if side == "OLD" {
					image, unused = segment.Old, segment.New
				}
				if len(image) != 1 || unused != nil {
					t.Fatal("scalar eligibility changed one-sided set demand", segment)
				}
				if condition != "true" {
					if frame.Rows != 0 {
						t.Fatal("false/NULL ROW WHEN retained scalar images", frame)
					}
					return
				}
				if frame.Rows != 1 || len(frame.Old[0]) != 2 || len(frame.New[0]) != 2 {
					t.Fatal("qualifying scalar consumer lost counterpart", frame)
				}
				shared := frame.New[0]
				if side == "OLD" {
					shared = frame.Old[0]
				}
				if &image[0][0] != &shared[0] {
					t.Fatal("scalar consumer recopied demanded transition backing")
				}
			})
		}
	}
}

func TestTriggerTransitionCheckpoint(t *testing.T) {
	_, s, read, _, ref := transitionRuntimeFixture(t, 1)
	if err := s.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 1, 5))
	before := core.ScopeAfterTestFrames(s)[0]
	mark, err := s.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 2, 6))
	transitionRuntimeExpose(t, s, ref)
	if !core.ScopeAfterTestFrames(s)[0].Segments[0].Frozen {
		t.Fatal("live branch was not exposed")
	}
	if err := s.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 3, 7))
	if err := s.EndPLException(mark, true, errors.New("caught suffix")); err != nil {
		t.Fatal(err)
	}
	restored := core.ScopeAfterTestFrames(s)[0]
	segment := restored.Segments[0]
	if restored.Rows != 1 || restored.Statements != 1 || restored.RowCursor != 0 || restored.StatementCursor != 0 || segment.Frozen || len(segment.New) != 1 {
		t.Fatal("checkpoint retained suffix/exposure/progress", restored)
	}
	if segment.Identity == before.Segments[0].Identity || restored.RowSegments[0] != segment.Identity || restored.StatementSegments[0] != segment.Identity {
		t.Fatal("checkpoint maps/events did not restore one detached segment")
	}
	if &segment.New[0][0] != &before.Segments[0].New[0][0] {
		t.Fatal("checkpoint copied immutable payload")
	}
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 4, 8))
	if len(segment.New) != 1 || len(core.ScopeAfterTestFrames(s)[0].Segments[0].New) != 2 {
		t.Fatal("append modified retained checkpoint vector")
	}
	if err := s.Finish(errors.New("test cleanup")); err == nil {
		t.Fatal("missing cleanup failure")
	}
	if len(core.ScopeAfterTestFrames(s)) != 0 {
		t.Fatal("terminal cleanup retained transition images")
	}
}

func TestTriggerTransitionExposure(t *testing.T) {
	_, s, read, _, ref := transitionRuntimeFixture(t, 1)
	if err := s.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 1, 5))
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 2, 6))
	transitionRuntimeExpose(t, s, ref)
	first := core.ScopeAfterTestFrames(s)[0]
	if !first.Segments[0].Frozen || first.Skipped != 1 {
		t.Fatal("first exposed group not consumed", first)
	}
	if err := s.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	zero := core.ScopeAfterTestFrames(s)[0]
	if zero.Statements != 1 || zero.Segments[0].Identity != first.Segments[0].Identity {
		t.Fatal("zero-match registration rotated or replaced exposed group", zero)
	}
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 3, 7))
	second := core.ScopeAfterTestFrames(s)[0]
	if second.Statements != 2 || len(second.Segments[0].New) != 1 || second.Segments[0].Identity == first.Segments[0].Identity {
		t.Fatal("next accepted row did not rotate statement group", second)
	}
	if second.StatementSegments[0] != first.Segments[0].Identity || second.StatementSegments[1] != second.Segments[0].Identity || len(first.Segments[0].New) != 2 {
		t.Fatal("rotation rewrote frozen provenance")
	}
	transitionRuntimeExpose(t, s, ref)
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	finished := core.ScopeAfterTestFrames(s)[0]
	if finished.RowCursor != 3 || finished.StatementCursor != 2 || finished.Skipped != 2 {
		t.Fatal("final drain replayed/lost exposed groups", finished)
	}
	t.Run("paired-update", func(t *testing.T) {
		e, scope, read, relation, procedure := triggerUnitFixture(t, "BEGIN PERFORM count(*) FROM x; RETURN NULL; END")
		p := triggerUnitDefinition(t, read, relation, procedure, "s", stored.TriggerUpdate).Parts()
		p.Timing, p.Level, p.OldTable, p.NewTable = stored.TriggerAfter, stored.TriggerStatement, "old_x", "x"
		statement, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		p.Name, p.Level, p.OID = "r", stored.TriggerRow, p.OID+1
		row, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		relation = triggerUnitInstall(t, e, relation, statement, row)
		triggerUnitAdmit(t, scope, relation)
		ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
		pair := func(id, before, after int64) {
			t.Helper()
			if err := scope.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, Old: transitionRuntimeRow(t, read, id, before), New: transitionRuntimeRow(t, read, id, after)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := scope.WriteHost().AfterStatement(ref, stored.TriggerUpdate, nil); err != nil {
			t.Fatal(err)
		}
		pair(1, 5, 6)
		if err := scope.WriteHost().CompleteAfterStatement(ref, stored.TriggerUpdate); err != nil {
			t.Fatal(err)
		}
		if err := scope.WriteHost().ExposeAfterStatement(ref, stored.TriggerUpdate); err != nil {
			t.Fatal(err)
		}
		first := core.ScopeAfterTestFrames(scope)[0].Segments[0]
		if !first.Frozen || len(first.Old) != 1 || len(first.New) != 1 {
			t.Fatal("UPDATE did not expose paired sets", first)
		}
		if err := scope.WriteHost().AfterStatement(ref, stored.TriggerUpdate, nil); err != nil {
			t.Fatal(err)
		}
		pair(2, 7, 8)
		next := core.ScopeAfterTestFrames(scope)[0].Segments[0]
		if next.Identity == first.Identity || len(next.Old) != 1 || len(next.New) != 1 || &next.Old[0][0] == &first.Old[0][0] || &next.New[0][0] == &first.New[0][0] {
			t.Fatal("UPDATE images rotated independently or reused frozen payload", next)
		}
	})
}
