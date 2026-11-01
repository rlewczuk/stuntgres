package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestTriggerTransitionHierarchyOwnership(t *testing.T) {
	for _, physicalRows := range []bool{false, true} {
		t.Run(map[bool]string{false: "logical-only", true: "physical-rows"}[physicalRows], func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			for _, query := range []string{
				"CREATE TABLE p(id integer,v integer)", "CREATE TABLE c(extra text) INHERITS(p)",
				"INSERT INTO p VALUES(1,10)", "INSERT INTO c VALUES(2,20,'extra')",
				"CREATE FUNCTION tt_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
				"CREATE TRIGGER s AFTER UPDATE ON p REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION tt_body()",
				"CREATE TRIGGER s AFTER UPDATE ON c REFERENCING OLD TABLE y EXECUTE FUNCTION tt_body()",
			} {
				enginetest.SessionSQL(t, session, query)
			}
			if physicalRows {
				enginetest.SessionSQL(t, session, "CREATE TRIGGER r AFTER UPDATE ON p FOR EACH ROW EXECUTE FUNCTION tt_body()")
				enginetest.SessionSQL(t, session, "CREATE TRIGGER r AFTER UPDATE ON c FOR EACH ROW EXECUTE FUNCTION tt_body()")
			}
			scope := foreignActionUnitScope(t, e, session, "UPDATE p SET v=v+1")
			frame := core.ScopeAfterTestFrames(scope)[0]
			if len(frame.Segments) != 1 || frame.Statements != 1 {
				t.Fatal("logical hierarchy group missing or duplicated", frame)
			}
			segment := frame.Segments[0]
			if len(segment.Schema) != 2 || len(segment.Old) != 2 || len(segment.New) != 2 {
				t.Fatal("hierarchy images lost projection or multiplicity", segment)
			}
			seen := map[int64]bool{}
			for i, old := range segment.Old {
				id, _ := old[0].Integer()
				before, _ := old[1].Integer()
				after, _ := segment.New[i][1].Integer()
				if seen[id] || len(old) != 2 || len(segment.New[i]) != 2 || after != before+1 {
					t.Fatal("accepted logical pair lost or duplicated", segment)
				}
				seen[id] = true
			}
			if !seen[1] || !seen[2] {
				t.Fatal("missing accepted ancestor or child image", seen)
			}
			if !physicalRows && frame.Rows != 0 {
				t.Fatal("capture-only logical target synthesized ROW events", frame)
			}
			if physicalRows {
				if frame.Rows != 2 || frame.RowRelations[0] == frame.RowRelations[1] {
					t.Fatal("physical ROW attribution collapsed into named target", frame)
				}
				if &frame.Old[0][0] != &segment.Old[0][0] || &frame.New[0][0] != &segment.New[0][0] {
					t.Fatal("same-ref accepted image copied for scalar consumer")
				}
			}
		})
	}
	t.Run("movement-checkpoint", func(t *testing.T) {
		e := engine.New()
		session := enginetest.StartSession(t, e, nil)
		for _, query := range []string{
			"CREATE TABLE p(id integer,v integer) PARTITION BY RANGE(id)", "CREATE TABLE lo PARTITION OF p FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE hi(v integer,id integer)", "ALTER TABLE p ATTACH PARTITION hi FOR VALUES FROM(10) TO(20)",
			"INSERT INTO p VALUES(1,10),(2,20)",
			"CREATE FUNCTION tt_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
			"CREATE FUNCTION tt_insert() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.id=12 THEN PERFORM 1/0; END IF; NEW.v:=NEW.v+1; RETURN NEW; END$$",
			"CREATE TRIGGER b BEFORE INSERT ON hi FOR EACH ROW EXECUTE FUNCTION tt_insert()",
			"CREATE TRIGGER s AFTER UPDATE ON p REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION tt_body()",
			"CREATE TRIGGER r AFTER INSERT OR UPDATE OR DELETE ON p FOR EACH ROW EXECUTE FUNCTION tt_body()",
		} {
			enginetest.SessionSQL(t, session, query)
		}
		scope := foreignActionUnitScope(t, e, session, "UPDATE p SET id=11 WHERE id=1")
		frame := core.ScopeAfterTestFrames(scope)[0]
		if len(frame.Segments) != 1 || frame.Rows != 2 || frame.Statements != 1 {
			t.Fatal("movement fabricated logical ROW or root I/D events", frame)
		}
		segment := frame.Segments[0]
		if segment.Event != stored.TriggerUpdate || len(segment.Old) != 1 || len(segment.New) != 1 || frame.RowRelations[0] == frame.RowRelations[1] {
			t.Fatal("movement halves have different owners or physical events", frame)
		}
		oldID, _ := segment.Old[0][0].Integer()
		newID, _ := segment.New[0][0].Integer()
		newValue, _ := segment.New[0][1].Integer()
		if oldID != 1 || newID != 11 || newValue != 11 || &segment.New[0][0] == &frame.New[1][0] {
			t.Fatal("movement lost named projection or borrowed reordered leaf backing", segment)
		}
		mark, err := scope.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		_, failure := scope.Execute(enginetest.Statement(t, "UPDATE p SET id=12 WHERE id=2"), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(failure, &diagnostic) || diagnostic.Code != "22012" {
			t.Fatal("destination failure was not reached", failure)
		}
		suffix := core.ScopeAfterTestFrames(scope)[0]
		if len(suffix.Segments) != 1 || len(suffix.Segments[0].Old) != 2 || len(suffix.Segments[0].New) != 1 || suffix.Rows != frame.Rows+1 {
			t.Fatal("failed destination did not retain accepted source OLD before rollback", suffix)
		}
		if err := scope.EndPLException(mark, true, failure); err != nil {
			t.Fatal(err)
		}
		restored := core.ScopeAfterTestFrames(scope)[0]
		if len(restored.Segments) != 1 || len(restored.Segments[0].Old) != 1 || len(restored.Segments[0].New) != 1 || restored.Rows != frame.Rows {
			t.Fatal("failed moved suffix survived checkpoint", restored)
		}
		if &restored.Segments[0].Old[0][0] != &segment.Old[0][0] || &restored.Segments[0].New[0][0] != &segment.New[0][0] {
			t.Fatal("checkpoint copied accepted immutable movement payload")
		}
	})
}
