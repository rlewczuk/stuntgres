package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceDMLFeatureSmoke(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceDMLDiamond(s)
		s.exec("INSERT INTO p VALUES(3,'parent')", 1)
		s.values("SELECT * FROM ONLY p ORDER BY a", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "parent"}, {int64(7), "same"}, {int64(7), "same"}})
		s.exec("DELETE FROM ONLY p WHERE a=3", 1)
		const q = "UPDATE p SET a=a+$1 RETURNING a,x"
		held, err := s.c.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer held.Close()
		var eager fixture.SequenceFrozenRows
		// The retained eager cursor owns its actual recording independently of
		// later executions of this same held SQL against the restored catalog.
		eagerRecord := &fixture.Recorder{RawBackend: s.record.RawBackend}
		step := fixture.Select(q, []string{"a", "x"}, []string{"INT4", "TEXT"}, inheritanceDMLFive(8), int64(1))
		step.Bag = true
		step.CheckMetadata = inheritanceDMLMetadata(s, step.Columns, step.Types)
		if s.b.PG {
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.record, step)
		} else {
			rows, err := held.QueryContext(context.WithValue(s.p.Ctx, fixture.RecorderKey{}, eagerRecord), int64(1))
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			eager = fixture.SequenceFrozenRows{Executor: s.c, Rows: rows, Query: q}
		}
		s.values("SELECT * FROM d", []string{"y", "a", "x", "z"}, []string{"BOOL", "INT4", "TEXT", "INT8"}, [][]any{{true, int64(8), "same", int64(9)}, {true, int64(8), "same", int64(9)}})
		s.exec("UPDATE ONLY p SET a=a+1", 2)
		s.fail("UPDATE p SET a=a+10", inheritanceCheckDiagnostic("d", "d_limit", "18, same"))
		checks := []fixture.Step{
			fixture.Select("SELECT * FROM p ORDER BY a", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "same"}, {int64(8), "same"}, {int64(8), "same"}, {int64(9), "same"}, {int64(9), "same"}}),
			fixture.Select("SELECT * FROM ONLY p", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "same"}, {int64(9), "same"}}),
			fixture.Select("SELECT * FROM ONLY d", []string{"y", "a", "x", "z"}, []string{"BOOL", "INT4", "TEXT", "INT8"}, [][]any{{true, int64(8), "same", int64(9)}, {true, int64(8), "same", int64(9)}}),
			fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='d'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"d_limit", true, int64(0), false}}),
		}
		inheritanceMutationRoundTrip(s, checks)
		// Execute the same original handle against the genuinely restored graph,
		// without changing its signature or re-preparing it.
		// A zero delta keeps the independent pre-delete bag 2x9 plus 3x8.
		step.Args = []any{int64(0)}
		step.Rows = [][]any{{int64(8), "same"}, {int64(8), "same"}, {int64(8), "same"}, {int64(9), "same"}, {int64(9), "same"}}
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.record, step)
		s.exec("DELETE FROM ONLY p", 2)
		s.exec("DELETE FROM p", 3)
		if !s.b.PG {
			step.Rows = inheritanceDMLFive(8)
			step.Args = []any{int64(1)}
			fixture.RunStep(s.t, s.p.Ctx, eager, eagerRecord, step)
		}
		s.values("SELECT * FROM p", []string{"a", "x"}, []string{"INT4", "TEXT"}, nil)
	})
}
