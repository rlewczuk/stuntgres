package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsNDistinctTemporaryControls(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, query := range []string{
			"CREATE TABLE nd_t(a INT,b INT)",
			"CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t",
		} {
			s.run(fixture.Exec(query, 0))
		}
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(2,2)", 2))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 0", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(3,3)", 1))
		var saved bytes.Buffer
		if !s.pg {
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
		}
		// This same physical session must stay alive through both replacements.
		for _, query := range []string{
			"CREATE TEMP TABLE nd_temp(a INT,b INT)",
			"CREATE STATISTICS pg_temp.nd_temp_s(ndistinct) ON a,b FROM nd_temp",
		} {
			s.run(fixture.Exec(query, 0))
		}
		s.run(fixture.Exec("INSERT INTO nd_temp VALUES(NULL,NULL),(NULL,NULL),(1,1)", 3))
		s.run(fixture.Exec("ANALYZE nd_temp", 0))
		s.run(fixture.Exec("ALTER STATISTICS pg_temp.nd_temp_s SET STATISTICS 0", 0))
		s.run(fixture.Exec("INSERT INTO nd_temp VALUES(2,2)", 1))
		persistent := []any{"nd_s", false, `{"1, 2": 2}`}
		temporary := []any{"nd_temp_s", false, `{"1, 2": 2}`}
		s.run(ndistinctRows(s.pg, persistent, temporary))

		if !s.pg {
			replace := func(control func() error, expected ...[]any) {
				t.Helper()
				old, err := s.conn.QueryContext(s.pair.Ctx, ndistinctQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				metadata, err := old.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				checkMetadata := func(columns []*sql.ColumnType) {
					t.Helper()
					if len(columns) != 3 || columns[2].Name() != "stxdndistinct" || columns[2].DatabaseTypeName() != "PG_NDISTINCT" {
						t.Fatal("retained DATA metadata changed", columns)
					}
				}
				checkMetadata(metadata)
				if err := control(); err != nil {
					t.Fatal(err)
				}
				// Both live DATA payloads must have current-generation authority.
				// An invalid TEMP value must not poison the imported persistent row.
				s.run(ndistinctRows(false, expected...))
				for _, want := range [][]any{persistent, temporary} {
					if !old.Next() {
						t.Fatal("held DATA snapshot lost a row", old.Err())
					}
					var name, payload string
					var inherited bool
					if err := old.Scan(&name, &inherited, &payload); err != nil {
						t.Fatal(err)
					}
					if name != want[0] || inherited != want[1] || payload != want[2] {
						t.Fatal("held DATA snapshot changed", name, inherited, payload)
					}
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("held DATA snapshot gained rows or failed", old.Err())
				}
				checkMetadata(metadata)
			}
			restore := func() error { return stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())) }
			replace(restore, persistent, temporary)
			replace(func() error { return stuntgres.Reset(s.pair.Ctx, s.pair.Native) }, temporary)
			if err := restore(); err != nil {
				t.Fatal(err)
			}
		}
		// PostgreSQL runs the same SQL collection/target sequence. Only native
		// controls replace catalogs; no PostgreSQL restore API is invented.
		s.run(ndistinctRows(s.pg, persistent, temporary))
		s.run(fixture.Select("SELECT stxname,stxstattarget FROM pg_statistic_ext WHERE stxname IN('nd_s','nd_temp_s') ORDER BY stxname", []string{"stxname", "stxstattarget"}, []string{"NAME", "INT2"}, [][]any{{"nd_s", int64(0)}, {"nd_temp_s", int64(0)}}))
		s.run(fixture.Exec("ALTER STATISTICS pg_temp.nd_temp_s SET STATISTICS DEFAULT", 0))
		s.run(fixture.Exec("ANALYZE nd_temp", 0))
		s.run(ndistinctRows(s.pg, persistent, []any{"nd_temp_s", false, `{"1, 2": 3}`}))
	})
}
