package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequencePreparedTemporalParameterMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			c := w.c
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
				exec(q)
			}
			s := hold("SELECT nextval('s'),TIMESTAMP '2000-01-01',setval('t',$1,false)")
			check := func(want int64) {
				stamp, _ := sequencePreparedTemporal(t, w, s, want)
				if !stamp.Equal(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
					t.Fatal("static timestamp", stamp)
				}
			}
			check(10)
			for _, q := range []string{"CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE a.t START 200", "ALTER SEQUENCE b.s CACHE 1"} {
				exec(q)
			}
			check(100)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('a.t')", 70)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('b.t')", 70)
		})
	}
}
