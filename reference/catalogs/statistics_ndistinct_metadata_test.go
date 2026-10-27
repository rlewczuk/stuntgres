package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsNDistinctTargets(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE nd_t(a INT,b INT)", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(2,2)", 2))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", 0))
		s.run(fixture.Exec("CREATE STATISTICS nd_z(ndistinct) ON a,b FROM nd_t", 0))
		target := func(value any) {
			s.run(fixture.Select("SELECT stxstattarget FROM pg_statistic_ext WHERE stxname='nd_s'", []string{"stxstattarget"}, []string{"INT2"}, [][]any{{value}}))
		}
		target(nil)
		for _, n := range []string{"1", "100", "10000"} {
			s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS "+n, 0))
			s.run(fixture.Exec("ANALYZE nd_t", 0))
			s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 2}`}, []any{"nd_z", false, `{"1, 2": 2}`}))
		}
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 0", 0))
		target(int64(0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(3,3)", 1))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 2}`}, []any{"nd_z", false, `{"1, 2": 3}`}))
		s.run(statisticsError("ALTER STATISTICS nd_s SET STATISTICS -2", "22023", "statistics target -2 is too low"))
		target(int64(0))
		s.run(statisticsError("ALTER STATISTICS missing_nd SET STATISTICS -2", "22023", "statistics target -2 is too low"))
		s.run(statisticsError("ALTER STATISTICS missing_nd SET STATISTICS 1", "42704", `statistics object "missing_nd" does not exist`))
		s.run(fixture.Exec("ALTER STATISTICS IF EXISTS missing_nd SET STATISTICS 1", 0))
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 10001", 0))
		target(int64(10000))
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 0", 0))
		target(int64(0))
		s.run(fixture.Exec("SAVEPOINT nd_sp", 0))
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 1", 0))
		s.run(fixture.Exec("ROLLBACK TO nd_sp", 0))
		target(int64(0))
		s.run(fixture.Exec("ROLLBACK", 0))
		target(int64(10000))
		for _, n := range []string{"-1", "DEFAULT"} {
			s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS "+n, 0))
			target(nil)
		}
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 2": 3}`}))
	})
}

func TestReferenceStatisticsNDistinctHierarchy(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE nd_p(a INT,b INT)", "CREATE TABLE nd_c(extra TEXT) INHERITS(nd_p)", "CREATE STATISTICS nd_p_s(ndistinct) ON a,b FROM nd_p", "CREATE STATISTICS nd_c_s(ndistinct) ON a,b FROM nd_c"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO nd_p VALUES(1,1),(1,1)", 2))
		s.run(fixture.Exec("INSERT INTO nd_c(a,b,extra) VALUES(2,2,'x'),(2,2,'y'),(2,2,'z')", 3))
		s.run(fixture.Exec("ANALYZE ONLY nd_p", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_p_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", true, `{"1, 2": 2}`}))
		s.run(fixture.Exec("ANALYZE nd_p", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_c_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", true, `{"1, 2": 2}`}))
		s.run(fixture.Exec("INSERT INTO nd_c(a,b,extra) VALUES(3,3,'n')", 1))
		s.run(fixture.Exec("ANALYZE nd_c", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_c_s", false, `{"1, 2": 2}`}, []any{"nd_p_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", true, `{"1, 2": 2}`}))
		for _, q := range []string{"CREATE TABLE nd_part(a INT,b INT) PARTITION BY RANGE(a)", "CREATE TABLE nd_low PARTITION OF nd_part FOR VALUES FROM(0) TO(10)", "CREATE TABLE nd_high PARTITION OF nd_part FOR VALUES FROM(10) TO(20)", "CREATE STATISTICS nd_part_s(ndistinct) ON a,b FROM nd_part"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO nd_part VALUES(1,1),(1,1),(11,11)", 3))
		s.run(fixture.Exec("ANALYZE ONLY nd_part", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_c_s", false, `{"1, 2": 2}`}, []any{"nd_p_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", true, `{"1, 2": 2}`}, []any{"nd_part_s", true, `{"1, 2": 2}`}))
	})
}

func TestReferenceStatisticsNDistinctDataSchema(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Select("SELECT c.oid::bigint AS oid,c.reltype::bigint AS rowtype FROM pg_class c WHERE c.relname='pg_statistic_ext_data'", []string{"oid", "rowtype"}, []string{"INT8", "INT8"}, [][]any{{int64(3429), int64(10033)}}))
		s.run(fixture.Select("SELECT attname,atttypid::bigint AS typeoid,attnotnull FROM pg_attribute WHERE attrelid=3429 AND attnum>0 ORDER BY attnum", []string{"attname", "typeoid", "attnotnull"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"stxoid", int64(26), true}, {"stxdinherit", int64(16), true}, {"stxdndistinct", int64(3361), false}, {"stxddependencies", int64(3402), false}, {"stxdmcv", int64(5017), false}, {"stxdexpr", int64(10028), false}}))
		s.run(fixture.Select("SELECT oid::bigint AS oid,typelem::bigint AS element FROM pg_type WHERE oid=10028", []string{"oid", "element"}, []string{"INT8", "INT8"}, [][]any{{int64(10028), int64(10029)}}))
		s.run(fixture.Exec("CREATE TABLE nd_t(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,1)", 1))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		step := fixture.Select("SELECT stxdndistinct,stxddependencies,stxdmcv,stxdexpr FROM pg_statistic_ext_data", []string{"stxdndistinct", "stxddependencies", "stxdmcv", "stxdexpr"}, []string{statisticsDatabaseTypeName(s.pg, "pg_ndistinct"), statisticsDatabaseTypeName(s.pg, "pg_dependencies"), statisticsDatabaseTypeName(s.pg, "pg_mcv_list"), "_PG_STATISTIC"}, [][]any{{`{"1, 2": 1}`, nil, nil, nil}})
		step = ndistinctRawResult(s.pg, step, 0, 1, 2, 3)
		if s.pg {
			step.Types[3] = ""
		}
		s.run(fixture.Select("SELECT typtype,typcategory,typrelid::bigint AS relation FROM pg_type WHERE oid=10029", []string{"typtype", "typcategory", "relation"}, []string{"CHAR", "CHAR", "INT8"}, [][]any{{"c", "C", int64(2619)}}))
		// The catalog-only row still owns genuine type ACL metadata; it is
		// neither an arbitrary unbacked composite nor an omitted UDT grant.
		s.run(fixture.Select("SELECT udt_schema::text AS schema,udt_name::text AS name,privilege_type::text AS privilege FROM information_schema.role_udt_grants WHERE udt_schema='pg_catalog' AND udt_name='pg_statistic' AND grantee='PUBLIC'", []string{"schema", "name", "privilege"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"pg_catalog", "pg_statistic", "TYPE USAGE"}}))
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range columns {
				_, known := c.Nullable()
				if known == s.pg {
					t.Fatal("nullability adapter", c.Name(), known)
				}
			}
			if !s.pg && columns[3].ScanType() != reflect.TypeFor[string]() {
				t.Fatal("catalog statistic array scan type", columns[3].ScanType())
			}
		}
		s.run(step)
		arrayType := "_PG_STATISTIC"
		if s.pg {
			arrayType = ""
		}
		s.run(fixture.Select("SELECT stxdexpr IS NULL AS absent,COALESCE(stxdexpr,stxdexpr) AS expr FROM pg_statistic_ext_data", []string{"absent", "expr"}, []string{"BOOL", arrayType}, [][]any{{true, nil}}))
	})
}
