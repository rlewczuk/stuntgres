package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceTemporaryTableControls(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "CREATE TABLE public.persistent(v INT)", 0)
		s.exec(s.c, "INSERT INTO public.persistent VALUES(7)", 1)
		var dump bytes.Buffer
		if !s.postgres {
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
		}
		s.exec(s.c, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
		s.exec(s.peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
		s.exec(s.c, "SET search_path TO pg_temp", 0)
		s.exec(s.peer, "SET search_path TO pg_temp", 0)
		own, foreign := tempReferenceNamespace(s.t, s, false), tempReferenceNamespace(s.t, s, true)
		if own == foreign {
			s.t.Fatal("shared temporary namespace")
		}
		s.utility("CREATE TABLE owned_table(v INT)", nil)
		s.exec(s.c, "INSERT INTO owned_table VALUES(11)", 1)
		h := s.held(s.c, "SELECT nextval('seed'),currval('seed'),lastval()")
		s.heldValues(h, []string{"nextval", "currval", "lastval"}, 31, 31, 31)
		s.value(s.peer, "SELECT nextval('peer_seed')", 81)
		if !s.postgres {
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
		}
		s.heldValues(h, []string{"nextval", "currval", "lastval"}, 32, 32, 32)
		s.value(s.c, "SELECT currval('seed')", 32)
		s.value(s.c, "SELECT lastval()", 32)
		s.state(s.c, "seed", 35, true)
		temporaryTableRows(s, s.c, "owned_table", 11)
		if !s.postgres {
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
		}
		s.heldValues(h, []string{"nextval", "currval", "lastval"}, 33, 33, 33)
		s.state(s.c, "seed", 35, true)
		temporaryTableRows(s, s.c, "owned_table", 11)
		temporaryTableRows(s, s.c, "public.persistent", 7)
		s.value(s.peer, "SELECT nextval('peer_seed')", 82)
		if err := h.statement.Close(); err != nil {
			s.t.Fatal(err)
		}
		s.retire()
		s.missing(s.peer, own+".seed", "last_value", "24")
		s.missing(s.peer, own+".owned_table", "v", "15")
		s.value(s.peer, "SELECT nextval('peer_seed')", 83)
		temporaryTableRows(s, s.peer, "public.persistent", 7)
	})
}
