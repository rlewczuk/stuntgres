package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTiesExists(t *testing.T) {
	for _, tc := range []struct {
		key, count, predicate string
		want                  bool
		args                  []any
	}{
		{"k+0", "1", "", true, nil},
		{"k+0", "1", " WHERE FALSE", false, nil},
		{"nextval('ties_exists_s')", "1", "", true, nil},
		{"nextval('ties_exists_s')", "1", " WHERE FALSE", false, nil},
		{"nextval('ties_exists_s')", "0", "", false, nil},
		{"k+0", "$1", "", true, []any{int64(1)}},
		{"k+0", "$1", "", false, []any{int64(0)}},
		{"k+0", "$1", "", true, []any{nil}},
	} {
		t.Run(tc.key+tc.count+tc.predicate+map[bool]string{true: "/true", false: "/false"}[tc.want], func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SEQUENCE ties_exists_s START WITH 1", 0))
				q := "SELECT EXISTS(SELECT id FROM ties_t" + tc.predicate + " ORDER BY " + tc.key + " FETCH FIRST " + tc.count + " ROWS WITH TIES) AS present"
				referenceTiesBoth(t, p, b, fixture.Select(q, []string{"present"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.args...))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT is_called FROM ties_exists_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			})
		})
	}
	t.Run("captured computed presence graph", func(t *testing.T) {
		referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			referenceTiesSetup(t, p, b)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW ties_exists_view AS SELECT EXISTS(SELECT id FROM ties_t ORDER BY k+0 FETCH FIRST ROW WITH TIES) AS present", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER TABLE ties_t RENAME COLUMN k TO renamed_k", 0))
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			referenceTiesBoth(t, p, b, fixture.Select("SELECT present FROM ties_exists_view", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
			var definition string
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('ties_exists_view'::regclass)").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW ties_exists_replay AS "+definition, 0))
			referenceTiesBoth(t, p, b, fixture.Select("SELECT present FROM ties_exists_replay", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		})
	})
}

func TestReferenceQueryTiesNullableKeys(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		rows   [][]any
	}{
		{"ORDER BY a NULLS FIRST,b NULLS LAST FETCH FIRST 2 ROWS WITH TIES", [][]any{{nil, int64(1)}, {nil, int64(2)}, {nil, int64(2)}}},
		{"ORDER BY a NULLS FIRST,b NULLS LAST FETCH FIRST 4 ROWS WITH TIES", [][]any{{nil, int64(1)}, {nil, int64(2)}, {nil, int64(2)}, {nil, nil}, {nil, nil}}},
		{"ORDER BY a NULLS FIRST,b NULLS FIRST FETCH FIRST ROW WITH TIES", [][]any{{nil, nil}, {nil, nil}}},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				q := "SELECT a,b FROM (VALUES(NULL::int,1),(NULL::int,2),(NULL::int,2),(NULL::int,NULL),(NULL::int,NULL)) AS t(a,b) " + tc.suffix
				referenceTiesBoth(t, p, b, fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows))
			})
		})
	}
	t.Run("hidden computed later nullable key", func(t *testing.T) {
		referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			q := "SELECT id FROM (VALUES(1,NULL::int,1),(2,NULL::int,2),(3,NULL::int,2),(4,NULL::int,NULL),(5,NULL::int,NULL)) AS t(id,a,b) ORDER BY a NULLS FIRST,b+0 NULLS LAST FETCH FIRST 2 ROWS WITH TIES"
			s := fixture.Select(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3))
			s.Bag = true
			referenceTiesBoth(t, p, b, s)
		})
	})
}
