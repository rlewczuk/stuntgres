package reference_test

import (
	"strings"
	"testing"
)

func tempReferenceNamespace(t *testing.T, s *tempReferenceSession, peer bool) string {
	t.Helper()
	c := s.c
	if peer {
		c = s.peer
	}
	var name string
	if err := c.QueryRowContext(s.p.ctx, "SELECT current_schema").Scan(&name); err != nil || !strings.HasPrefix(name, "pg_temp_") {
		t.Fatal(name, err)
	}
	return name
}

func TestReferenceSequenceTemporaryTableBoundary(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
		s.exec(s.peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
		s.exec(s.c, "SET search_path TO pg_temp", 0)
		s.exec(s.peer, "SET search_path TO pg_temp", 0)
		own, foreign := tempReferenceNamespace(s.t, s, false), tempReferenceNamespace(s.t, s, true)
		if own == foreign {
			s.t.Fatal("shared temporary namespace")
		}
		h := s.held(s.c, "SELECT nextval('seed'),currval('seed'),lastval()")
		s.heldValues(h, []string{"nextval", "currval", "lastval"}, 31, 31, 31)
		s.value(s.peer, "SELECT nextval('peer_seed')", 81)
		for _, c := range []struct{ q, name string }{
			{"CREATE TABLE path_table(v INT)", "path_table"},
			{"CREATE TABLE " + own + ".qualified(v INT)", "qualified"},
			{"CREATE TABLE pg_temp.alias_table(v INT)", "alias_table"},
		} {
			if s.prepared {
				held := s.held(s.c, c.q)
				for range 2 {
					referenceRunStep(s.t, s.p.ctx, held, s.record, referenceStep{query: c.q})
					s.utility("DROP TABLE "+c.name, nil)
				}
				if err := held.statement.Close(); err != nil {
					s.t.Fatal(err)
				}
			} else {
				s.utility(c.q, nil)
				s.utility("DROP TABLE "+c.name, nil)
			}
			s.state(s.c, "seed", 35, true)
			s.state(s.peer, "peer_seed", 85, true)
			s.value(s.c, "SELECT currval('seed')", 31)
			s.value(s.c, "SELECT lastval()", 31)
		}
		for _, c := range []struct{ q, position string }{
			{"CREATE TABLE " + foreign + ".forbidden(v INT)", "14"},
			{"CREATE TABLE IF NOT EXISTS " + foreign + ".peer_seed(v INT)", "28"},
		} {
			s.utility(c.q, tempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", c.position))
			s.state(s.c, "seed", 35, true)
			s.state(s.peer, "peer_seed", 85, true)
			s.value(s.c, "SELECT currval('seed')", 31)
			s.value(s.c, "SELECT lastval()", 31)
		}
		s.utility("CREATE TABLE IF NOT EXISTS seed(v INT)", nil)
		s.utility("CREATE TABLE IF NOT EXISTS "+own+".seed(v INT)", nil)
		s.state(s.c, "seed", 35, true)
		s.state(s.peer, "peer_seed", 85, true)
		s.missing(s.c, own+".forbidden", "v", "15")
		s.missing(s.peer, foreign+".forbidden", "v", "15")
		s.utility("CREATE TABLE public.allowed(v INT)", nil)
		s.exec(s.c, "INSERT INTO public.allowed VALUES(7)", 1)
		temporaryTableRows(s, s.c, "public.allowed", 7)
	})
}
