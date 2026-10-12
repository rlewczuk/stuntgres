package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprVirtualBoundMerge(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TYPE virtual_merge_pair AS (a integer[])`,
			`CREATE TABLE merge_destination(k integer,v virtual_merge_pair)`,
			`CREATE TABLE merge_source(k integer,g integer GENERATED ALWAYS AS (k+1) VIRTUAL)`,
			`CREATE TABLE merge_keys(k integer)`,
			`CREATE TABLE merge_recipe(k integer,g integer GENERATED ALWAYS AS (10/(2-k)) VIRTUAL)`,
			`CREATE TABLE merge_sentinel(k integer)`,
		)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_sentinel VALUES(77)`, 1))
		count := int64(1)
		if b.PG {
			// Existing public MERGE fixture convention for lib/pq command tags.
			count = 0
		}
		q := `MERGE INTO merge_destination d USING merge_source s ON d.k=s.k WHEN MATCHED THEN UPDATE SET v.a[s.g]=9`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_destination VALUES(1,NULL)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_source(k) VALUES(1)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, count))
		compositeExprBoth(t, p, b, c, `SELECT k,(v).a::text AS a FROM merge_destination ORDER BY k`, []string{"k", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "[2:2]={9}"}})

		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		// Each execution replaces both source and destination rows. The held
		// target must evaluate the new source recipe, not a prior bound value.
		for _, sample := range []struct {
			k int
			a string
		}{{1, "[2:2]={9}"}, {4, "[5:5]={9}"}, {7, "[8:8]={9}"}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM merge_destination`, 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM merge_source`, 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_destination VALUES($1,NULL)`, 1, sample.k))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_source(k) VALUES($1)`, 1, sample.k))
			result, err := held.ExecContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, count)
			compositeExprBoth(t, p, b, c, `SELECT k,(v).a::text AS a FROM merge_destination ORDER BY k`, []string{"k", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(sample.k), sample.a}})
		}

		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM merge_destination`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM merge_source`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_destination VALUES(1,NULL),(2,NULL)`, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_source(k) VALUES(1)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_keys VALUES(1),(2)`, 2))
		unchanged := func(t *testing.T) {
			t.Helper()
			compositeExprBoth(t, p, b, c, `SELECT k,v IS NULL AS whole_null,(v).a::text AS a FROM merge_destination ORDER BY k`, []string{"k", "whole_null", "a"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), true, nil}, {int64(2), true, nil}})
			compositeExprBoth(t, p, b, c, `SELECT k FROM merge_sentinel`, []string{"k"}, []string{"INT4"}, [][]any{{int64(77)}})
			compositeExprBoth(t, p, b, c, `SELECT 42 AS recovered`, []string{"recovered"}, []string{"INT4"}, [][]any{{int64(42)}})
		}
		// A real outer join supplies a present key but absent virtual-source
		// occurrence. NULL extension must win over evaluating its k+1 recipe.
		absent := `MERGE INTO merge_destination d USING (merge_keys x LEFT JOIN merge_source s ON x.k=s.k) ON d.k=x.k WHEN MATCHED THEN UPDATE SET v.a[s.g]=9`
		fixture.CompositeReferenceError(t, p, c, absent, "execute", fixture.CompositeReferenceDiagnostic("22004", "array subscript in assignment must not be null"))
		unchanged(t)

		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO merge_recipe(k) VALUES(1),(2)`, 2))
		// Only ordinary keys are projected by the ordered source. Reading g
		// anywhere outside the nested bound would conceal missing target demand.
		late := `MERGE INTO merge_destination d USING ((SELECT k FROM merge_keys ORDER BY k) x JOIN merge_recipe s ON x.k=s.k) ON d.k=x.k WHEN MATCHED THEN UPDATE SET v.a[s.g]=9`
		fixture.CompositeReferenceError(t, p, c, late, "execute", fixture.CompositeReferenceDiagnostic("22012", "division by zero"))
		unchanged(t)
		// Recovery also executes the original held mutation on this connection.
		result, err := held.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, count)
		compositeExprBoth(t, p, b, c, `SELECT k,(v).a::text AS a FROM merge_destination ORDER BY k`, []string{"k", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "[2:2]={9}"}, {int64(2), nil}})
		compositeExprBoth(t, p, b, c, `SELECT k FROM merge_sentinel`, []string{"k"}, []string{"INT4"}, [][]any{{int64(77)}})
	})
}
