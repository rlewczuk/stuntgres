package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceSequenceTemporaryTableCapability(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				if !backend.pg {
					continue
				} // Native CREATE rejection is independently asserted, not parity.
				t.Run(backend.name, func(t *testing.T) {
					s := newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)
					s.exec(s.c, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
					s.exec(s.peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
					s.exec(s.c, "SET search_path TO pg_temp", 0)
					s.exec(s.peer, "SET search_path TO pg_temp", 0)
					own, foreign := tempReferenceNamespace(t, s, false), tempReferenceNamespace(t, s, true)
					if own == foreign {
						t.Fatal(own, foreign)
					}
					var pid int
					if err := s.c.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
						t.Fatal(err)
					}
					s.value(s.c, "SELECT nextval('seed')", 31)
					log := s.state(s.c, "seed", 35, true)
					for _, tc := range []struct{ q, position string }{{"CREATE TABLE " + foreign + ".forbidden(v INT)", "14"}, {"CREATE TABLE IF NOT EXISTS " + foreign + ".peer_seed(v INT)", "28"}} {
						s.utility(tc.q, tempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", tc.position))
					}
					for _, tc := range []struct{ q, name string }{{"CREATE TABLE " + own + ".qualified(v INT)", "qualified"}, {"CREATE TABLE path_table(v INT)", "path_table"}} {
						s.utility(tc.q, nil)
						s.exec(s.c, "INSERT INTO "+tc.name+" VALUES(7)", 1)
						referenceRunStep(t, p.ctx, s.c, s.record, sequenceInt64Step(referenceStep{query: "SELECT v FROM " + own + "." + tc.name, selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}}))
					}
					if after := s.state(s.c, "seed", 35, true); after != log {
						t.Fatal(log, after)
					}
					s.value(s.c, "SELECT currval('seed')", 31)
					s.value(s.c, "SELECT lastval()", 31)
					s.value(s.c, "SELECT nextval('seed')", 32)
					s.utility("CREATE TABLE public.persistent(v INT)", nil)
					s.retire()
					ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
					defer cancel()
					for {
						var live bool
						if err := s.peer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
							t.Fatal(err)
						}
						if !live {
							break
						}
					}
					for _, name := range []string{"seed", "qualified", "path_table"} {
						var absent bool
						if err := s.peer.QueryRowContext(p.ctx, "SELECT to_regclass($1) IS NULL", own+"."+name).Scan(&absent); err != nil || !absent {
							t.Fatal(name, absent, err)
						}
					}
					s.value(s.peer, "SELECT nextval('peer_seed')", 81)
					var exists bool
					if err := s.peer.QueryRowContext(p.ctx, "SELECT to_regclass('public.persistent') IS NOT NULL").Scan(&exists); err != nil || !exists {
						t.Fatal(exists, err)
					}
				})
			}
		})
	}
}
