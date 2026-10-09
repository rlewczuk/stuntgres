package reference_test

import "testing"

func TestReferenceSequenceTempCreationLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, initialized := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+map[bool]string{false: "Uninitialized", true: "Initialized"}[initialized], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						s := newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)
						s.exec(s.c, "CREATE SEQUENCE public.remembered START 31 CACHE 5", 0)
						s.exec(s.c, "CREATE SEQUENCE public.survivor START 71 CACHE 1", 0)
						s.exec(s.peer, "CREATE TEMP SEQUENCE peer_seed START 81", 0)
						s.value(s.c, "SELECT nextval('public.remembered')", 31)
						if initialized {
							s.exec(s.c, "CREATE TEMP SEQUENCE seed START 41 CACHE 5", 0)
							s.value(s.c, "SELECT nextval('seed')", 41)
						}
						for _, q := range []string{"CREATE SEQUENCE pg_temp.valid_logged START 51", "CREATE TEMP SEQUENCE pg_temp.valid_temp START 61"} {
							s.utility(q, nil)
						}
						s.value(s.c, "SELECT nextval('public.remembered')", 32)
						if initialized {
							s.value(s.c, "SELECT nextval('seed')", 42)
						}
						s.value(s.c, "SELECT nextval('pg_temp.valid_logged')", 51)
						s.value(s.c, "SELECT nextval('pg_temp.valid_temp')", 61)
						s.retire()
						s.value(s.peer, "SELECT nextval('peer_seed')", 81)
						s.value(s.peer, "SELECT nextval('public.survivor')", 71)
						s.value(s.peer, "SELECT nextval('public.remembered')", 36)
						for _, name := range []string{"valid_logged", "valid_temp"} {
							s.missing(s.peer, name, "last_value", "24")
						}
					})
				}
			})
		}
	}
}
