package reference_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRegProcMixedRolePersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regproc_mixed_role"})
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{
				"CREATE ROLE regproc_mixed_role",
				"CREATE TYPE public.regproc_mixed_pair AS (p regprocedure,r regrole)",
				// Opaque container constants are supported; do not replace these
				// with scalar REGROLE constants hidden inside ROW or ARRAY.
				`CREATE TABLE public.regproc_mixed_values(id integer,c public.regproc_mixed_pair DEFAULT '("abs(integer)",regproc_mixed_role)'::public.regproc_mixed_pair)`,
				`CREATE VIEW public.regproc_mixed_constant AS SELECT '("abs(integer)",regproc_mixed_role)'::public.regproc_mixed_pair AS c`,
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.regproc_mixed_values(id) VALUES(1)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.regproc_mixed_values VALUES(2,NULL)", 1))
			regprocMixedRoleCheck(t, p, c, b.Recorder)
			regprocMixedRoleBoundary(t, p.Ctx, c, b.Recorder)

			if b.PG {
				for _, q := range []string{
					"DROP VIEW public.regproc_mixed_constant",
					"DROP TABLE public.regproc_mixed_values",
					"DROP TYPE public.regproc_mixed_pair",
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				// Replay the bytes actually emitted by native Dump, not a
				// hand-reconstructed PostgreSQL equivalent.
				if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("PostgreSQL rejected actual mixed routine/role Dump", err)
				}
				regprocMixedRoleCheck(t, p, c, b.Recorder)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.regproc_mixed_values(id) VALUES(3)", 1))
				regprocMixedRoleDefaultCheck(t, p, c, b.Recorder)
				regprocMixedRoleBoundary(t, p.Ctx, c, b.Recorder)
				return
			}

			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if dump.Len() == 0 {
				t.Fatal("empty mixed routine/role Dump")
			}
			emitted = bytes.Clone(dump.Bytes())
			var sourceID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'regproc_mixed_role'::regrole::oid::bigint").Scan(&sourceID); err != nil {
				t.Fatal(err)
			}

			target := fixture.NewFixture(t, false, true)
			for _, q := range []string{
				"CREATE ROLE regproc_mixed_displace_a",
				"CREATE ROLE regproc_mixed_displace_b",
				"CREATE ROLE regproc_mixed_role",
			} {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
			}
			var targetID int64
			if err := target.Memory.QueryRowContext(target.Ctx, "SELECT 'regproc_mixed_role'::regrole::oid::bigint").Scan(&targetID); err != nil {
				t.Fatal(err)
			}
			if sourceID == targetID {
				t.Fatal("target role allocation was not displaced")
			}
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal("displaced mixed routine/role Restore", err)
			}
			regprocMixedRoleCheck(t, target, target.Memory, target.MemoryRecord)
			// The restored role member must reference the target's named role,
			// not carry the source's backend-local role OID.
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select(
				"SELECT (c).r::oid::bigint AS composite_role FROM public.regproc_mixed_values WHERE id=1",
				[]string{"composite_role"}, []string{"INT8"}, [][]any{{targetID}},
			))
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.regproc_mixed_values(id) VALUES(3)", 1))
			regprocMixedRoleDefaultCheck(t, target, target.Memory, target.MemoryRecord)
			regprocMixedRoleBoundary(t, target.Ctx, target.Memory, target.MemoryRecord)
		})
	}
}

func regprocMixedRoleCheck(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	steps := []fixture.Step{
		fixture.Select(
			"SELECT id,(c).p AS p,(c).r AS r FROM public.regproc_mixed_values ORDER BY id",
			[]string{"id", "p", "r"}, []string{"INT4", "REGPROCEDURE", "REGROLE"},
			[][]any{{int64(1), "abs(integer)", "regproc_mixed_role"}, {int64(2), nil, nil}},
		),
		fixture.Select(
			"SELECT (c).r::oid='regproc_mixed_role'::regrole::oid AS composite_role,(c).p::oid=1397::oid AS composite_routine FROM public.regproc_mixed_values WHERE id=1",
			[]string{"composite_role", "composite_routine"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}},
		),
		fixture.Select(
			"SELECT (c).p AS p,(c).r AS r,(c).r::oid='regproc_mixed_role'::regrole::oid AS named_role FROM public.regproc_mixed_constant",
			[]string{"p", "r", "named_role"}, []string{"REGPROCEDURE", "REGROLE", "BOOL"}, [][]any{{"abs(integer)", "regproc_mixed_role", true}},
		),
	}
	for _, step := range steps {
		regprocBoth(t, p, x, r, step)
	}
}

func regprocMixedRoleDefaultCheck(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	regprocBoth(t, p, x, r, fixture.Select(
		"SELECT (c).p AS p,(c).r AS r,(c).r::oid='regproc_mixed_role'::regrole::oid AS composite_role FROM public.regproc_mixed_values WHERE id=3",
		[]string{"p", "r", "composite_role"}, []string{"REGPROCEDURE", "REGROLE", "BOOL"},
		[][]any{{"abs(integer)", "regproc_mixed_role", true}},
	))
}

func regprocMixedRoleBoundary(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	want := fixture.ControlsReferenceDiagnostic("0A000", "constant of the type regrole cannot be used here")
	for _, q := range []string{
		"CREATE TABLE public.regproc_mixed_forbidden(r regrole DEFAULT 'regproc_mixed_role'::regrole)",
		"CREATE VIEW public.regproc_mixed_forbidden_view AS SELECT 'regproc_mixed_role'::regrole AS r",
	} {
		fixture.RunStep(t, ctx, x, r, fixture.ErrorDiagnostic(q, want))
	}
}
