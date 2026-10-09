package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceTemporaryTableControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			s := newTempReferenceSession(t, p, p.memory, p.memoryRecord, prepared)
			s.exec(s.c, "CREATE TABLE public.persistent(v INT)", 0)
			s.exec(s.c, "INSERT INTO public.persistent VALUES(7)", 1)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			s.exec(s.c, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
			s.exec(s.peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
			s.exec(s.c, "SET search_path TO pg_temp", 0)
			s.exec(s.peer, "SET search_path TO pg_temp", 0)
			own, foreign := tempReferenceNamespace(t, s, false), tempReferenceNamespace(t, s, true)
			if own == foreign {
				t.Fatal("shared temporary namespace")
			}
			h := s.held(s.c, "SELECT nextval('seed'),currval('seed'),lastval()")
			s.heldValues(h, []string{"nextval", "currval", "lastval"}, 31, 31, 31)
			s.value(s.peer, "SELECT nextval('peer_seed')", 81)
			// Persistent controls may replace the catalog, never physical TEMP provenance or tails.
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			s.heldValues(h, []string{"nextval", "currval", "lastval"}, 32, 32, 32)
			s.value(s.c, "SELECT currval('seed')", 32)
			s.value(s.c, "SELECT lastval()", 32)
			s.state(s.c, "seed", 35, true)
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			s.heldValues(h, []string{"nextval", "currval", "lastval"}, 33, 33, 33)
			s.state(s.c, "seed", 35, true)
			referenceRunStep(t, p.ctx, s.c, s.record, sequenceInt64Step(referenceStep{query: "SELECT v FROM public.persistent", selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}}))
			s.value(s.peer, "SELECT nextval('peer_seed')", 82)
			if err := h.statement.Close(); err != nil {
				t.Fatal(err)
			}
			s.retire()
			s.missing(s.peer, own+".seed", "last_value", "24")
			s.value(s.peer, "SELECT nextval('peer_seed')", 83)
			referenceRunStep(t, p.ctx, s.peer, s.record, sequenceInt64Step(referenceStep{query: "SELECT v FROM public.persistent", selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}}))
		})
	}
}
