package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

var statisticExtDataNames = []string{"stxoid", "stxdinherit", "stxdndistinct", "stxddependencies", "stxdmcv", "stxdexpr"}

func statisticExtDataStep(pg bool, query string, rows [][]any, args ...any) fixture.Step {
	types := []string{"OID", "BOOL", statisticsDatabaseTypeName(pg, "pg_ndistinct"), statisticsDatabaseTypeName(pg, "pg_dependencies"), statisticsDatabaseTypeName(pg, "pg_mcv_list"), "_PG_STATISTIC"}
	if pg {
		types[5] = ""
	}
	// The shared fixture normalizes pq OID bytes to decimal text.
	if pg {
		adapted := make([][]any, len(rows))
		for i, row := range rows {
			adapted[i] = append([]any(nil), row...)
			adapted[i][0] = strconv.FormatInt(row[0].(int64), 10)
		}
		rows = adapted
	}
	s := ndistinctRawResult(pg, fixture.Select(query, statisticExtDataNames, types, rows, args...), 2, 3, 4, 5)
	raw := s.CheckRaw
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 0 {
			fixture.OidReferenceValue(t, pg, "OID", value)
			return true
		}
		return raw(t, column, row, value)
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != 6 {
			t.Fatal("DATA width", len(columns))
		}
		for i, c := range columns {
			nullable, known := c.Nullable()
			if c.Name() != statisticExtDataNames[i] || c.DatabaseTypeName() != types[i] || known == pg || !pg && nullable != (i >= 2) {
				t.Fatalf("DATA column %d: %v nullable(%t,%t)", i, c, nullable, known)
			}
		}
	}
	return s
}

// Resolve the definition through its real namespace and owning relation, never
// through an allocator assumption or a DATA row that might not exist yet.
func statisticExtDataID(t *testing.T, conn *sql.Conn, name, table string) int64 {
	t.Helper()
	return fixture.DeparseDefinitionID(t, conn, "SELECT s.oid FROM pg_catalog.pg_statistic_ext s JOIN pg_catalog.pg_class c ON c.oid=s.stxrelid JOIN pg_catalog.pg_namespace n ON n.oid=s.stxnamespace WHERE s.stxname=$1 AND c.relname=$2 AND n.nspname='public'", name, table)
}

func TestReferencePGStatisticExtDataReads(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE stxd_t(a INT,b INT)", "CREATE STATISTICS stxd_s(ndistinct) ON a,b FROM stxd_t", "CREATE TABLE stxd_unbuilt(a INT,b INT)", "CREATE STATISTICS stxd_unbuilt_s(ndistinct) ON a,b FROM stxd_unbuilt"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO stxd_t VALUES(1,1),(1,1),(2,2),(2,2)", 4))
		id := statisticExtDataID(t, s.conn, "stxd_s", "stxd_t")
		unbuilt := statisticExtDataID(t, s.conn, "stxd_unbuilt_s", "stxd_unbuilt")
		for _, built := range []bool{false, true} {
			if built {
				s.run(fixture.Exec("ANALYZE stxd_t", 0))
			}
			var want [][]any
			if built {
				want = [][]any{{id, false, `{"1, 2": 2}`, nil, nil, nil}}
			}
			for _, source := range []string{"pg_catalog.pg_statistic_ext_data", "pg_statistic_ext_data"} {
				s.run(statisticExtDataStep(s.pg, "SELECT * FROM "+source+" WHERE stxoid=$1", want, id))
				s.run(statisticExtDataStep(s.pg, "SELECT * FROM "+source+" WHERE stxoid=$1", nil, unbuilt))
				s.run(statisticExtDataStep(s.pg, "SELECT * FROM "+source+" WHERE FALSE", nil))
			}
		}
	})
}

func TestReferencePGStatisticExtDataShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.OidReferenceRows(t, p.Ctx, b, conn, s) }
			run(fixture.Select("SELECT oid,relname,relnamespace,relkind,relpersistence,relnatts,relhasindex,relowner FROM pg_catalog.pg_class WHERE oid=3429", []string{"oid", "relname", "relnamespace", "relkind", "relpersistence", "relnatts", "relhasindex", "relowner"}, []string{"OID", "NAME", "OID", "CHAR", "CHAR", "INT2", "BOOL", "OID"}, [][]any{{int64(3429), "pg_statistic_ext_data", int64(11), "r", "p", int64(6), true, int64(10)}}))
			attrs := [][]any{{int64(1), "stxoid", int64(26), true}, {int64(2), "stxdinherit", int64(16), true}, {int64(3), "stxdndistinct", int64(3361), false}, {int64(4), "stxddependencies", int64(3402), false}, {int64(5), "stxdmcv", int64(5017), false}, {int64(6), "stxdexpr", int64(10028), false}}
			run(fixture.Select("SELECT attnum,attname,atttypid,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid=3429 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull"}, []string{"INT2", "NAME", "OID", "BOOL"}, attrs))
			row := fixture.DeparseDefinitionID(t, conn, "SELECT reltype FROM pg_catalog.pg_class WHERE oid=3429")
			array := fixture.DeparseDefinitionID(t, conn, "SELECT typarray FROM pg_catalog.pg_type WHERE oid=$1", row)
			if row == array || !b.PG && (row != 10033 || array != 10032) {
				t.Fatal("DATA composite identities", row, array)
			}
			run(fixture.Select("SELECT typname,typtype,typrelid,typnamespace,typarray FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typtype", "typrelid", "typnamespace", "typarray"}, []string{"NAME", "CHAR", "OID", "OID", "OID"}, [][]any{{"pg_statistic_ext_data", "c", int64(3429), int64(11), array}}, row))
			run(fixture.Select("SELECT typname,typelem,typnamespace FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typelem", "typnamespace"}, []string{"NAME", "OID", "OID"}, [][]any{{"_pg_statistic_ext_data", row, int64(11)}}, array))
		})
	}
}

func TestReferencePGStatisticExtDataDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, source := range []string{"pg_catalog.pg_statistic_ext_data", "pg_statistic_ext_data"} {
				for _, name := range []string{"oid", "missing"} {
					for _, prepared := range []bool{false, true} {
						want := fixture.ControlsReferenceDiagnostic("42703", fmt.Sprintf("column %q does not exist", name))
						want.Position = "8"
						s := fixture.ErrorDiagnostic("SELECT "+name+" FROM "+source, want)
						s.SelectRows, s.ErrorStage, s.Prepared = true, "execute", prepared
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, statisticExtDataStep(b.PG, "SELECT * FROM "+source+" WHERE FALSE", nil))
					}
				}
			}
		})
	}
}
