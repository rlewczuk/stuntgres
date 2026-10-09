package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedOptions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			c := w.c
			var actor string
			if err := c.QueryRowContext(p.ctx, "SELECT current_user").Scan(&actor); err != nil {
				t.Fatal(err)
			}
			for i, option := range []string{"AS BIGINT", "INCREMENT 1", "START 10", "RESTART 10", "MINVALUE 1", "NO MINVALUE", "MAXVALUE 9223372036854775807", "NO MAXVALUE", "CACHE 1", "CYCLE", "NO CYCLE", "SET UNLOGGED", "OWNED BY NONE", "OWNED BY b.t.c", "OWNER TO postgres", "SET LOGGED", "SET UNLOGGED"} {
				if i == 0 {
					exec("CREATE SCHEMA a")
					exec("CREATE SCHEMA b")
					exec("CREATE TABLE b.t(c INT)")
					exec("SET search_path TO a,b")
				}
				exec("CREATE SEQUENCE b.s START 10")
				if i == 16 {
					exec("ALTER SEQUENCE b.s SET UNLOGGED")
				}
				if option == "OWNER TO postgres" {
					option = `OWNER TO "` + actor + `"`
				}
				s := hold("SELECT nextval('s')")
				exec("CREATE SEQUENCE a.s START 100")
				// Bare ALTER is a syntax error, not a no-op option. It
				// must leave the held binding and generator untouched.
				_, syntaxErr := c.ExecContext(p.ctx, "ALTER SEQUENCE b.s")
				syntaxWant := controlsReferenceDiagnostic("42601", "syntax error at end of input")
				syntaxWant.Position = "19"
				referenceAssertDiagnostic(t, syntaxErr, &syntaxWant)
				exec("ALTER SEQUENCE b.s " + option)
				want := int64(100)
				if i >= 12 {
					want = 10
				}
				value(s, want)
				if i < 12 {
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('b.s')", 10)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				exec("DROP SEQUENCE a.s,b.s")
			}
		})
	}
}
