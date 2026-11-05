package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetDiagnostics(t *testing.T) {
	// REL_18_6 parser/input records confirmed against pinned PostgreSQL 18.6.
	// Prepare/execute phases and every exposed logical field remain asserted.
	cases := []struct {
		args, code, message string
		position            int
		hint                string
	}{
		{"'x'", "22P02", `invalid input syntax for type oid: "x"`, 33, ""},
		{"0,0,'x'", "22P02", `invalid input syntax for type bigint: "x"`, 37, ""},
		{"0,0,0,'x'", "22P02", `invalid input syntax for type boolean: "x"`, 39, ""},
		{"'4294967296'", "22003", `value "4294967296" is out of range for type oid`, 33, ""},
		{"0,0,'9223372036854775808'", "22003", `value "9223372036854775808" is out of range for type bigint`, 37, ""},
		{"true", "42883", "function pg_stat_statements_reset(boolean) does not exist", 8, "resolution"},
		{"'0'::text", "42883", "function pg_stat_statements_reset(text) does not exist", 8, "resolution"},
		{"0,0,0,false,0", "42883", "function pg_stat_statements_reset(integer, integer, integer, boolean, integer) does not exist", 8, "resolution"},
		{"missing=>0", "42883", "function pg_stat_statements_reset(missing => integer) does not exist", 8, "resolution"},
		{"userid=>0,userid=>0", "42601", `argument name "userid" used more than once`, 0, ""},
		{"userid=>0,0", "42601", "positional argument cannot follow named argument", 0, ""},
		{"-1::bigint", "22003", "OID out of range", 0, ""},
		{"4294967296::bigint", "22003", "OID out of range", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.args, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				pgssResetSeed(s)
				s.exec("SET search_path=pgss_part,pg_catalog")
				before, info := pgssResetSnapshot(s), pgssResetInfo(s)
				q := "SELECT pg_stat_statements_reset(" + c.args + ");"
				want := fixture.ControlsReferenceDiagnostic(c.code, c.message)
				want.Position = fixture.Position(c.position)
				if c.hint != "" {
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				if strings.Contains(c.args, "userid=>0,userid") {
					want.Position = fixture.Position(strings.LastIndex(q, "userid") + 1)
				}
				if c.args == "userid=>0,0" {
					want.Position = fixture.Position(strings.LastIndex(q, "0") + 1)
				}
				for _, prepared := range []bool{false, true} {
					step := fixture.ErrorDiagnostic(q, want)
					step.Prepared, step.SelectRows = prepared, true
					step.ErrorStage = "execute"
					if prepared && c.message != "OID out of range" {
						step.ErrorStage = "prepare"
					}
					s.run(step)
					pgssResetEqual(s.t, pgssResetSnapshot(s), before)
					pgssResetEqual(s.t, pgssResetInfo(s), info)
				}
			})
		})
	}
	t.Run("bound_input", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssResetSeed(s)
			before, info := pgssResetSnapshot(s), pgssResetInfo(s)
			q := "SELECT pgss_part.pg_stat_statements_reset($1)"
			held := pgssResetHeld(s, q)
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type oid: "x"`)
			// Pinned PostgreSQL 18.6 redacts the malformed parameter's text
			// in its genuine Bind-input diagnostic context.
			want.Context = "unnamed portal parameter $1 = '...'"
			step := fixture.ErrorDiagnostic(q, want, "x")
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(s.t, s.p.Ctx, held, s.b.Recorder, step)
			pgssResetEqual(s.t, pgssResetSnapshot(s), before)
			pgssResetEqual(s.t, pgssResetInfo(s), info)
		})
	})
	t.Run("native_pre_canceled_and_expired", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			// A database/sql native adapter boundary, not a lib/pq promise:
			// pq can race delivery and invalidate its transport on cancellation.
			// Shared admitted pre-body cancellation is exercised below.
			if s.b.PG {
				return
			}
			pgssResetSeed(s)
			before, info := pgssResetSnapshot(s), pgssResetInfo(s)
			held := pgssResetHeld(s, pgssResetCall)
			for _, expired := range []bool{false, true} {
				ctx, cancel := context.WithCancel(s.p.Ctx)
				want := context.Canceled
				if expired {
					cancel()
					ctx, cancel = context.WithDeadline(s.p.Ctx, time.Now().Add(-time.Second))
					want = context.DeadlineExceeded
				} else {
					cancel()
				}
				for _, x := range []fixture.Executor{s.observer, held} {
					rows, err := x.QueryContext(ctx, pgssResetCall)
					if rows != nil {
						rows.Close()
					}
					if !errors.Is(err, want) {
						cancel()
						s.t.Fatalf("pre-body context boundary: %v, want %v", err, want)
					}
					pgssResetEqual(s.t, pgssResetSnapshot(s), before)
					pgssResetEqual(s.t, pgssResetInfo(s), info)
				}
				cancel()
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "before_body_direct"
		if prepared {
			name = "before_body_prepared"
		}
		t.Run(name, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				a, b := pgssResetSeed(s)
				s.work(fixture.Exec("SET pg_stat_statements.track=none", 0))
				s.exec("CREATE TABLE pgss_part.pgss_reset_cancel_args(userid oid,dbid oid,queryid bigint,minmax_only boolean)")
				s.run(fixture.Exec("INSERT INTO pgss_part.pgss_reset_cancel_args VALUES(0,0,0,false)", 1))
				q := "SELECT pgss_part.pg_stat_statements_reset(userid,dbid,queryid,minmax_only) FROM pgss_part.pgss_reset_cancel_args"
				var x fixture.Executor = s.worker
				if prepared {
					stmt, err := s.worker.PrepareContext(s.p.Ctx, q)
					if err != nil {
						s.t.Fatal(err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{Executor: s.worker, Statement: stmt, SQL: q}
				}
				// The cancellation helper's genuine PostgreSQL Admin queries
				// monitor pg_stat_activity/pg_locks and call pg_cancel_backend.
				// Those successful queries legitimately add their own history.
				// Preserve every one of the 52 fields of both seeded targets,
				// not unrelated monitor counters; reset effects on either target
				// (including disappearance or minmax-only changes) must fail.
				targets := "SELECT * FROM pgss_part.pg_stat_statements WHERE query IN ($1,$2) AND toplevel AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY queryid"
				before, info := pgssResetRead(s, targets, a, b), pgssResetInfo(s)
				if len(before) != 2 {
					s.t.Fatalf("cancellation seed targets=%d, want both A and B", len(before))
				}
				s.exec("BEGIN")
				defer s.observer.ExecContext(s.p.Ctx, "ROLLBACK")
				s.exec("LOCK TABLE pgss_part.pgss_reset_cancel_args IN ACCESS EXCLUSIVE MODE")
				// The reset's non-NULL arguments come from the locked relation:
				// real lock admission proves the function body cannot have run.
				// The shared helper joins the operation and asserts complete
				// PG57014 diagnostics / native ordinary context cancellation.
				fixture.RelationLockActiveCancel(s.t, s.p, s.b, s.worker, q, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, q)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				s.exec("ROLLBACK")
				after := pgssResetRead(s, targets, a, b)
				if len(after) != 2 {
					s.t.Fatalf("cancellation retained targets=%d, want both A and B", len(after))
				}
				pgssResetEqual(s.t, after, before)
				pgssResetEqual(s.t, pgssResetInfo(s), info)
			})
		})
	}
}
