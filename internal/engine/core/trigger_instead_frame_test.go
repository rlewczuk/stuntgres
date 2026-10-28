package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTriggerInsteadFrameOwnership(t *testing.T) {
	for _, phase := range []string{"success", "null", "error", "delete", "partial", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			body := "BEGIN PERFORM 1; NEW.v:=NEW.v+1; RETURN NEW; END"
			switch phase {
			case "null":
				body = "BEGIN PERFORM 1; RETURN NULL; END"
			case "error":
				body = "BEGIN PERFORM 1/0; RETURN NEW; END"
			case "delete":
				body = "BEGIN PERFORM 1; OLD.v:=99; RETURN OLD; END"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e, s, read, relation, routine := insteadUnitFixture(t, ctx, body)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			event := stored.TriggerUpdate
			if phase == "delete" {
				event = stored.TriggerDelete
			}
			insteadUnitAdmit(t, s, relation, ref, event)
			integer, _ := read.LookupType(sqltype.Int4)
			old := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
			next := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 7)}
			request := dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, Old: old, New: next, Instead: true}
			if phase == "delete" {
				request.Event, request.New = dml.BeforeDelete, nil
			}
			original := core.ScopeSQLRoutine(s)
			program := routine.SQL.PL
			changed := *routine
			changed.ObjectRevision = routine.Revision() + 1
			changed.Configuration = catalog.NewRoutineConfiguration([]string{"application_name=instead-private"})
			if phase == "partial" {
				changed.Configuration = catalog.NewRoutineConfiguration([]string{"application_name=instead-private", "statement_timeout=not-a-number"})
			}
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			state.SetDescriptor(changed.Address(), &changed)
			e.Catalog.PublishLocked(state)
			e.Mu.Unlock()
			if phase == "cancel" {
				cancel()
			}
			result, err := s.WriteHost().BeforeRow(request)
			if (phase == "error" || phase == "partial" || phase == "cancel") != (err != nil) {
				t.Fatal("unexpected invocation exit", err)
			}
			if err == nil {
				if result.Proceed != (phase != "null") {
					t.Fatal("NULL did not terminate chain")
				}
				if phase == "success" {
					value, _ := result.Row[1].Integer()
					if value != 8 {
						t.Fatal("replacement lost", value)
					}
				}
				if phase == "delete" && result.Row != nil {
					t.Fatal("DELETE replacement contents escaped")
				}
			}
			oldValue, _ := old[1].Integer()
			newValue, _ := next[1].Integer()
			if oldValue != 5 || newValue != 7 || routine.SQL.PL != program || routine.Configuration != nil {
				t.Fatal("frame mutated caller or immutable program")
			}
			if core.ScopeActiveSQLRoutineDepth(s) != 0 || s.Op.EffectiveUser != nil || !core.SameSQLRoutineDemand(original, core.ScopeSQLRoutine(s)) {
				t.Fatal("invocation leaked actor, policy, depth or diagnostic owner")
			}
			if phase != "cancel" {
				capture, ok := s.BeforeCaptures[dml.BeforeKey{Relation: ref, Event: request.Event, Instead: true}]
				if !ok || capture.Relation != relation || capture.Catalog.Get(routine.Address()) != &changed {
					t.Fatal("command capture lost actual immutable identity")
				}
				if _, ok := s.BeforeCaptures[dml.BeforeKey{Relation: ref, Event: request.Event}]; ok {
					t.Fatal("timing capture collided with BEFORE")
				}
			}
			if !e.Mu.TryLock() {
				t.Fatal("invocation retained engine lock")
			}
			e.Mu.Unlock()
		})
	}
	t.Run("nested-record-and-argv", func(t *testing.T) {
		_, _, read, relation, routine := insteadUnitFixture(t, context.Background(), "BEGIN RETURN NEW; END")
		integer, _ := read.LookupType(sqltype.Int4)
		array, _ := read.LookupType(1007)
		nestedShape, _ := read.LookupType(relation.RowType)
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "items", Usage: array.Usage()}, {Attribute: 2, Name: "nested", Usage: nestedShape.Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		item := triggerUnitInt(t, integer, 5)
		elements := []sqltype.Value{item}
		items, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Length: 1, Lower: 1}}, elements)
		if err != nil {
			t.Fatal(err)
		}
		fields := []sqltype.Value{item, item}
		nested, err := sqltype.CompositeValue(nestedShape, fields)
		if err != nil {
			t.Fatal(err)
		}
		producer := []sqltype.Value{items, nested}
		rel, err := core.PLTriggerRelation(read, relation)
		if err != nil {
			t.Fatal(err)
		}
		rel.Shape = shape
		args := []string{"original"}
		outer, err := proc.NewPLTriggerFrame(routine.SQL, rel, "outer", "UPDATE", "INSTEAD OF", "ROW", args, producer, producer)
		if err != nil {
			t.Fatal(err)
		}
		retainedOld, retainedNew, retainedArgs := outer.Values[0], outer.Values[1], outer.Values[11]
		elements[0] = triggerUnitInt(t, integer, 99)
		fields[1] = elements[0]
		args[0] = "changed"
		producer[0] = sqltype.Null(array)
		inner, err := proc.NewPLTriggerFrame(routine.SQL, rel, "inner", "DELETE", "INSTEAD OF", "ROW", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		inner.Close()
		outer.Close()
		for _, value := range []sqltype.Value{retainedOld, retainedNew} {
			a, _ := value.CompositeField(0).ArrayElement(0).Integer()
			n, _ := value.CompositeField(1).CompositeField(1).Integer()
			if a != 5 || n != 5 {
				t.Fatal("nested frame borrowed producer backing")
			}
		}
		arg, _ := retainedArgs.ArrayElement(0).Text()
		if arg != "original" || retainedArgs.ArrayDimensions()[0].Lower != 0 {
			t.Fatal("TG_ARGV backing or lower bound changed")
		}
	})
}
