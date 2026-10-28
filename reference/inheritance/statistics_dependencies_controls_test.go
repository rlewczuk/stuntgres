package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsDependenciesDestination(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_t(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON a,b FROM dep_t", 0))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(1,10),(2,20),(1,20)", 4))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesPartial}))
		var saved bytes.Buffer
		if !s.pg {
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector(t.Name() + "-destination")
			if err != nil {
				t.Fatal(err)
			}
			other := sql.OpenDB(connector)
			defer other.Close()
			recorded, rec := fixture.RecordedNativePool(t, connector)
			if _, err := other.ExecContext(s.pair.Ctx, "CREATE TABLE collision(v INT); CREATE STATISTICS collision_s ON (v+1) FROM collision"); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.pair.Ctx, other, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, s.pair.Ctx, recorded, rec, dependenciesRows(false, []any{"dep_s", false, dependenciesPartial}))
		}
		// Keep the actual physical session alive; TEMP facts are preserved by
		// destination authority, independently of persistent source objects.
		s.run(fixture.Exec("CREATE TEMP TABLE dep_temp(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS pg_temp.dep_temp_s(dependencies) ON a,b FROM dep_temp", 0))
		// Analyze fractional state, then make the live population perfect
		// without ANALYZE. TEMP reownership must preserve .25/.5, not remeasure.
		s.run(fixture.Exec("INSERT INTO dep_temp VALUES(1,10),(1,10),(2,20),(1,20)", 4))
		s.run(fixture.Exec("ANALYZE dep_temp", 0))
		s.run(fixture.Exec("DELETE FROM dep_temp WHERE a=1 AND b=20", 1))
		s.run(fixture.Exec("CREATE TEMP TABLE dep_temp_zero(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS pg_temp.dep_temp_zero_s(dependencies) ON a,b FROM dep_temp_zero", 0))
		s.run(fixture.Exec("INSERT INTO dep_temp_zero VALUES(1,10),(1,20),(2,10),(2,20)", 4))
		s.run(fixture.Exec("ANALYZE dep_temp_zero", 0))
		s.run(fixture.Exec("DELETE FROM dep_temp_zero WHERE (a=1 AND b=20) OR (a=2 AND b=10)", 2))
		persistent := []any{"dep_s", false, dependenciesPartial}
		temporary := []any{"dep_temp_s", false, dependenciesPartial}
		analyzedNull := []any{"dep_temp_zero_s", false, nil}
		s.run(dependenciesRows(s.pg, persistent, temporary, analyzedNull))
		if !s.pg {
			// Exercise each distinct replacement path with genuine held DATA
			// rows. Old fractional and NULL carriers must survive independently
			// of the new generation's authenticated live catalog projection.
			replace := func(control func() error, before, after [][]any) {
				t.Helper()
				held, err := s.conn.QueryContext(s.pair.Ctx, dependenciesQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				metadata, err := held.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(metadata) != 3 || metadata[2].DatabaseTypeName() != "PG_DEPENDENCIES" {
					t.Fatal("held TEMP dependency metadata", metadata)
				}
				if err := control(); err != nil {
					t.Fatal(err)
				}
				s.run(dependenciesRows(false, after...))
				for _, want := range before {
					if !held.Next() {
						t.Fatal("held TEMP dependency row lost", held.Err())
					}
					var name string
					var inherited bool
					var payload any
					if err := held.Scan(&name, &inherited, &payload); err != nil {
						t.Fatal(err)
					}
					if name != want[0] || inherited != want[1] || payload != want[2] {
						t.Fatal("held TEMP fractional/NULL datum changed", name, inherited, payload, want)
					}
				}
				if held.Next() || held.Err() != nil {
					t.Fatal("held TEMP snapshot gained rows or failed", held.Err())
				}
				if metadata[2].DatabaseTypeName() != "PG_DEPENDENCIES" {
					t.Fatal("held TEMP dependency metadata changed", metadata[2])
				}
			}
			all := [][]any{persistent, temporary, analyzedNull}
			tempOnly := [][]any{temporary, analyzedNull}
			restore := func() error { return stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())) }
			replace(restore, all, all)
			replace(func() error { return stuntgres.Reset(s.pair.Ctx, s.pair.Native) }, all, tempOnly)
			replace(restore, tempOnly, all)
		}
		s.run(dependenciesRows(s.pg, persistent, temporary, analyzedNull))
		s.run(fixture.Select("SELECT a,b FROM dep_temp ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), int64(20)}}))
		s.run(fixture.Exec("ANALYZE dep_temp", 0))
		s.run(dependenciesRows(s.pg, persistent, []any{"dep_temp_s", false, dependenciesFull}, analyzedNull))
	})
}
