package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingSmoke(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		run(fixture.Exec("DELETE FROM t AS d USING s AS x WHERE d.id=x.k", 3))
		run(deleteUsingTargets(deleteUsingSurvivors()))
		deleteUsingSourcesUnchanged(run)
	})
	t.Run("held_output", TestReferenceDeleteUsingSelectionIdentity)
	t.Run("actual_join", TestReferenceDeleteUsingSources)
	t.Run("self", TestReferenceDeleteUsingScopes)
	t.Run("late_output_failure", TestReferenceDeleteUsingAtomicity)
	t.Run("pre_cancel", TestReferenceDeleteUsingCancellation)
	t.Run("committed_survivor_controls", TestReferenceDeleteUsingControls)
}

func TestReferenceDeleteUsingSelectionIdentity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				deleteUsingSeed(run)
				q := "DELETE FROM t AS d USING s AS x WHERE d.id=x.k RETURNING d.id,d.v,x.label,old.id,new.id"
				executor := fixture.Executor(c)
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					run(deleteUsingTargets(deleteUsingOriginal()))
					executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				step := fixture.Select(q, []string{"id", "v", "label", "id", "id"}, []string{"INT4", "INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(2), int64(20), "two", int64(2), nil}, {int64(2), int64(20), "two", int64(2), nil}, {int64(3), int64(30), "three", int64(3), nil}})
				step.Bag = true
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
				run(deleteUsingTargets(deleteUsingSurvivors()))
				deleteUsingSourcesUnchanged(run)
				step.Rows = nil
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
				run(deleteUsingTargets(deleteUsingSurvivors()))
			})
		})
	}
}

func TestReferenceDeleteUsingSources(t *testing.T) {
	for _, tc := range []struct {
		name, source, predicate string
		count                   int64
		removeThree             bool
	}{
		{"on", "s JOIN r ON s.k=r.k", "d.id=s.k", 2, false},
		{"merged_alias", "s JOIN r USING(k) AS u", "d.id=u.k", 2, false},
		{"left", "s LEFT JOIN r USING(k)", "d.id=s.k", 3, true},
		{"comma", "s,r", "d.id=s.k AND s.k=r.k", 2, false},
		{"whole_alias", "(s JOIN r USING(k)) AS joined", "d.id=joined.k", 2, false},
		{"values", "(VALUES (2),(2)) AS x(k)", "d.id=x.k", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				deleteUsingSeed(run)
				run(fixture.Exec("CREATE TABLE r(k INT,tag TEXT)", 0))
				run(fixture.Exec("INSERT INTO r VALUES(2,'ok')", 1))
				run(fixture.Exec("DELETE FROM t d USING "+tc.source+" WHERE "+tc.predicate, tc.count))
				want := deleteUsingSurvivors()
				if !tc.removeThree {
					want = append(want, []any{int64(3), int64(30)})
				}
				run(deleteUsingTargets(want))
				deleteUsingSourcesUnchanged(run)
				run(fixture.Select("SELECT k,tag FROM r", []string{"k", "tag"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "ok"}}))
			})
		})
	}
	for _, tc := range []struct {
		name, source string
		count        int64
	}{
		{"product", "s", 6},
		{"empty", "(SELECT k FROM s WHERE FALSE) AS x", 0},
		{"no_leaves", "(SELECT 1 AS k) AS x", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				deleteUsingSeed(run)
				run(fixture.Exec("DELETE FROM t USING "+tc.source, tc.count))
				var want [][]any
				if tc.count == 0 {
					want = deleteUsingOriginal()
				}
				run(deleteUsingTargets(want))
				deleteUsingSourcesUnchanged(run)
			})
		})
	}
}

func TestReferenceDeleteUsingScopes(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		run(fixture.Exec("DELETE FROM t d USING t peer WHERE d.id=peer.id AND peer.v=20", 2))
		run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(3), int64(30)}, {int64(4), int64(40)}, {nil, int64(50)}}))
		deleteUsingSourcesUnchanged(run)
	})
}

// Preserve the successful self-USING and RETURNING assertion union formerly
// exercised only in TestReferenceDeleteCoreExcludedForms's PostgreSQL branch.
func TestReferenceDeleteUsingMigratedExcludedForm(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		run(fixture.Exec("DELETE FROM t USING t AS other WHERE t.id=other.id AND t.id=1", 1))
		run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Select("DELETE FROM t RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil))
	})
}
