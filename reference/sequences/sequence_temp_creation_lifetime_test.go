package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceTempCreationLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, initialized := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+map[bool]string{false: "Uninitialized", true: "Initialized"}[initialized], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						s := fixture.NewTempReferenceSession(t, p, backend.DB, backend.Recorder, prepared)
						s.Exec(s.C, "CREATE SEQUENCE public.remembered START 31 CACHE 5", 0)
						s.Exec(s.C, "CREATE SEQUENCE public.survivor START 71 CACHE 1", 0)
						s.Exec(s.Peer, "CREATE TEMP SEQUENCE peer_seed START 81", 0)
						s.Value(s.C, "SELECT nextval('public.remembered')", 31)
						if initialized {
							s.Exec(s.C, "CREATE TEMP SEQUENCE seed START 41 CACHE 5", 0)
							s.Value(s.C, "SELECT nextval('seed')", 41)
						}
						for _, q := range []string{"CREATE SEQUENCE pg_temp.valid_logged START 51", "CREATE TEMP SEQUENCE pg_temp.valid_temp START 61"} {
							s.Utility(q, nil)
						}
						s.Value(s.C, "SELECT nextval('public.remembered')", 32)
						if initialized {
							s.Value(s.C, "SELECT nextval('seed')", 42)
						}
						s.Value(s.C, "SELECT nextval('pg_temp.valid_logged')", 51)
						s.Value(s.C, "SELECT nextval('pg_temp.valid_temp')", 61)
						s.Retire()
						s.Value(s.Peer, "SELECT nextval('peer_seed')", 81)
						s.Value(s.Peer, "SELECT nextval('public.survivor')", 71)
						s.Value(s.Peer, "SELECT nextval('public.remembered')", 36)
						for _, name := range []string{"valid_logged", "valid_temp"} {
							s.Missing(s.Peer, name, "last_value", "24")
						}
					})
				}
			})
		}
	}
}
