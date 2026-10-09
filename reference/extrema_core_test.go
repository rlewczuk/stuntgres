package reference_test

import (
	"database/sql"
	"testing"
)

// extremaReference shares the actual SQL/API body across available backends.
func extremaReference(t *testing.T, body func(*testing.T, *referencePair, *sql.Conn, *referenceRecorder, bool)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, conn, backend.recorder, backend.pg)
		})
	}
}

func extremaBoth(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step := s
			if prepared {
				step = referencePrepared(step)
			}
			referenceRunStep(t, p.ctx, conn, recorder, step)
		})
	}
}

func TestReferenceExtremaCore(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE extrema_t(k TEXT,v INTEGER)", 0))
		run(referenceExec("INSERT INTO extrema_t VALUES ('a',3),('a',NULL),('a',-2),('b',8),('b',8),(NULL,NULL)", 6))
		for _, tc := range []struct {
			name string
			step referenceStep
		}{
			{"global", referenceSelect("SELECT min(v),max(v) FROM extrema_t", []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(8)}})},
			{"empty", referenceSelect("SELECT min(v),max(v) FROM extrema_t WHERE FALSE", []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}})},
			{"null", referenceSelect("SELECT min(v),max(v) FROM extrema_t WHERE k IS NULL", []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}})},
			{"empty_grouped", referenceSelect("SELECT k,min(v),max(v) FROM extrema_t WHERE FALSE GROUP BY k", []string{"k", "min", "max"}, []string{"TEXT", "INT4", "INT4"}, nil)},
			{"having", referenceSelect("SELECT k,min(v),min(v),count(v) FROM extrema_t GROUP BY k HAVING max(v)>3 ORDER BY max(v)", []string{"k", "min", "min", "count"}, []string{"TEXT", "INT4", "INT4", "INT8"}, [][]any{{"b", int64(8), int64(8), int64(2)}})},
		} {
			t.Run(tc.name, func(t *testing.T) { extremaBoth(t, p, conn, recorder, tc.step) })
		}
		s := referenceSelect("SELECT k,min(v),max(v) FROM extrema_t GROUP BY k", []string{"k", "min", "max"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"a", int64(-2), int64(3)}, {"b", int64(8), int64(8)}, {nil, nil, nil}})
		s.bag = true
		extremaBoth(t, p, conn, recorder, s)
	})
}

func TestReferenceExtremaResolution(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE DOMAIN extrema_di AS INTEGER", 0))
		run(referenceExec("CREATE DOMAIN extrema_ds AS VARCHAR(4) COLLATE \"C\"", 0))
		run(referenceExec("CREATE DOMAIN extrema_chain AS extrema_di", 0))
		run(referenceExec("CREATE TABLE extrema_resolution(i extrema_chain,s extrema_ds)", 0))
		run(referenceExec("INSERT INTO extrema_resolution VALUES (4,'z'),(-2,'a'),(NULL,NULL)", 3))
		extremaBoth(t, p, conn, recorder, referenceSelect("SELECT min(i),max(s),min(NULL),max('x'),min($1),max($2::integer) FROM extrema_resolution", []string{"min", "max", "min", "max", "min", "max"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "INT4"}, [][]any{{int64(-2), "z", nil, "x", "y", int64(9)}}, "y", int64(9)))
		extremaBoth(t, p, conn, recorder, referenceSelect("SELECT min(i),max(s),min(NULL),max('x'),min($1) FROM extrema_resolution WHERE FALSE", []string{"min", "max", "min", "max", "min"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil, nil, nil}}, "y"))
		extremaBoth(t, p, conn, recorder, referenceSelect("SELECT min(relname),max(relkind) FROM pg_catalog.pg_class WHERE relname='extrema_resolution'", []string{"min", "max"}, []string{"TEXT", "TEXT"}, [][]any{{"extrema_resolution", "r"}}))
	})
}
