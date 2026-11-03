package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func statioSequenceRetire(t *testing.T, c *sql.Conn) {
	t.Helper()
	if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal("physical writer retirement", err)
	}
}

func TestReferencePGStatioAllSequencesCounters(t *testing.T) {
	// Each prefix has a fresh writer and exactly one publication. This observes
	// every boundary without sleeps, bypassing throttles or losing CACHE tails.
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
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					statioSequenceExec(t, p, b, a, "CREATE SEQUENCE statio_counter CACHE 3")
					oid := statioSequenceOID(t, p, a, "public.statio_counter")
					statioSequenceCheck(t, p, b, peer, oid, "public", "statio_counter", 0, 0)
					statioSequenceExec(t, p, b, a, "BEGIN")
					defer a.ExecContext(context.Background(), "ROLLBACK")
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT seqcache FROM pg_sequence WHERE seqrelid=$1::oid", []string{"seqcache"}, []string{"INT8"}, [][]any{{int64(3)}}, oid))
					if prefix >= 1 {
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_counter')", "nextval", 1)
					}
					if prefix >= 2 {
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_counter')", "nextval", 2)
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_counter')", "nextval", 3)
					}
					if prefix >= 3 {
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_counter')", "nextval", 4)
					}
					if prefix >= 4 {
						pgSequencesScalar(t, p, b, a, "SELECT currval('statio_counter')", "currval", 4)
						pgSequencesScalar(t, p, b, a, "SELECT lastval()", "lastval", 4)
					}
					if prefix >= 5 {
						pgSequencesScalar(t, p, b, a, "SELECT setval('statio_counter',20,false)", "setval", 20)
					}
					if prefix >= 6 {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT last_value FROM pg_sequences WHERE schemaname='public' AND sequencename='statio_counter'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}))
					}
					if prefix >= 7 {
						pgSequencesScalar(t, p, b, a, "SELECT last_value FROM statio_counter", "last_value", 20)
					}
					if prefix >= 8 {
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_counter')", "nextval", 20)
					}
					if prefix >= 9 {
						statioSequenceRows(t, p, b, a, statioSequenceProbe, oid)
						stmt, err := a.PrepareContext(p.Ctx, "SELECT last_value FROM statio_counter")
						if err != nil {
							t.Fatal(err)
						}
						stmt.Close()
						stmt, err = a.PrepareContext(p.Ctx, "SELECT last_value FROM pg_sequences WHERE sequencename='statio_counter'")
						if err != nil {
							t.Fatal(err)
						}
						stmt.Close()
						statioSequenceExec(t, p, b, a, "SAVEPOINT rejected")
						s := fixture.ImplicitError("SELECT setval('statio_counter',0,false)", fixture.Diagnostic{Code: "22003", Message: `setval: value 0 is out of bounds for sequence "statio_counter" (1..9223372036854775807)`})
						s.SelectRows = true
						fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
						statioSequenceExec(t, p, b, a, "ROLLBACK TO rejected")
					}
					statioSequenceExec(t, p, b, a, "COMMIT")
					statioSequenceCheck(t, p, b, peer, oid, "public", "statio_counter", vector.read, vector.hit)
					stmt, err := peer.PrepareContext(p.Ctx, statioSequenceProbe)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					statioSequenceCheck(t, p, b, fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: statioSequenceProbe}, oid, "public", "statio_counter", vector.read, vector.hit)
				})
			}
		})
	}
	for _, kind := range []string{"cache_one", "two_physical_sessions", "tracking_off_tail", "tracking_off", "rollback", "default_identity_trigger"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					cache := 3
					if kind == "cache_one" {
						cache = 1
					}
					statioSequenceExec(t, p, b, a, fmt.Sprintf("CREATE SEQUENCE statio_vector CACHE %d", cache))
					oid := statioSequenceOID(t, p, a, "public.statio_vector")
					statioSequenceExec(t, p, b, a, "BEGIN")
					wantRead, wantHit := int64(1), int64(0)
					switch kind {
					case "cache_one":
						for n := int64(1); n <= 5; n++ {
							pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", n)
						}
						wantHit = 4
					case "two_physical_sessions":
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 1)
						statioSequenceExec(t, p, b, peer, "BEGIN")
						pgSequencesScalar(t, p, b, peer, "SELECT nextval('statio_vector')", "nextval", 4)
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 2)
						statioSequenceExec(t, p, b, peer, "COMMIT")
						wantHit = 1
					case "tracking_off_tail":
						statioSequenceExec(t, p, b, a, "SET track_counts=off")
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 1)
						statioSequenceExec(t, p, b, a, "SET track_counts=on")
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 2)
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 3)
						wantRead = 0
					case "tracking_off":
						statioSequenceExec(t, p, b, a, "SET track_counts=off")
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 1)
						statioSequenceExec(t, p, b, a, "SET track_counts=on")
						for n := int64(2); n <= 4; n++ {
							pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", n)
						}
						statioSequenceExec(t, p, b, a, "SET track_counts=off")
						pgSequencesScalar(t, p, b, a, "SELECT setval('statio_vector',40,false)", "setval", 40)
						statioSequenceExec(t, p, b, a, "SET track_counts=on")
						for _, q := range []string{"SET track_activities=off", "SET track_functions=none", "SET track_io_timing=off", "SET track_wal_io_timing=off"} {
							statioSequenceExec(t, p, b, a, q)
						}
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 40)
						wantHit = 1
					case "rollback":
						pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_vector')", "nextval", 1)
					case "default_identity_trigger":
						statioSequenceExec(t, p, b, a, "CREATE TABLE statio_defaults(id bigint DEFAULT nextval('statio_vector'), ident bigint GENERATED ALWAYS AS IDENTITY)")
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO statio_defaults DEFAULT VALUES", 1))
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT id,ident FROM statio_defaults", []string{"id", "ident"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO statio_defaults DEFAULT VALUES", 1))
						pgSequencesScalar(t, p, b, a, "SELECT currval('statio_vector')", "currval", 2)
						statioSequenceExec(t, p, b, a, "CREATE FUNCTION statio_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM nextval('statio_vector'); RETURN NEW; END$$")
						statioSequenceExec(t, p, b, a, "CREATE TRIGGER statio_trigger BEFORE INSERT ON statio_defaults FOR EACH ROW EXECUTE FUNCTION statio_trigger_fn()")
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO statio_defaults DEFAULT VALUES", 1))
						pgSequencesScalar(t, p, b, a, "SELECT currval('statio_vector')", "currval", 4)
						wantHit = 1
					}
					end := "COMMIT"
					if kind == "rollback" {
						end = "ROLLBACK"
					}
					statioSequenceExec(t, p, b, a, end)
					statioSequenceCheck(t, p, b, peer, oid, "public", "statio_vector", wantRead, wantHit)
					if kind == "default_identity_trigger" {
						identity := statioSequenceOID(t, p, peer, "public.statio_defaults_ident_seq")
						statioSequenceCheck(t, p, b, peer, identity, "public", "statio_defaults_ident_seq", 1, 2)
					}
				})
			}
		})
	}
	t.Run("reader_modes", statioSequenceSnapshotScenario)
	t.Run("failure_gates", statioSequenceFailureScenario)
}

func statioSequenceSnapshotScenario(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					setup, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					for _, q := range []string{"CREATE SEQUENCE statio_snap_a", "CREATE SEQUENCE statio_snap_b"} {
						statioSequenceExec(t, p, b, setup, q)
					}
					a := statioSequenceOID(t, p, setup, "public.statio_snap_a")
					bID := statioSequenceOID(t, p, setup, "public.statio_snap_b")
					statioSequenceExec(t, p, b, observer, "SET stats_fetch_consistency="+mode)
					statioSequenceExec(t, p, b, observer, "BEGIN")
					defer observer.ExecContext(context.Background(), "ROLLBACK")
					statioSequenceCheck(t, p, b, observer, a, "public", "statio_snap_a", 0, 0)
					writer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer writer.Close()
					statioSequenceExec(t, p, b, writer, "BEGIN")
					pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_snap_a')", "nextval", 1)
					pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_snap_b')", "nextval", 1)
					pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_snap_b')", "nextval", 2)
					statioSequenceExec(t, p, b, writer, "COMMIT")
					// Retire the actual physical writer, not merely its pooled loan.
					statioSequenceRetire(t, writer)
					wantA, wantB, hitB := int64(0), int64(1), int64(1)
					if mode == "none" {
						wantA = 1
					}
					if mode == "snapshot" {
						wantB, hitB = 0, 0
					}
					statioSequenceCheck(t, p, b, observer, a, "public", "statio_snap_a", wantA, 0)
					statioSequenceCheck(t, p, b, observer, bID, "public", "statio_snap_b", wantB, hitB)
					clear := fixture.Select("SELECT pg_stat_clear_snapshot()", []string{"pg_stat_clear_snapshot"}, []string{"VOID"}, [][]any{{""}})
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, clear)
					statioSequenceCheck(t, p, b, observer, a, "public", "statio_snap_a", 1, 0)
					statioSequenceCheck(t, p, b, observer, bID, "public", "statio_snap_b", 1, 1)
					pending, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pending.Close()
					statioSequenceExec(t, p, b, pending, "BEGIN")
					pgSequencesScalar(t, p, b, pending, "SELECT nextval('statio_snap_a')", "nextval", 2)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, clear)
					statioSequenceCheck(t, p, b, observer, a, "public", "statio_snap_a", 1, 0)
					statioSequenceExec(t, p, b, pending, "ROLLBACK")
					statioSequenceRetire(t, pending)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Prepared(clear))
					statioSequenceCheck(t, p, b, observer, a, "public", "statio_snap_a", 1, 1)
					statioSequenceExec(t, p, b, observer, "ROLLBACK")
				})
			}
		})
	}
}

func statioSequenceFailureScenario(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE SEQUENCE statio_failure MINVALUE 1 MAXVALUE 2 CACHE 1")
			oid := statioSequenceOID(t, p, a, "public.statio_failure")
			for _, function := range []string{"nextval", "setval"} {
				for _, prepared := range []bool{false, true} {
					statioSequenceExec(t, p, b, a, "BEGIN READ ONLY")
					q := "SELECT nextval('statio_failure')"
					if function == "setval" {
						q = "SELECT setval('statio_failure',1,true)"
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + function + "() in a read-only transaction"})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
					statioSequenceExec(t, p, b, a, "ROLLBACK")
				}
			}
			statioSequenceCheck(t, p, b, observer, oid, "public", "statio_failure", 0, 0)
			if !b.PG {
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := a.QueryContext(canceled, "SELECT nextval('statio_failure')")
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled access exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("precanceled access", err)
				}
				statioSequenceCheck(t, p, b, observer, oid, "public", "statio_failure", 0, 0)
			}
			statioSequenceExec(t, p, b, a, "BEGIN")
			for n := int64(1); n <= 2; n++ {
				pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_failure')", "nextval", n)
			}
			for _, prepared := range []bool{false, true} {
				statioSequenceExec(t, p, b, a, "SAVEPOINT overflow")
				s := fixture.ImplicitError("SELECT nextval('statio_failure')", fixture.Diagnostic{Code: "2200H", Message: `nextval: reached maximum value of sequence "statio_failure" (2)`})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
				statioSequenceExec(t, p, b, a, "ROLLBACK TO overflow")
			}
			statioSequenceExec(t, p, b, a, "ROLLBACK")
			statioSequenceCheck(t, p, b, observer, oid, "public", "statio_failure", 1, 1)
		})
	}
}
