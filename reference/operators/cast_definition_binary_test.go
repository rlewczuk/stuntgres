package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionBinarySafety(t *testing.T) {
	// All declarations use absent pairs. Never replace a protected numeric cast.
	for _, tc := range []struct {
		name       string
		ddl        []string
		query, typ string
		rows       [][]any
	}{
		{"word32", []string{"CREATE CAST (xid AS real) WITHOUT FUNCTION", "CREATE CAST (real AS xid) WITHOUT FUNCTION"}, "SELECT (($1::xid)::real)::xid::text AS v", "TEXT", [][]any{{"1065353216"}}},
		{"word64", []string{"CREATE CAST (xid8 AS float8) WITHOUT FUNCTION", "CREATE CAST (float8 AS xid8) WITHOUT FUNCTION"}, "SELECT (($1::xid8)::float8)::xid8::text AS v", "TEXT", [][]any{{"4607182418800017408"}}},
		{"text_bytes", []string{"CREATE CAST (text AS bytea) WITHOUT FUNCTION"}, "SELECT ($1::text)::bytea AS v", "BYTEA", [][]any{{"abc"}}},
		{"date_epoch", []string{"CREATE CAST (date AS real) WITHOUT FUNCTION", "CREATE CAST (real AS xid) WITHOUT FUNCTION"}, "SELECT (('2000-01-02'::date)::real)::xid::text AS v", "TEXT", [][]any{{"1"}}},
		{"date_infinity", []string{"CREATE CAST (date AS real) WITHOUT FUNCTION", "CREATE CAST (real AS xid) WITHOUT FUNCTION"}, "SELECT (('infinity'::date)::real)::xid::text AS v", "TEXT", [][]any{{"2147483647"}}},
		{"money_bits", []string{"CREATE CAST (float8 AS money) WITHOUT FUNCTION", "CREATE CAST (money AS xid8) WITHOUT FUNCTION"}, "SELECT (((1::float8)::money)::xid8)::text AS v", "TEXT", [][]any{{"4607182418800017408"}}},
		{"time_word", []string{"CREATE CAST (time AS xid8) WITHOUT FUNCTION"}, "SELECT ('00:00:01'::time)::xid8::text AS v", "TEXT", [][]any{{"1000000"}}},
		{"timestamp_epoch", []string{"CREATE CAST (timestamp AS xid8) WITHOUT FUNCTION"}, "SELECT ('2000-01-01 00:00:01'::timestamp)::xid8::text AS v", "TEXT", [][]any{{"1000000"}}},
		{"timestamptz_epoch", []string{"CREATE CAST (timestamptz AS xid8) WITHOUT FUNCTION"}, "SELECT ('2000-01-01 00:00:01+00'::timestamptz)::xid8::text AS v", "TEXT", [][]any{{"1000000"}}},
		{"json_bytes", []string{"CREATE CAST (json AS bytea) WITHOUT FUNCTION"}, `SELECT (' { "a" : 1 } '::json)::bytea AS v`, "BYTEA", [][]any{{` { "a" : 1 } `}}},
		{"lseg_box", []string{"CREATE CAST (lseg AS box) WITHOUT FUNCTION"}, "SELECT ('[(3,4),(1,2)]'::lseg)::box::text AS v", "TEXT", [][]any{{"(3,4),(1,2)"}}},
		{"lseg_box_order", []string{"CREATE CAST (lseg AS box) WITHOUT FUNCTION"}, "SELECT ('[(1,2),(3,4)]'::lseg)::box::text AS v", "TEXT", [][]any{{"(1,2),(3,4)"}}},
		{"line_circle", []string{"CREATE CAST (line AS circle) WITHOUT FUNCTION"}, "SELECT ('{1,2,3}'::line)::circle AS v", "CIRCLE", [][]any{{"<(1,2),3>"}}},
		{"circle_line", []string{"CREATE CAST (circle AS line) WITHOUT FUNCTION"}, "SELECT ('<(1,2),3>'::circle)::line::text AS v", "TEXT", [][]any{{"{1,2,3}"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				for _, q := range tc.ddl {
					pair := strings.Split(strings.TrimSuffix(strings.TrimPrefix(q, "CREATE CAST ("), ") WITHOUT FUNCTION"), " AS ")
					castDefinitionCleanup(t, p, backend, [2]string{pair[0], pair[1]})
					run(fixture.Exec(q, 0))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, tc.rows)
					s.Prepared = prepared
					if tc.typ == "CIRCLE" {
						// CIRCLE's ordinary output exposes all three physical
						// float8 fields without an unrelated CIRCLE-to-TEXT edge.
						s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
							if backend.PG {
								if _, ok := value.([]byte); !ok {
									t.Fatalf("circle PG raw %T, want []byte", value)
								}
							} else if _, ok := value.(string); !ok {
								t.Fatalf("circle native raw %T, want string", value)
							}
							return true
						}
					}
					switch tc.name {
					case "word32":
						s.Args = []any{"1065353216"}
					case "word64":
						s.Args = []any{"4607182418800017408"}
					case "text_bytes":
						s.Args = []any{"abc"}
					}
					run(s)
				}
				if tc.name == "word32" {
					run(fixture.Select("SELECT ($1::xid)::real AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{float64(1)}}, "1065353216"))
					for _, word := range []string{"0", "1", "2147483648", "2139095040", "2143289345", "2139095041", "4294967295"} {
						run(fixture.Prepared(fixture.Select(tc.query, []string{"v"}, []string{"TEXT"}, [][]any{{word}}, word)))
					}
					run(fixture.Select("SELECT (NULL::xid)::real AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{nil}}))
					run(fixture.Select("SELECT castfunc=0 AS no_routine,castmethod::text AS method,castcontext::text AS context FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"no_routine", "method", "context"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "b", "e"}}))
					run(fixture.Exec("DROP CAST (xid AS real)", 0))
					d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type xid to real")
					d.Position = "18"
					run(fixture.ErrorDiagnostic("SELECT ('1'::xid)::real", d))
				}
				if tc.name == "word64" {
					run(fixture.Select(tc.query, []string{"v"}, []string{"TEXT"}, [][]any{{"9223372036854775808"}}, "9223372036854775808"))
				}
				if tc.name == "text_bytes" {
					run(fixture.Select(tc.query, []string{"v"}, []string{"BYTEA"}, [][]any{{`\x4142`}}, `\x4142`))
				}
			}
		})
	}
	for _, tc := range []struct{ name, setup, source, target, message string }{
		{"physical", "", "integer", "bigint", "source and target data types are not physically compatible"},
		{"enum", "CREATE TYPE cast_a AS ENUM ('a'); CREATE TYPE cast_b AS ENUM ('a')", "cast_a", "cast_b", "enum data types are not binary-compatible"},
		{"composite", "CREATE TYPE cast_a AS (v integer); CREATE TYPE cast_b AS (v integer)", "cast_a", "cast_b", "composite data types are not binary-compatible"},
		{"array", "", "integer[]", "text[]", "array data types are not binary-compatible"},
		{"range", "", "int4range", "numrange", "range data types are not binary-compatible"},
		{"domain", "CREATE DOMAIN cast_a AS integer; CREATE DOMAIN cast_b AS integer", "cast_a", "cast_b", "domain data types must not be marked binary-compatible"},
		{"same", "", "integer", "integer", "source data type and target data type are the same"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				run(fixture.ErrorDiagnostic("CREATE CAST ("+tc.source+" AS "+tc.target+") WITHOUT FUNCTION", fixture.ControlsReferenceDiagnostic("42P17", tc.message)))
			}
		})
	}
}

func TestReferenceCastDefinitionCapabilityBoundary(t *testing.T) {
	for _, pair := range []struct{ source, target string }{{"jsonb", "bytea"}, {"numeric", "text"}, {"point", "interval"}, {"path", "cube"}, {"bit", "bytea"}, {"tsvector", "bytea"}} {
		t.Run(pair.source+"_"+pair.target, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				if pair.target == "cube" {
					run(fixture.Exec("CREATE EXTENSION cube", 0))
					run(fixture.Select("SELECT typname AS name,typlen AS len,typbyval AS byval,typalign AS align FROM pg_type WHERE oid IN ('path'::regtype,'cube'::regtype) ORDER BY typname", []string{"name", "len", "byval", "align"}, []string{"NAME", "INT2", "BOOL", "CHAR"}, [][]any{{"cube", int64(-1), false, "d"}, {"path", int64(-1), false, "d"}}))
				}
				q := "CREATE CAST (" + pair.source + " AS " + pair.target + ") WITHOUT FUNCTION"
				castDefinitionCleanup(t, p, backend, [2]string{pair.source, pair.target})
				if backend.PG {
					// Declaration is safe. Never execute an ABI/malformed PG datum.
					run(fixture.Exec(q, 0))
					run(fixture.Exec("DROP CAST ("+pair.source+" AS "+pair.target+")", 0))
				} else {
					run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "binary cast representation is not supported")))
				}
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource=$1::regtype AND casttarget=$2::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, pair.source, pair.target))
			}
		})
	}
	// These payloads are not representable by native validated target providers.
	// PostgreSQL must not execute the malformed relabel merely to obtain a golden.
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		if backend.PG {
			continue
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		for _, tc := range []struct{ ddl, query string }{
			{"CREATE CAST (bytea AS text) WITHOUT FUNCTION", `SELECT ('\x00'::bytea)::text`},
			{"CREATE CAST (xid AS date) WITHOUT FUNCTION", "SELECT ('2147483646'::xid)::date"},
		} {
			pair := strings.Split(strings.TrimSuffix(strings.TrimPrefix(tc.ddl, "CREATE CAST ("), ") WITHOUT FUNCTION"), " AS ")
			castDefinitionCleanup(t, p, backend, [2]string{pair[0], pair[1]})
			run(fixture.Exec(tc.ddl, 0))
			run(fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic("0A000", "binary cast value is not representable in embedded mode")))
		}
	}
}
