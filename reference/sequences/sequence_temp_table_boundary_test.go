package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceTemporaryTableBoundary(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
		s.Exec(s.Peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
		s.Exec(s.C, "SET search_path TO pg_temp", 0)
		s.Exec(s.Peer, "SET search_path TO pg_temp", 0)
		own, foreign := fixture.TempReferenceNamespace(s.T, s, false), fixture.TempReferenceNamespace(s.T, s, true)
		if own == foreign {
			s.T.Fatal("shared temporary namespace")
		}
		h := s.Held(s.C, "SELECT nextval('seed'),currval('seed'),lastval()")
		s.HeldValues(h, []string{"nextval", "currval", "lastval"}, 31, 31, 31)
		s.Value(s.Peer, "SELECT nextval('peer_seed')", 81)
		for _, c := range []struct{ q, name string }{
			{"CREATE TABLE path_table(v INT)", "path_table"},
			{"CREATE TABLE " + own + ".qualified(v INT)", "qualified"},
			{"CREATE TABLE pg_temp.alias_table(v INT)", "alias_table"},
		} {
			if s.Prepared {
				held := s.Held(s.C, c.q)
				for range 2 {
					fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Step{Query: c.q})
					s.Utility("DROP TABLE "+c.name, nil)
				}
				if err := held.Statement.Close(); err != nil {
					s.T.Fatal(err)
				}
			} else {
				s.Utility(c.q, nil)
				s.Utility("DROP TABLE "+c.name, nil)
			}
			s.State(s.C, "seed", 35, true)
			s.State(s.Peer, "peer_seed", 85, true)
			s.Value(s.C, "SELECT currval('seed')", 31)
			s.Value(s.C, "SELECT lastval()", 31)
		}
		for _, c := range []struct{ q, position string }{
			{"CREATE TABLE " + foreign + ".forbidden(v INT)", "14"},
			{"CREATE TABLE IF NOT EXISTS " + foreign + ".peer_seed(v INT)", "28"},
		} {
			s.Utility(c.q, fixture.TempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", c.position))
			s.State(s.C, "seed", 35, true)
			s.State(s.Peer, "peer_seed", 85, true)
			s.Value(s.C, "SELECT currval('seed')", 31)
			s.Value(s.C, "SELECT lastval()", 31)
		}
		s.Utility("CREATE TABLE IF NOT EXISTS seed(v INT)", nil)
		s.Utility("CREATE TABLE IF NOT EXISTS "+own+".seed(v INT)", nil)
		s.State(s.C, "seed", 35, true)
		s.State(s.Peer, "peer_seed", 85, true)
		s.Missing(s.C, own+".forbidden", "v", "15")
		s.Missing(s.Peer, foreign+".forbidden", "v", "15")
		s.Utility("CREATE TABLE public.allowed(v INT)", nil)
		s.Exec(s.C, "INSERT INTO public.allowed VALUES(7)", 1)
		fixture.TemporaryTableRows(s, s.C, "public.allowed", 7)
	})
}
