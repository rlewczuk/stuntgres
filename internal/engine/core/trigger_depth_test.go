package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTriggerDepthRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		fail, cancel bool
	}{
		{"success", "BEGIN NEW.v := pg_trigger_depth(); RETURN NEW; END", false, false},
		{"protected", "BEGIN BEGIN NEW.v := 1/0; EXCEPTION WHEN division_by_zero THEN NEW.v := pg_trigger_depth(); END; RETURN NEW; END", false, false},
		{"error", "BEGIN NEW.v := 1/0; RETURN NEW; END", true, false},
		{"cancel", "BEGIN NEW.v := pg_trigger_depth(); RETURN NEW; END", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e, s, read, relation, d := triggerUnitFixtureContext(t, ctx, tc.body)
			definition := triggerUnitDefinition(t, read, relation, d, "depth", stored.TriggerInsert)
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, s, relation)
			integer, _ := read.LookupType(sqltype.Int4)
			h := s.WriteHost()
			check := func(want int64) {
				t.Helper()
				value, err := h.TriggerDepthValue(integer)
				if err != nil {
					t.Fatal(err)
				}
				got, err := value.Integer()
				if err != nil || got != want {
					t.Fatal("live depth", got, want, err)
				}
			}
			check(0)
			// A real host RI entry and a user invocation share the same owner,
			// while general routine safety depth starts and ends independently.
			h.EnterForeignKeyTrigger()
			defer h.LeaveForeignKeyTrigger()
			check(1)
			if core.ScopeActiveSQLRoutineDepth(s) != 0 {
				t.Fatal("RI entered ordinary routine safety depth")
			}
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			request := dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 0)}}
			if tc.cancel {
				cancel()
			}
			result, err := h.BeforeRow(request)
			if (err != nil) != tc.fail {
				t.Fatal("invocation outcome", err)
			}
			if !tc.fail {
				got, err := result.Row[1].Integer()
				if err != nil || got != 2 {
					t.Fatal("user body depth", got, err)
				}
			}
			check(1)
			if core.ScopeActiveSQLRoutineDepth(s) != 0 {
				t.Fatal("ordinary routine depth leaked")
			}
			h.LeaveForeignKeyTrigger()
			check(0)
			h.EnterForeignKeyTrigger()
		})
	}
}
