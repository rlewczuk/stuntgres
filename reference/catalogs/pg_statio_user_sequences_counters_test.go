package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserSequencesCounters(t *testing.T) {
	// Fresh physical writers publish each cumulative prefix once, preserving
	// cache tails without sleeps, forced flushes or publication-throttle changes.
	vectors := []struct {
		label     string
		read, hit int64
	}{
		{"creation_and_metadata", 0, 0}, {"first_reservation", 1, 0},
		{"cached_tail", 1, 0}, {"refill", 1, 1}, {"memory_only", 1, 1},
		{"setval", 1, 2}, {"authorized_uncalled_last_value", 1, 3},
		{"direct_generator_read", 1, 4}, {"post_setval_reservation", 1, 5},
		{"failed_and_metadata_reads", 1, 5},
	}
	for prefix, vector := range vectors {
		t.Run(vector.label, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					writer, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					statioSequenceExec(t, p, b, writer, "CREATE SEQUENCE statio_user_counter CACHE 3")
					oid := statioSequenceOID(t, p, writer, "public.statio_user_counter")
					stmt, err := observer.PrepareContext(p.Ctx, statioUserSequenceProbe)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioUserSequenceProbe}
					statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_counter", 0, 0)
					statioSequenceExec(t, p, b, writer, "BEGIN")
					defer writer.ExecContext(context.Background(), "ROLLBACK")
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Select("SELECT seqcache FROM pg_sequence WHERE seqrelid=$1::oid", []string{"seqcache"}, []string{"INT8"}, [][]any{{int64(3)}}, oid))
					if prefix >= 1 {
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_counter')", "nextval", 1)
					}
					if prefix >= 2 {
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_counter')", "nextval", 2)
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_counter')", "nextval", 3)
					}
					if prefix >= 3 {
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_counter')", "nextval", 4)
					}
					if prefix >= 4 {
						pgSequencesScalar(t, p, b, writer, "SELECT currval('statio_user_counter')", "currval", 4)
						pgSequencesScalar(t, p, b, writer, "SELECT lastval()", "lastval", 4)
					}
					if prefix >= 5 {
						pgSequencesScalar(t, p, b, writer, "SELECT setval('statio_user_counter',20,false)", "setval", 20)
					}
					if prefix >= 6 {
						fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Select("SELECT last_value FROM pg_sequences WHERE schemaname='public' AND sequencename='statio_user_counter'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}))
					}
					if prefix >= 7 {
						pgSequencesScalar(t, p, b, writer, "SELECT last_value FROM statio_user_counter", "last_value", 20)
					}
					if prefix >= 8 {
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_counter')", "nextval", 20)
					}
					if prefix >= 9 {
						statioSequenceRows(t, p, b, writer, statioUserSequenceProbe, oid)
						statioSequenceRows(t, p, b, writer, statioSequenceProbe, oid)
						for _, q := range []string{"SELECT last_value FROM statio_user_counter", "SELECT last_value FROM pg_sequences WHERE sequencename='statio_user_counter'", statioUserSequenceProbe} {
							metadata, err := writer.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							metadata.Close()
						}
						for _, prepared := range []bool{false, true} {
							statioSequenceExec(t, p, b, writer, "SAVEPOINT rejected")
							s := fixture.ImplicitError("SELECT setval('statio_user_counter',0,false)", fixture.Diagnostic{Code: "22003", Message: `setval: value 0 is out of bounds for sequence "statio_user_counter" (1..9223372036854775807)`})
							s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
							fixture.RunStep(t, p.Ctx, writer, b.Recorder, s)
							statioSequenceExec(t, p, b, writer, "ROLLBACK TO rejected")
						}
					}
					statioSequenceExec(t, p, b, writer, "COMMIT")
					statioSequenceRetire(t, writer)
					statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_counter", vector.read, vector.hit)
				})
			}
		})
	}
	for _, kind := range []string{"cache_one", "two_physical_writers", "tracking_off_tail", "tracking_off_refill", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					writer, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					cache := 3
					if kind == "cache_one" {
						cache = 1
					}
					statioSequenceExec(t, p, b, writer, fmt.Sprintf("CREATE SEQUENCE statio_user_vector CACHE %d", cache))
					oid := statioSequenceOID(t, p, writer, "public.statio_user_vector")
					stmt, err := observer.PrepareContext(p.Ctx, statioUserSequenceProbe)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioUserSequenceProbe}
					statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_vector", 0, 0)
					statioSequenceExec(t, p, b, writer, "BEGIN")
					defer writer.ExecContext(context.Background(), "ROLLBACK")
					read, hit := int64(1), int64(0)
					switch kind {
					case "cache_one":
						for n := int64(1); n <= 5; n++ {
							pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", n)
						}
						hit = 4
					case "two_physical_writers":
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						statioSequenceExec(t, p, b, peer, "BEGIN")
						defer peer.ExecContext(context.Background(), "ROLLBACK")
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", 1)
						pgSequencesScalar(t, p, b, peer, "SELECT nextval('statio_user_vector')", "nextval", 4)
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", 2)
						statioSequenceExec(t, p, b, peer, "COMMIT")
						statioSequenceRetire(t, peer)
						hit = 1
					case "tracking_off_tail", "tracking_off_refill":
						statioSequenceExec(t, p, b, writer, "SET track_counts=off")
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", 1)
						statioSequenceExec(t, p, b, writer, "SET track_counts=on")
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", 2)
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", 3)
						read = 0
						if kind == "tracking_off_refill" {
							pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", 4)
							read = 1
						}
					case "rollback":
						pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_vector')", "nextval", 1)
					}
					end := "COMMIT"
					if kind == "rollback" {
						end = "ROLLBACK"
					}
					statioSequenceExec(t, p, b, writer, end)
					statioSequenceRetire(t, writer)
					statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_vector", read, hit)
				})
			}
		})
	}
}
