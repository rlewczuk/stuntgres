package reference_test

import (
	"database/sql"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsDependenciesTypes(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_t(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON a,b FROM dep_t", 0))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(2,20)", 2))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		id := statisticExtDataID(t, s.conn, "dep_s", "dep_t")
		s.run(statisticExtDataStep(s.pg, "SELECT * FROM pg_statistic_ext_data WHERE stxoid=$1", [][]any{{id, false, nil, dependenciesFull, nil, nil}}, id))
		step := dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull})
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			want := reflect.TypeOf("")
			if s.pg {
				want = reflect.TypeOf((*any)(nil)).Elem()
			}
			if columns[2].DatabaseTypeName() != statisticsDatabaseTypeName(s.pg, "pg_dependencies") || columns[2].ScanType() != want {
				t.Fatal(columns[2])
			}
		}
		s.run(step)
		s.run(fixture.Select("SELECT oid::bigint,typname FROM pg_type WHERE oid=3402", []string{"oid", "typname"}, []string{"INT8", "NAME"}, [][]any{{int64(3402), "pg_dependencies"}}))
	})
}

func TestReferenceStatisticsDependenciesEstimateBoundary(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_w(a INT,b TEXT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_w_s(dependencies) ON a,b FROM dep_w", 0))
		// Distinct complete logical strings are preserved; no physical-width guess is
		// made from their SQL output. PostgreSQL may omit sample items at its raw
		// varlena width boundary, so only native numeric strengths are specified here.
		x, y := strings.Repeat("long-x-", 300), strings.Repeat("long-y-", 300)
		s.run(fixture.Exec("INSERT INTO dep_w VALUES(1,$1),(1,$1),(2,$2)", 3, x, y))
		s.run(fixture.Exec("ANALYZE dep_w", 0))
		if !s.pg {
			s.run(dependenciesRows(false, []any{"dep_w_s", false, dependenciesFull}))
		} else {
			rows, err := s.conn.QueryContext(s.pair.Ctx, "SELECT stxddependencies FROM pg_statistic_ext_data")
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("missing analyzed row", rows.Err())
			}
			var value any
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != nil {
				raw, ok := value.([]byte)
				if !ok {
					t.Fatal("non-genuine dependency carrier", value)
				}
				var degrees map[string]float64
				if err := json.Unmarshal(raw, &degrees); err != nil || len(degrees) == 0 {
					t.Fatal("invalid dependency display", string(raw), err)
				}
				for direction, degree := range degrees {
					if math.IsNaN(degree) || math.IsInf(degree, 0) || degree <= 0 || degree > 1 {
						t.Fatal(direction, degree)
					}
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected analyzed rows", rows.Err())
			}
			rows.Close()
		}
		s.run(fixture.Select("SELECT a,b FROM dep_w ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), x}, {int64(1), x}, {int64(2), y}}))
	})
}

func TestReferenceStatisticsDependenciesDefinitionErrors(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_t(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON a,b FROM dep_t", 0))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(2,20)", 2))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		for _, tc := range []struct{ query, code, message string }{
			{"CREATE STATISTICS dep_bad(bogus) ON a,b FROM dep_t", "42601", `unrecognized statistics kind "bogus"`},
			{"CREATE STATISTICS dep_bad(dependencies) ON a FROM dep_t", "42P17", "extended statistics require at least 2 columns"},
			{"CREATE STATISTICS dep_bad(dependencies) ON a,a FROM dep_t", "42701", "duplicate column name in statistics definition"},
			{"ANALYZE dep_absent", "42P01", `relation "dep_absent" does not exist`},
			{"ANALYZE dep_t(missing)", "42703", `column "missing" of relation "dep_t" does not exist`},
		} {
			s.run(statisticsError(tc.query, tc.code, tc.message))
			s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}))
		}
	})
}
