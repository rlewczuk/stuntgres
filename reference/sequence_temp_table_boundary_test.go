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
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				if backend.pg {
					continue
				} // Actual PG CREATE capability has its own independent test.
				t.Run(backend.name, func(t *testing.T) {
					s := newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)
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
					for _, q := range []string{"CREATE TABLE forbidden(v INT)", "CREATE TABLE " + own + ".forbidden(v INT)", "CREATE TABLE " + foreign + ".forbidden(v INT)", "CREATE TABLE IF NOT EXISTS seed(v INT)", "CREATE TABLE IF NOT EXISTS " + own + ".seed(v INT)", "CREATE TABLE IF NOT EXISTS " + foreign + ".peer_seed(v INT)", "CREATE TABLE pg_temp.forbidden(v INT)"} {
						position := ""
						if q == "CREATE TABLE pg_temp.forbidden(v INT)" {
							position = "14"
						}
						d := tempReferenceDiagnostic("0A000", "temporary schema creation is unsupported", "", position)
						if prepared {
							held := s.held(s.c, q)
							for range 2 {
								referenceRunStep(t, p.ctx, held, s.record, referenceStep{query: q, state: d.Code, diagnostic: d, errorStage: "execute"})
							}
							if err := held.statement.Close(); err != nil {
								t.Fatal(err)
							}
						} else {
							s.utility(q, d)
						}
						s.state(s.c, "seed", 35, true)
						s.state(s.peer, "peer_seed", 85, true)
						s.value(s.c, "SELECT currval('seed')", 31)
						s.value(s.c, "SELECT lastval()", 31)
					}
					s.missing(s.c, own+".forbidden", "v", "15")
					s.missing(s.peer, foreign+".forbidden", "v", "15")
					s.exec(s.c, "CREATE TABLE public.allowed(v INT)", 0)
					s.exec(s.c, "INSERT INTO public.allowed VALUES(7)", 1)
					referenceRunStep(t, p.ctx, s.c, s.record, sequenceInt64Step(referenceStep{query: "SELECT v FROM public.allowed", selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}}))
				})
			}
		})
	}
}
