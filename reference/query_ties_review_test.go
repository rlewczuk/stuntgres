package reference_test

import (
	"bytes"
	"stuntgres"
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				referenceTiesSetup(t, p, b)
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE SEQUENCE ties_exists_s START WITH 1", 0))
				q := "SELECT EXISTS(SELECT id FROM ties_t" + tc.predicate + " ORDER BY " + tc.key + " FETCH FIRST " + tc.count + " ROWS WITH TIES) AS present"
				referenceTiesBoth(t, p, b, referenceSelect(q, []string{"present"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.args...))
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT is_called FROM ties_exists_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			})
		})
	}
	t.Run("captured computed presence graph", func(t *testing.T) {
		referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
			referenceTiesSetup(t, p, b)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE VIEW ties_exists_view AS SELECT EXISTS(SELECT id FROM ties_t ORDER BY k+0 FETCH FIRST ROW WITH TIES) AS present", 0))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("ALTER TABLE ties_t RENAME COLUMN k TO renamed_k", 0))
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			referenceTiesBoth(t, p, b, referenceSelect("SELECT present FROM ties_exists_view", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
			var definition string
			if err := b.db.QueryRowContext(p.ctx, "SELECT pg_get_viewdef('ties_exists_view'::regclass)").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE VIEW ties_exists_replay AS "+definition, 0))
			referenceTiesBoth(t, p, b, referenceSelect("SELECT present FROM ties_exists_replay", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				q := "SELECT a,b FROM (VALUES(NULL::int,1),(NULL::int,2),(NULL::int,2),(NULL::int,NULL),(NULL::int,NULL)) AS t(a,b) " + tc.suffix
				referenceTiesBoth(t, p, b, referenceSelect(q, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows))
			})
		})
	}
	t.Run("hidden computed later nullable key", func(t *testing.T) {
		referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
			q := "SELECT id FROM (VALUES(1,NULL::int,1),(2,NULL::int,2),(3,NULL::int,2),(4,NULL::int,NULL),(5,NULL::int,NULL)) AS t(id,a,b) ORDER BY a NULLS FIRST,b+0 NULLS LAST FETCH FIRST 2 ROWS WITH TIES"
			s := referenceSelect(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3))
			s.bag = true
			referenceTiesBoth(t, p, b, s)
		})
	})
}
