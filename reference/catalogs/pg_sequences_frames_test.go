package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequencesFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, q := range []string{
				"CREATE SEQUENCE a",
				"CREATE SEQUENCE b",
				"CREATE TABLE filter_names(n NAME)",
				"CREATE VIEW pgsv_frames_view AS SELECT sequencename AS n,schemaname AS ns FROM pg_sequences",
				"CREATE VIEW pgsv_frames_nested AS SELECT ns,n FROM pgsv_frames_view",
			} {
				pgSequenceExec(t, p, b, a, q)
			}
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO filter_names VALUES ('a')", 1))
			cases := []struct {
				name string
				step fixture.Step
			}{
				// The two NAME operands have the same numeric slot, but belong to
				// different lexical frames. Of the two sequences only b differs
				// from the outer table's a; this expectation precedes comparison.
				{"correlated_derived", fixture.Select("SELECT (SELECT count(*) FROM (SELECT sequencename FROM pg_sequences) s WHERE s.sequencename <> t.n) AS matched FROM filter_names t", []string{"matched"}, []string{"INT8"}, [][]any{{int64(1)}})},
				{"correlated_equal", fixture.Select("SELECT (SELECT count(*) FROM (SELECT sequencename FROM pg_sequences) s WHERE s.sequencename = t.n) AS matched FROM filter_names t", []string{"matched"}, []string{"INT8"}, [][]any{{int64(1)}})},
				{"correlated_stored", fixture.Select("SELECT (SELECT count(*) FROM pgsv_frames_view s WHERE s.n <> t.n) AS matched FROM filter_names t", []string{"matched"}, []string{"INT8"}, [][]any{{int64(1)}})},
				{"correlated_nested_reordered", fixture.Select("SELECT (SELECT count(*) FROM pgsv_frames_nested s WHERE s.n <> t.n) AS matched FROM filter_names t", []string{"matched"}, []string{"INT8"}, [][]any{{int64(1)}})},
				{"derived_alias", fixture.Select("SELECT s.n FROM (SELECT sequencename FROM pg_sequences) s(n) WHERE s.n='a'", []string{"n"}, []string{"NAME"}, [][]any{{"a"}})},
				{"derived_reordered", fixture.Select("SELECT s.n,s.ns FROM (SELECT sequencename AS n,schemaname AS ns FROM pg_sequences) s WHERE s.n='a'", []string{"n", "ns"}, []string{"NAME", "NAME"}, [][]any{{"a", "public"}})},
				{"stored_reordered", fixture.Select("SELECT s.n,s.ns FROM pgsv_frames_nested s WHERE s.n='a'", []string{"n", "ns"}, []string{"NAME", "NAME"}, [][]any{{"a", "public"}})},
				{"outer_join_offset", fixture.Select("SELECT s.n FROM filter_names t JOIN (SELECT sequencename AS n FROM pg_sequences) s ON t.n=s.n WHERE s.n='a'", []string{"n"}, []string{"NAME"}, [][]any{{"a"}})},
				{"inner_join_offset", fixture.Select("SELECT s.n FROM (SELECT p.sequencename AS n FROM filter_names t JOIN pg_sequences p ON t.n=p.sequencename) s WHERE s.n='a'", []string{"n"}, []string{"NAME"}, [][]any{{"a"}})},
				{"correlated_join_offset", fixture.Select("SELECT (SELECT count(*) FROM filter_names u JOIN (SELECT sequencename AS n FROM pg_sequences) s ON TRUE WHERE s.n <> t.n) AS matched FROM filter_names t", []string{"matched"}, []string{"INT8"}, [][]any{{int64(1)}})},
				{"parameter_reordered", fixture.Select("SELECT s.n FROM (SELECT sequencename AS n,schemaname AS ns FROM pg_sequences) s WHERE s.n=$1::name", []string{"n"}, []string{"NAME"}, [][]any{{"b"}}, "b")},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					t.Run("direct", func(t *testing.T) {
						fixture.PgDescriptionExecutorRows(t, p, b, a, c.step)
					})
					t.Run("prepared", func(t *testing.T) {
						fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Prepared(c.step))
					})
				})
			}
		})
	}
}
