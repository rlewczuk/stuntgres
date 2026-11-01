package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
)

// Inspect the actual producer-owned queue and accepted segments, not SQL audit
// output. Public direct/prepared values and firing are covered by Grouping.
func TestTriggerTransitionDeclaredEvents(t *testing.T) {
	insertEvents := []stored.TriggerEvent{stored.TriggerUpdate, stored.TriggerInsert}
	mergeEvents := []stored.TriggerEvent{stored.TriggerDelete, stored.TriggerUpdate, stored.TriggerInsert}
	for _, tc := range []struct {
		name, command string
		events        []stored.TriggerEvent
		counts        map[stored.TriggerEvent][2]int
	}{
		{"conflict-mixed", "INSERT INTO transition_producer VALUES(1,11),(3,30) ON CONFLICT(id) DO UPDATE SET v=excluded.v", insertEvents, map[stored.TriggerEvent][2]int{stored.TriggerInsert: {0, 1}, stored.TriggerUpdate: {1, 1}}},
		{"conflict-empty-update", "INSERT INTO transition_producer VALUES(3,30) ON CONFLICT(id) DO UPDATE SET v=excluded.v", insertEvents, map[stored.TriggerEvent][2]int{stored.TriggerInsert: {0, 1}, stored.TriggerUpdate: {0, 0}}},
		{"merge-mixed", "MERGE INTO transition_producer t USING(VALUES(1,11),(2,0),(3,30)) s(id,v) ON t.id=s.id WHEN MATCHED AND s.v=0 THEN DELETE WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)", mergeEvents, map[stored.TriggerEvent][2]int{stored.TriggerInsert: {0, 1}, stored.TriggerUpdate: {1, 1}, stored.TriggerDelete: {1, 0}}},
		{"merge-empty", "MERGE INTO transition_producer t USING(SELECT 1 AS id,11 AS v WHERE false) s ON t.id=s.id WHEN MATCHED AND s.v=0 THEN DELETE WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)", mergeEvents, map[stored.TriggerEvent][2]int{stored.TriggerInsert: {0, 0}, stored.TriggerUpdate: {0, 0}, stored.TriggerDelete: {0, 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			for _, statement := range []string{
				"CREATE TABLE transition_producer(id integer PRIMARY KEY,v integer)",
				"INSERT INTO transition_producer VALUES(1,10),(2,20)",
				"CREATE FUNCTION transition_producer_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
				"CREATE TRIGGER i AFTER INSERT ON transition_producer REFERENCING NEW TABLE x EXECUTE FUNCTION transition_producer_body()",
				"CREATE TRIGGER u AFTER UPDATE ON transition_producer REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION transition_producer_body()",
				"CREATE TRIGGER d AFTER DELETE ON transition_producer REFERENCING OLD TABLE y EXECUTE FUNCTION transition_producer_body()",
			} {
				enginetest.SessionSQL(t, session, statement)
			}
			op, err := session.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("owner fixture rollback"))
			result, err := scope.Execute(enginetest.Statement(t, tc.command), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows != nil {
				defer result.Rows.Close()
			}
			command, _ := core.ScopeEffectTestIDs(scope)
			frame := core.ScopeAfterTestFrames(scope)[command]
			if !slices.Equal(frame.StatementEvents, tc.events) || frame.Qualified != len(tc.events) || frame.Statements != len(tc.events) || frame.Rows != 0 || len(frame.Segments) != len(tc.events) {
				t.Fatal("producer omitted, reordered or duplicated declared event ownership", frame)
			}
			for i, event := range frame.StatementEvents {
				if event == stored.TriggerUpdate && !slices.Equal(frame.StatementTargets[i], []int32{2}) {
					t.Fatal("alternate UPDATE lost stable target attributes", frame.StatementTargets[i])
				}
				matched := false
				for _, segment := range frame.Segments {
					if segment.Event != event {
						continue
					}
					matched = true
					if segment.Identity != frame.StatementSegments[i] || len(segment.Schema) != 2 || [2]int{len(segment.Old), len(segment.New)} != tc.counts[event] {
						t.Fatal("declared event lost typed accepted-image provenance", event, segment)
					}
				}
				if !matched {
					t.Fatal("statement event has no current typed segment", event)
				}
			}
		})
	}
}
