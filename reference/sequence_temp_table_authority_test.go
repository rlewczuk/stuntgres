package reference_test

import "testing"

func TestReferenceSequenceTemporaryTableCapability(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
		s.exec(s.peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
		s.exec(s.c, "SET search_path TO pg_temp", 0)
		s.exec(s.peer, "SET search_path TO pg_temp", 0)
		own, foreign := tempReferenceNamespace(s.t, s, false), tempReferenceNamespace(s.t, s, true)
		if own == foreign {
			s.t.Fatal(own, foreign)
		}
		s.value(s.c, "SELECT nextval('seed')", 31)
		log := s.state(s.c, "seed", 35, true)
		for _, c := range []struct{ q, position string }{
			{"CREATE TABLE " + foreign + ".forbidden(v INT)", "14"},
			{"CREATE TABLE IF NOT EXISTS " + foreign + ".peer_seed(v INT)", "28"},
		} {
			s.utility(c.q, tempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", c.position))
		}
		for _, c := range []struct{ q, name string }{
			{"CREATE TABLE " + own + ".qualified(v INT)", "qualified"},
			{"CREATE TABLE path_table(v INT)", "path_table"},
		} {
			s.utility(c.q, nil)
			s.exec(s.c, "INSERT INTO "+c.name+" VALUES(7)", 1)
			temporaryTableRows(s, s.c, own+"."+c.name, 7)
		}
		if after := s.state(s.c, "seed", 35, true); after != log {
			s.t.Fatal(log, after)
		}
		s.value(s.c, "SELECT currval('seed')", 31)
		s.value(s.c, "SELECT lastval()", 31)
		s.value(s.c, "SELECT nextval('seed')", 32)
		s.utility("CREATE TABLE public.persistent(v INT)", nil)
		s.retire()
		for _, name := range []string{"seed", "qualified", "path_table"} {
			if s.postgres {
				var absent bool
				if err := s.peer.QueryRowContext(s.p.ctx, "SELECT to_regclass($1) IS NULL", own+"."+name).Scan(&absent); err != nil || !absent {
					s.t.Fatal(name, absent, err)
				}
			} else {
				column, position := "v", "15"
				if name == "seed" {
					column, position = "last_value", "24"
				}
				s.missing(s.peer, own+"."+name, column, position)
			}
		}
		s.value(s.peer, "SELECT nextval('peer_seed')", 81)
		temporaryTableRows(s, s.peer, "public.persistent")
	})
}
