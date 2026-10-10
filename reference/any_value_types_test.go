package reference_test

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceAnyValueTypes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for i, tc := range []struct{ declaration, typeName, literal, text string }{
				{"SMALLINT", "INT2", "-32768", "-32768"}, {"INTEGER", "INT4", "2147483647", "2147483647"}, {"BIGINT", "INT8", "-9223372036854775808", "-9223372036854775808"},
				{"BOOL", "BOOL", "false", "false"}, {"TEXT", "TEXT", "", ""}, {"VARCHAR(4)", "VARCHAR", "xy", "xy"}, {"CHAR(4)", "BPCHAR", "x", "x"},
				{"BYTEA", "BYTEA", `\x00ff5c`, `\x00ff5c`}, {"BYTEA", "BYTEA", `\x`, `\x`},
				{"NUMERIC(10,4)", "NUMERIC", "1.2300", "1.2300"}, {"NUMERIC", "NUMERIC", "NaN", "NaN"}, {"NUMERIC", "NUMERIC", "Infinity", "Infinity"}, {"NUMERIC", "NUMERIC", "-Infinity", "-Infinity"},
				{"REAL", "FLOAT4", "1.5", "1.5"}, {"REAL", "FLOAT4", "-0", "-0"}, {"REAL", "FLOAT4", "NaN", "NaN"}, {"REAL", "FLOAT4", "Infinity", "Infinity"},
				{"DOUBLE PRECISION", "FLOAT8", "1.5", "1.5"}, {"DOUBLE PRECISION", "FLOAT8", "-0", "-0"}, {"DOUBLE PRECISION", "FLOAT8", "NaN", "NaN"}, {"DOUBLE PRECISION", "FLOAT8", "-Infinity", "-Infinity"},
				{"UUID", "UUID", "01234567-89ab-cdef-0123-456789abcdef", "01234567-89ab-cdef-0123-456789abcdef"}, {"OID", "OID", "4294967295", "4294967295"}, {"NAME", "NAME", "hello", "hello"},
				{"DATE", "DATE", "2000-02-29", "2000-02-29"}, {"DATE", "DATE", "infinity", "infinity"}, {"TIME(3)", "TIME", "12:34:56.123", "12:34:56.123"}, {"TIME WITH TIME ZONE", "TIMETZ", "12:34:56+02", "12:34:56+02"},
				{"TIMESTAMP(3)", "TIMESTAMP", "2000-02-29 12:34:56.123", "2000-02-29 12:34:56.123"}, {"TIMESTAMP", "TIMESTAMP", "-infinity", "-infinity"}, {"TIMESTAMP WITH TIME ZONE", "TIMESTAMPTZ", "2000-02-29 12:34:56+00", "2000-02-29 12:34:56+00"},
				{"INTERVAL", "INTERVAL", "1 mon 2 days 03:04:05.006", "1 mon 2 days 03:04:05.006"},
			} {
				t.Run(fmt.Sprintf("carrier_%d", i), func(t *testing.T) {
					table := fmt.Sprintf("av_type_%d", i)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE "+table+"(v "+tc.declaration+")", 0))
					run(referenceExec("INSERT INTO "+table+" VALUES(NULL),('"+tc.literal+"'),(NULL)", 3))
					textQuery := "SELECT any_value(v)::text AS retained FROM " + table
					textStep := referenceSelect(textQuery, []string{"retained"}, []string{"TEXT"}, [][]any{{tc.text}})
					if !backend.pg {
						switch tc.typeName {
						case "INT2", "NUMERIC", "FLOAT4", "FLOAT8", "UUID", "NAME":
							// These existing carriers have no explicit cast to
							// TEXT. The raw aggregate assertion below remains
							// the native exact-datum/codec witness.
							textStep = referenceError(textQuery, "0A000")
							textStep.selectRows = true
						}
					}
					anyValueRoutes(t, p, backend,
						textStep,
						referenceSelect("SELECT any_value(v) FROM "+table+" WHERE FALSE", []string{"any_value"}, []string{tc.typeName}, [][]any{{nil}}),
						referenceSelect("SELECT any_value(v) FROM "+table+" WHERE v IS NULL", []string{"any_value"}, []string{tc.typeName}, [][]any{{nil}}),
					)
					want := any(tc.text)
					switch tc.typeName {
					case "INT2", "INT4", "INT8":
						n, err := strconv.ParseInt(tc.literal, 10, 64)
						if err != nil {
							t.Fatal(err)
						}
						want = n
					case "BOOL":
						want = false
					case "BPCHAR":
						want = "x   "
					case "BYTEA":
						raw, err := hex.DecodeString(strings.TrimPrefix(tc.literal, `\x`))
						if err != nil {
							t.Fatal(err)
						}
						want = string(raw)
					case "FLOAT4", "FLOAT8":
						f, err := strconv.ParseFloat(tc.literal, 64)
						if err != nil {
							t.Fatal(err)
						}
						want = f
					case "OID":
						if !backend.pg {
							want = int64(4294967295)
						}
					case "DATE":
						if backend.pg && tc.literal != "infinity" {
							want = time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC)
						}
					case "TIME":
						if backend.pg {
							want = time.Date(0, 1, 1, 12, 34, 56, 123000000, time.UTC)
						}
					case "TIMETZ":
						if backend.pg {
							want = time.Date(0, 1, 1, 12, 34, 56, 0, time.FixedZone("", 2*60*60)).UTC()
						}
					case "TIMESTAMP":
						if tc.literal != "-infinity" {
							want = time.Date(2000, 2, 29, 12, 34, 56, 123000000, time.UTC)
						}
					case "TIMESTAMPTZ":
						want = time.Date(2000, 2, 29, 12, 34, 56, 0, time.UTC)
					}
					for _, prepared := range []bool{false, true} {
						anyValueResult(t, p, backend, "SELECT any_value(v) FROM "+table, prepared, []string{"any_value"}, []string{tc.typeName}, [][]any{{want}})
					}
				})
			}
			for _, prepared := range []bool{false, true} {
				anyValueResult(t, p, backend, "SELECT any_value('0'::regclass) FROM av_type_0", prepared, []string{"any_value"}, []string{"REGCLASS"}, [][]any{{"-"}})
			}
			for _, tc := range []struct {
				q, typ string
				want   any
			}{
				{"SELECT any_value(current_schemas(false)) FROM av_type_0", "_NAME", "{public}"},
				{"SELECT any_value(setseed(0)) FROM av_type_0", "VOID", ""},
				{"SELECT any_value(NULL::pg_node_tree) FROM av_type_0", "PG_NODE_TREE", nil},
				{"SELECT any_value(relkind) FROM pg_catalog.pg_class WHERE relname='av_type_0'", "CHAR", "r"},
			} {
				for _, prepared := range []bool{false, true} {
					anyValueResult(t, p, backend, tc.q, prepared, []string{"any_value"}, []string{tc.typ}, [][]any{{tc.want}})
				}
			}
			for _, tc := range []struct {
				cast, typ string
				arg, want any
			}{{"text", "TEXT", "", ""}, {"bytea", "BYTEA", []byte{0, 255}, string([]byte{0, 255})}, {"numeric", "NUMERIC", "1.2300", "1.2300"}, {"interval", "INTERVAL", "1 day", "1 day"}, {"uuid", "UUID", "01234567-89ab-cdef-0123-456789abcdef", "01234567-89ab-cdef-0123-456789abcdef"}} {
				for _, prepared := range []bool{false, true} {
					query := "SELECT any_value($1::" + tc.cast + ") FROM av_type_0"
					anyValueResult(t, p, backend, query, prepared, []string{"any_value"}, []string{tc.typ}, [][]any{{tc.want}}, tc.arg)
					anyValueResult(t, p, backend, query, prepared, []string{"any_value"}, []string{tc.typ}, [][]any{{nil}}, nil)
				}
			}
		})
	}
}
