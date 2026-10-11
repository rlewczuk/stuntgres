package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceTemporaryTableControls(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "CREATE TABLE public.persistent(v INT)", 0)
		s.Exec(s.C, "INSERT INTO public.persistent VALUES(7)", 1)
		var dump bytes.Buffer
		if !s.Postgres {
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Exec(s.C, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
		s.Exec(s.Peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
		s.Exec(s.C, "SET search_path TO pg_temp", 0)
		s.Exec(s.Peer, "SET search_path TO pg_temp", 0)
		own, foreign := fixture.TempReferenceNamespace(s.T, s, false), fixture.TempReferenceNamespace(s.T, s, true)
		if own == foreign {
			s.T.Fatal("shared temporary namespace")
		}
		s.Utility("CREATE TABLE owned_table(v INT)", nil)
		s.Exec(s.C, "INSERT INTO owned_table VALUES(11)", 1)
		h := s.Held(s.C, "SELECT nextval('seed'),currval('seed'),lastval()")
		s.HeldValues(h, []string{"nextval", "currval", "lastval"}, 31, 31, 31)
		s.Value(s.Peer, "SELECT nextval('peer_seed')", 81)
		if !s.Postgres {
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
		}
		s.HeldValues(h, []string{"nextval", "currval", "lastval"}, 32, 32, 32)
		s.Value(s.C, "SELECT currval('seed')", 32)
		s.Value(s.C, "SELECT lastval()", 32)
		s.State(s.C, "seed", 35, true)
		fixture.TemporaryTableRows(s, s.C, "owned_table", 11)
		if !s.Postgres {
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
		}
		s.HeldValues(h, []string{"nextval", "currval", "lastval"}, 33, 33, 33)
		s.State(s.C, "seed", 35, true)
		fixture.TemporaryTableRows(s, s.C, "owned_table", 11)
		fixture.TemporaryTableRows(s, s.C, "public.persistent", 7)
		s.Value(s.Peer, "SELECT nextval('peer_seed')", 82)
		if err := h.Statement.Close(); err != nil {
			s.T.Fatal(err)
		}
		s.Retire()
		s.Missing(s.Peer, own+".seed", "last_value", "24")
		s.Missing(s.Peer, own+".owned_table", "v", "15")
		s.Value(s.Peer, "SELECT nextval('peer_seed')", 83)
		fixture.TemporaryTableRows(s, s.Peer, "public.persistent", 7)
	})
}
