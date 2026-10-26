package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgTSParserColumns = []string{"oid", "prsname", "prsnamespace", "prsstart", "prstoken", "prsend", "prsheadline", "prslextype"}
var pgTSParserTypes = []string{"OID", "NAME", "OID", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC"}

// Released stock identities, independent of either backend's catalog output.
var pgTSParserRows = [][]any{{int64(3722), "default", int64(11), "prsd_start", "prsd_nexttoken", "prsd_end", "prsd_headline", "prsd_lextype"}}

func pgTSParserScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgTSParserStep(b fixture.Backend, query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, pgTSParserColumns, pgTSParserTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != len(pgTSParserColumns) {
			t.Fatal("parser descriptor width", len(columns))
		}
		for i, column := range columns {
			scan := reflect.TypeFor[string]()
			if i == 0 || i == 2 {
				scan = reflect.TypeFor[int64]()
			}
			if b.PG {
				scan = reflect.TypeFor[any]()
			}
			if column.Name() != pgTSParserColumns[i] || column.DatabaseTypeName() != pgTSParserTypes[i] || column.ScanType() != scan {
				t.Fatalf("parser descriptor %d: %q/%q/%v, want %q/%q/%v", i, column.Name(), column.DatabaseTypeName(), column.ScanType(), pgTSParserColumns[i], pgTSParserTypes[i], scan)
			}
			if nullable, known := column.Nullable(); nullable || known != !b.PG {
				t.Fatal("parser nullability", i, nullable, known)
			}
			if length, known := column.Length(); length != 0 || known {
				t.Fatal("parser length", i, length, known)
			}
			if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatal("parser decimal size", i, precision, scale, known)
			}
		}
	}
	return s
}

func pgTSParserBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, s fixture.Step) {
	t.Helper()
	t.Run("direct", func(t *testing.T) {
		fixture.PgDescriptionExecutorRows(t, p, b, executor, s)
	})
	t.Run("prepared", func(t *testing.T) {
		fixture.PgDescriptionExecutorRows(t, p, b, executor, fixture.Prepared(s))
	})
}

func TestReferencePGTSParserCatalog(t *testing.T) {
	t.Run("inventory", func(t *testing.T) {
		pgTSParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, relation := range []string{"pg_catalog.pg_ts_parser", "pg_ts_parser"} {
				t.Run(relation, func(t *testing.T) {
					t.Run("stock", func(t *testing.T) {
						pgTSParserBoth(t, p, b, b.DB, pgTSParserStep(b, "SELECT * FROM "+relation+" ORDER BY oid", pgTSParserRows))
					})
					t.Run("empty", func(t *testing.T) {
						pgTSParserBoth(t, p, b, b.DB, pgTSParserStep(b, "SELECT * FROM "+relation+" WHERE FALSE", [][]any{}))
					})
				})
			}
		})
	})

	t.Run("parameters", func(t *testing.T) {
		pgTSParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			type parameterCase struct {
				name    string
				value   any
				present bool
			}
			for _, probe := range []struct {
				name, query string
				cases       []parameterCase
			}{
				{"name", "SELECT * FROM pg_catalog.pg_ts_parser WHERE prsname=$1", []parameterCase{
					{"default", "default", true},
					{"dictionary_english", "english", false},
					{"configuration_simple", "simple", false},
					{"module_unaccent", "unaccent", false},
					{"absent", "ptsp_absent_scenario", false},
					{"null", nil, false},
					{"default_again", "default", true},
				}},
				{"oid", "SELECT * FROM pg_catalog.pg_ts_parser WHERE oid=$1", []parameterCase{
					{"default", int64(3722), true},
					{"zero", int64(0), false},
					{"maximum", int64(4294967295), false},
					{"null", nil, false},
					{"default_again", int64(3722), true},
				}},
			} {
				t.Run(probe.name, func(t *testing.T) {
					stmt, err := conn.PrepareContext(p.Ctx, probe.query)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := stmt.Close(); err != nil {
							t.Error(err)
						}
					})
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: probe.query}
					for _, tc := range probe.cases {
						t.Run(tc.name, func(t *testing.T) {
							want := [][]any{}
							if tc.present {
								want = pgTSParserRows
							}
							s := pgTSParserStep(b, probe.query, want, tc.value)
							pgTSParserBoth(t, p, b, conn, s)
							t.Run("held", func(t *testing.T) {
								// RunStep closes each cursor before the next parameter frame.
								fixture.PgDescriptionExecutorRows(t, p, b, held, s)
							})
						})
					}
				})
			}
		})
	})

	t.Run("links", func(t *testing.T) {
		pgTSParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			const query = `SELECT p.oid::bigint AS parser_oid,n.nspname,
       s.oid::bigint AS start_oid,s.proname AS start_name,s.pronamespace::bigint AS start_namespace,
       t.oid::bigint AS token_oid,t.proname AS token_name,t.pronamespace::bigint AS token_namespace,
       e.oid::bigint AS end_oid,e.proname AS end_name,e.pronamespace::bigint AS end_namespace,
       h.oid::bigint AS headline_oid,h.proname AS headline_name,h.pronamespace::bigint AS headline_namespace,
       l.oid::bigint AS lextype_oid,l.proname AS lextype_name,l.pronamespace::bigint AS lextype_namespace
FROM pg_catalog.pg_ts_parser p
JOIN pg_catalog.pg_namespace n ON n.oid=p.prsnamespace
JOIN pg_catalog.pg_proc s ON s.oid=p.prsstart::oid
JOIN pg_catalog.pg_proc t ON t.oid=p.prstoken::oid
JOIN pg_catalog.pg_proc e ON e.oid=p.prsend::oid
JOIN pg_catalog.pg_proc h ON h.oid=p.prsheadline::oid
JOIN pg_catalog.pg_proc l ON l.oid=p.prslextype::oid
WHERE p.oid=3722`
			s := fixture.Select(query,
				[]string{"parser_oid", "nspname", "start_oid", "start_name", "start_namespace", "token_oid", "token_name", "token_namespace", "end_oid", "end_name", "end_namespace", "headline_oid", "headline_name", "headline_namespace", "lextype_oid", "lextype_name", "lextype_namespace"},
				[]string{"INT8", "NAME", "INT8", "NAME", "INT8", "INT8", "NAME", "INT8", "INT8", "NAME", "INT8", "INT8", "NAME", "INT8", "INT8", "NAME", "INT8"},
				[][]any{{int64(3722), "pg_catalog", int64(3717), "prsd_start", int64(11), int64(3718), "prsd_nexttoken", int64(11), int64(3719), "prsd_end", int64(11), int64(3720), "prsd_headline", int64(11), int64(3721), "prsd_lextype", int64(11)}},
			)
			pgTSParserBoth(t, p, b, b.DB, s)
		})
	})

	t.Run("diagnostics", func(t *testing.T) {
		pgTSParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, tc := range []struct{ name, query, code, message, position string }{
				{"missing", "SELECT missing FROM pg_catalog.pg_ts_parser", "42703", `column "missing" does not exist`, "8"},
				{"unicode_position", "SELECT /*é*/ missing FROM pg_catalog.pg_ts_parser", "42703", `column "missing" does not exist`, "14"},
				{"invalid_oid", "SELECT 'bad'::oid FROM pg_catalog.pg_ts_parser WHERE FALSE", "22P02", `invalid input syntax for type oid: "bad"`, "8"},
				{"oid_overflow", "SELECT '4294967296'::oid FROM pg_catalog.pg_ts_parser WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`, "8"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, route := range []struct {
						name, stage string
						prepared    bool
					}{{"direct", "execute", false}, {"prepared", "prepare", true}} {
						t.Run(route.name, func(t *testing.T) {
							s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
							s.SelectRows, s.Prepared, s.ErrorStage = true, route.prepared, route.stage
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						})
					}
				})
			}
		})
	})
}
