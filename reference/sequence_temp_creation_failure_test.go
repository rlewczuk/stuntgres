package reference_test

import "testing"

func TestReferenceSequenceTempCreationFailures(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, initialized := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+map[bool]string{false: "Uninitialized", true: "Initialized"}[initialized], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						s := newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)
						for _, q := range []string{"CREATE SCHEMA creation_a", "CREATE SCHEMA creation_b", "CREATE SEQUENCE creation_b.s START 10 CACHE 1", "CREATE SEQUENCE public.remembered START 31 CACHE 5", "CREATE SEQUENCE public.survivor START 71 CACHE 1"} {
							s.exec(s.c, q, 0)
						}
						s.schemas("creation_a", "creation_b")
						if initialized {
							s.exec(s.c, "CREATE TEMP SEQUENCE seed START 41 CACHE 5", 0)
						}
						s.exec(s.peer, "CREATE TEMP SEQUENCE peer_seed START 81", 0)
						s.exec(s.peer, "SET search_path TO creation_a,creation_b,public", 0)
						h := s.held(s.peer, "SELECT nextval('s')")
						s.heldValues(h, []string{"nextval"}, 10)
						s.exec(s.c, "CREATE SEQUENCE creation_a.s START 100 CACHE 1", 0)
						s.value(s.c, "SELECT nextval('public.remembered')", 31)
						rememberedLog := s.state(s.c, "public.remembered", 35, true)
						var seedLog int64
						if initialized {
							s.value(s.c, "SELECT nextval('seed')", 41)
							seedLog = s.state(s.c, "pg_temp.seed", 45, true)
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
							s.utility(tc.q, tempReferenceDiagnostic(tc.code, tc.message, "", ""))
							if after := s.state(s.c, "public.remembered", 35, true); after != rememberedLog {
								t.Fatal("failed CREATE changed generator", rememberedLog, after)
							}
							s.value(s.c, "SELECT currval('public.remembered')", 31)
							s.value(s.c, "SELECT lastval()", map[bool]int64{false: 31, true: 41}[initialized])
							if initialized {
								if after := s.state(s.c, "pg_temp.seed", 45, true); after != seedLog {
									t.Fatal("failed CREATE changed seed", seedLog, after)
								}
								s.value(s.c, "SELECT currval('seed')", 41)
							}
							s.heldValues(h, []string{"nextval"}, 11+int64(i))
							s.state(s.c, "creation_a.s", 100, false)
							s.state(s.c, "public.survivor", 71, false)
						}
						for _, q := range []string{"CREATE SEQUENCE pg_temp.valid_logged START 51", "CREATE TEMP SEQUENCE pg_temp.valid_temp START 61"} {
							s.utility(q, nil)
						}
						for _, name := range []string{"pg_temp.invalid", "public.invalid"} {
							s.missing(s.c, name, "last_value", "24")
						}
						s.value(s.c, "SELECT nextval('public.remembered')", 32)
						if initialized {
							s.value(s.c, "SELECT nextval('seed')", 42)
						}
					})
				}
			})
		}
	}
}
