package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsMetadata(t *testing.T) {
	t.Run("released_declarations", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			// Pin the released OUT declaration independently of driver metadata.
			oids := map[string]string{"OID": "26", "BOOL": "16", "INT8": "20", "TEXT": "25", "FLOAT8": "701", "NUMERIC": "1700", "TIMESTAMPTZ": "1184"}
			allargs, modes := []string{"16"}, []string{"i"}
			for _, typ := range pgssTypes {
				allargs = append(allargs, oids[typ])
				modes = append(modes, "o")
			}
			q := "SELECT p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,p.proowner=e.extowner AS owned,p.pronamespace=e.extnamespace AS placed,d.deptype::text AS membership FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass JOIN pg_extension e ON e.oid=d.refobjid WHERE n.nspname='pgss_part' AND p.proname='pg_stat_statements' AND e.extname='pg_stat_statements'"
			s.both(fixture.Select(q,
				[]string{"nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "source", "binary", "language", "owned", "placed", "membership"},
				[]string{"INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT"},
				[][]any{{int64(1), "16", int64(2249), "{" + strings.Join(allargs, ",") + "}", "{" + strings.Join(modes, ",") + "}", "{showtext," + strings.Join(pgssNames, ",") + "}", true, true, "v", "s", "pg_stat_statements_1_12", "$libdir/pg_stat_statements", "c", true, true, "e"}}))
			s.both(fixture.Select("SELECT c.relname::text AS name,c.relkind::text AS kind,c.relowner=e.extowner AS owned,c.relnamespace=e.extnamespace AS placed,d.deptype::text AS membership FROM pg_class c JOIN pg_depend d ON d.objid=c.oid AND d.classid='pg_class'::regclass AND d.refclassid='pg_extension'::regclass JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='pg_stat_statements' AND c.relname IN ('pg_stat_statements','pg_stat_statements_info') ORDER BY c.relname", []string{"name", "kind", "owned", "placed", "membership"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{"pg_stat_statements", "v", true, true, "e"}, {"pg_stat_statements_info", "v", true, true, "e"}}))
			s.both(fixture.Select("SELECT p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pgss_part' AND p.proname='pg_stat_statements_info'", []string{"nargs", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "source"}, []string{"INT4", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(0), int64(2249), "{20,1184}", "{o,o}", "{dealloc,stats_reset}", false, true, "v", "s", "pg_stat_statements_info"}}))
		})
	})
	t.Run("empty_and_strict_null", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			for _, q := range []string{
				"SELECT * FROM pgss_part.pg_stat_statements WHERE false",
				"SELECT * FROM pgss_part.pg_stat_statements(true) WHERE false",
				"SELECT * FROM pgss_part.pg_stat_statements(false) WHERE false",
				"SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)",
				"SELECT * FROM pgss_part.pg_stat_statements($1::boolean)",
			} {
				step := fixture.Select(q, pgssNames, pgssTypes, nil)
				if strings.Contains(q, "$1") {
					step.Args = []any{nil}
				}
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					metadata, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.ScalarAssertMetadata(t, s.b.PG, metadata)
					for _, column := range metadata {
						nullable, known := column.Nullable()
						if s.b.PG || strings.Contains(q, "pg_stat_statements(") {
							if nullable || known {
								t.Fatalf("%s Nullable=(%v,%v), want unknown", column.Name(), nullable, known)
							}
						} else if !nullable || !known {
							t.Fatalf("view %s Nullable=(%v,%v), want (true,true)", column.Name(), nullable, known)
						}
					}
				}
				s.both(step)
			}
			s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info WHERE false", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, nil))
			s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info() WHERE false", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, nil))
			s.both(fixture.Select("SELECT dealloc>=0 AS valid_dealloc,stats_reset IS NOT NULL AND stats_reset<=clock_timestamp() AS valid_reset FROM pgss_part.pg_stat_statements_info", []string{"valid_dealloc", "valid_reset"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		})
	})
	t.Run("showtext_and_physical_boundary", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_metadata_data(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_metadata_data VALUES(1)", 1))
			pattern := "SELECT k AS pgss_metadata_value FROM pgss_part.pgss_metadata_data%"
			s.counts(pattern, 0, 0, 0)
			s.work(fixture.Select("SELECT k AS pgss_metadata_value FROM pgss_part.pgss_metadata_data WHERE k=1", []string{"pgss_metadata_value"}, []string{"INT4"}, [][]any{{int64(1)}}))
			s.counts(pattern, 1, 1, 1)
			q := "SELECT query IS NULL AS hidden,queryid<>0 AS identified,calls,rows FROM pgss_part.pg_stat_statements(false) WHERE queryid IN (SELECT queryid FROM pgss_part.pg_stat_statements WHERE query LIKE $1)"
			s.both(fixture.Select(q, []string{"hidden", "identified", "calls", "rows"}, []string{"BOOL", "BOOL", "INT8", "INT8"}, [][]any{{true, true, int64(1), int64(1)}}, pattern))
			var conditions []string
			for _, name := range pgssNames[18:50] {
				op := "=0"
				if s.b.PG {
					op = ">=0"
				}
				conditions = append(conditions, name+op)
			}
			q = "SELECT " + strings.Join(conditions, " AND ") + " AS physical,stats_since IS NOT NULL AND stats_since<=clock_timestamp() AS since,minmax_stats_since IS NOT NULL AND minmax_stats_since<=clock_timestamp() AS minmax FROM pgss_part.pg_stat_statements WHERE query LIKE $1"
			s.both(fixture.Select(q, []string{"physical", "since", "minmax"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, pattern))
		})
	})
}
