package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

const dependenciesQuery = "SELECT s.stxname,d.stxdinherit,d.stxddependencies FROM pg_statistic_ext s JOIN pg_statistic_ext_data d ON d.stxoid=s.oid ORDER BY s.stxname,d.stxdinherit"
const dependenciesFull = `{"1 => 2": 1.000000, "2 => 1": 1.000000}`
const dependenciesPartial = `{"1 => 2": 0.250000, "2 => 1": 0.500000}`

func dependenciesRows(pg bool, rows ...[]any) fixture.Step {
	return ndistinctRawResult(pg, fixture.Select(dependenciesQuery, []string{"stxname", "stxdinherit", "stxddependencies"}, []string{"NAME", "BOOL", statisticsDatabaseTypeName(pg, "pg_dependencies")}, rows), 2)
}

func TestReferenceStatisticsDependenciesCollection(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_t(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON a,b FROM dep_t", 0))
		identity := statisticExtDataID(t, s.conn, "dep_s", "dep_t")
		s.run(dependenciesRows(s.pg))
		s.run(fixture.Select("SELECT (SELECT stxddependencies FROM pg_statistic_ext_data) AS v", []string{"v"}, []string{statisticsDatabaseTypeName(s.pg, "pg_dependencies")}, [][]any{{nil}}))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(1,10),(2,20)", 3))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}))
		s.run(fixture.Select("SELECT a,b FROM dep_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), int64(20)}}))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,20)", 1))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesPartial}))
		s.run(fixture.Select("SELECT a,b FROM dep_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(1), int64(20)}, {int64(2), int64(20)}}))
		if current := statisticExtDataID(t, s.conn, "dep_s", "dep_t"); current != identity {
			t.Fatal("ANALYZE replaced definition identity", current, identity)
		}
	})
}

func TestReferenceStatisticsDependenciesNulls(t *testing.T) {
	cases := []struct {
		rows    string
		count   int64
		value   any
		ordered [][]any
	}{
		{"(1,10),(1,NULL),(2,20)", 3, `{"1 => 2": 0.333333, "2 => 1": 1.000000}`, [][]any{{int64(1), int64(10)}, {int64(1), nil}, {int64(2), int64(20)}}},
		{"(NULL,10),(NULL,20),(1,10)", 3, `{"1 => 2": 0.333333, "2 => 1": 0.333333}`, [][]any{{int64(1), int64(10)}, {nil, int64(10)}, {nil, int64(20)}}},
		{"(NULL,NULL),(NULL,NULL)", 2, dependenciesFull, [][]any{{nil, nil}, {nil, nil}}},
		{"(1,10),(1,20),(2,10),(2,20)", 4, nil, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}, {int64(2), int64(10)}, {int64(2), int64(20)}}},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				s.run(fixture.Exec("CREATE TABLE dep_t(a INT,b INT)", 0))
				s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON a,b FROM dep_t", 0))
				s.run(fixture.Exec("INSERT INTO dep_t VALUES"+c.rows, c.count))
				s.run(fixture.Select("SELECT a,b FROM dep_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, c.ordered))
				s.run(fixture.Exec("ANALYZE dep_t", 0))
				s.run(dependenciesRows(s.pg, []any{"dep_s", false, c.value}))
				s.run(fixture.Select("SELECT a,b FROM dep_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, c.ordered))
			})
		})
	}
}

func TestReferenceStatisticsDependenciesMulticolumn(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_t(a INT,b INT,c INT)", 0))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(0,0,0),(0,1,1),(1,0,1),(1,1,0)", 4))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON c,b,a FROM dep_t", 0))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, `{"1, 2 => 3": 1.000000, "1, 3 => 2": 1.000000, "2, 3 => 1": 1.000000}`}))
		s.run(fixture.Select("SELECT a,b,c FROM dep_t ORDER BY a,b,c", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(0), int64(0)}, {int64(0), int64(1), int64(1)}, {int64(1), int64(0), int64(1)}, {int64(1), int64(1), int64(0)}}))
	})
}

func TestReferenceStatisticsDependenciesDimensions(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_t(a INT,hole INT,b INT)", 0))
		s.run(fixture.Exec("ALTER TABLE dep_t DROP COLUMN hole", 0))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(1,10),(2,20),(1,20)", 4))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON b,a FROM dep_t", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_m(dependencies) ON a,(b+0) FROM dep_t", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_e(dependencies) ON (a+0),(b+0) FROM dep_t", 0))
		s.run(fixture.Exec("ALTER TABLE dep_t RENAME COLUMN b TO renamed_b", 0))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_e", false, `{"-1 => -2": 0.250000, "-2 => -1": 0.500000}`}, []any{"dep_m", false, `{"1 => -1": 0.250000, "-1 => 1": 0.500000}`}, []any{"dep_s", false, `{"1 => 3": 0.250000, "3 => 1": 0.500000}`}))
		id := statisticExtDataID(t, s.conn, "dep_s", "dep_t")
		s.run(fixture.Exec("ALTER TABLE dep_t RENAME TO dep_renamed", 0))
		if after := statisticExtDataID(t, s.conn, "dep_s", "dep_renamed"); after != id {
			t.Fatal("relation rename changed definition identity", after, id)
		}
		s.run(dependenciesRows(s.pg, []any{"dep_e", false, `{"-1 => -2": 0.250000, "-2 => -1": 0.500000}`}, []any{"dep_m", false, `{"1 => -1": 0.250000, "-1 => 1": 0.500000}`}, []any{"dep_s", false, `{"1 => 3": 0.250000, "3 => 1": 0.500000}`}))
	})
}

func TestReferenceStatisticsDependenciesRefresh(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE dep_t(a INT,b INT)", "CREATE STATISTICS dep_s ON a,b FROM dep_t", "ANALYZE dep_t"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(dependenciesRows(s.pg))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(2,20)", 2))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,20),(2,10)", 2))
		s.run(fixture.Exec("ALTER STATISTICS dep_s SET STATISTICS 0", 0))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}))
		s.run(fixture.Exec("ALTER STATISTICS dep_s SET STATISTICS 1", 0))
		s.run(fixture.Exec("ANALYZE dep_t(a)", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, nil}))
		s.run(ndistinctRows(s.pg, []any{"dep_s", false, `{"1, 2": 4}`}))
		s.run(fixture.Exec("DELETE FROM dep_t", 4))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, nil}))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(2,20)", 2))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}))
	})
}
