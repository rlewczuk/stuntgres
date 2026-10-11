package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeSequenceTempCreationSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, initialized := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+map[bool]string{false: "Uninitialized", true: "Initialized"}[initialized], func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "creation_a", "creation_b")
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						sequenceTempCreationWitness(t, fixture.NewTempReferenceSession(t, p, backend.DB, backend.Recorder, prepared), initialized)
					})
				}
			})
		}
	}
}

// Failed CREATE must not publish a temporary namespace or invalidate a held
// literal group. Actual native namespace identity is checked by engine tests.
func sequenceTempCreationWitness(t *testing.T, s *fixture.TempReferenceSession, initialized bool) {
	t.Helper()
	s.Schemas("creation_a", "creation_b")
	for _, q := range []string{
		"CREATE SCHEMA creation_a", "CREATE SCHEMA creation_b",
		"CREATE SEQUENCE creation_b.s START 10 CACHE 1",
		"CREATE SEQUENCE public.remembered START 31 CACHE 5",
		"CREATE SEQUENCE public.survivor START 71 CACHE 1",
	} {
		s.Exec(s.C, q, 0)
	}
	if initialized {
		s.Exec(s.C, "CREATE TEMP SEQUENCE seed START 41 CACHE 5", 0)
	}
	s.Exec(s.Peer, "CREATE TEMP SEQUENCE peer_seed START 81", 0)
	s.Exec(s.Peer, "SET search_path TO creation_a,creation_b,public", 0)
	h := s.Held(s.Peer, "SELECT nextval('s')")
	s.HeldValues(h, []string{"nextval"}, 10)
	s.Exec(s.C, "CREATE SEQUENCE creation_a.s START 100 CACHE 1", 0)
	s.Value(s.C, "SELECT nextval('public.remembered')", 31)
	rememberedLog := s.State(s.C, "public.remembered", 35, true)
	var seedLog int64
	if initialized {
		s.Value(s.C, "SELECT nextval('seed')", 41)
		seedLog = s.State(s.C, "pg_temp.seed", 45, true)
	}
	cases := []struct{ q, code, message string }{
		{"CREATE UNLOGGED SEQUENCE pg_temp.invalid", "42P16", "only temporary relations may be created in temporary schemas"},
		{"CREATE TEMP SEQUENCE creation_missing.invalid", "3F000", `schema "creation_missing" does not exist`},
		{"CREATE TEMP SEQUENCE public.invalid", "42P16", "cannot create temporary relation in non-temporary schema"},
		{"CREATE TEMP SEQUENCE IF NOT EXISTS public.survivor", "42P16", "cannot create temporary relation in non-temporary schema"},
	}
	if initialized {
		cases = append(cases, struct{ q, code, message string }{"CREATE UNLOGGED SEQUENCE IF NOT EXISTS pg_temp.seed", "42P16", "only temporary relations may be created in temporary schemas"})
	}
	for i, tc := range cases {
		s.Utility(tc.q, fixture.TempReferenceDiagnostic(tc.code, tc.message, "", ""))
		if after := s.State(s.C, "public.remembered", 35, true); after != rememberedLog {
			t.Fatal("failed CREATE changed generator", rememberedLog, after)
		}
		s.Value(s.C, "SELECT currval('public.remembered')", 31)
		s.Value(s.C, "SELECT lastval()", map[bool]int64{false: 31, true: 41}[initialized])
		if initialized {
			if after := s.State(s.C, "pg_temp.seed", 45, true); after != seedLog {
				t.Fatal("failed CREATE changed seed", seedLog, after)
			}
			s.Value(s.C, "SELECT currval('seed')", 41)
		}
		s.HeldValues(h, []string{"nextval"}, 11+int64(i))
		s.State(s.C, "creation_a.s", 100, false)
		s.State(s.C, "public.survivor", 71, false)
	}
	// Ordinary logged creation adjusts to TEMP; explicit TEMP alias also works.
	for _, q := range []string{"CREATE SEQUENCE pg_temp.valid_logged START 51", "CREATE TEMP SEQUENCE pg_temp.valid_temp START 61"} {
		s.Utility(q, nil)
	}
	for _, name := range []string{"pg_temp.invalid", "public.invalid"} {
		s.Missing(s.C, name, "last_value", "24")
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
}
