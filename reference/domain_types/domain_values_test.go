package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDomainBaseValues(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("SET DateStyle TO 'ISO, MDY'", 0))
		run(fixture.Exec("SET IntervalStyle TO 'postgres'", 0))
		run(fixture.Exec("CREATE TABLE domain_s.anchor (id integer)", 0))
		cases := []struct {
			name, base, literal, text string
			arg                       any
		}{
			{"small", "smallint", "7", "7", int64(7)},
			{"integer", "integer", "7", "7", int64(7)},
			{"big", "bigint", "7000000000", "7000000000", int64(7000000000)},
			{"boolean", "boolean", "TRUE", "true", true},
			{"textual", "text", "'hi'", "hi", "hi"},
			{"named", "name", "'alpha'", "alpha", "alpha"},
			{"fixed", "character(3)", "'ab'", "ab", "ab"},
			{"varying", "varchar(3)", "'abc'", "abc", "abc"},
			{"binary", "bytea", "'\\x0061ff'", "\\x0061ff", []byte{0, 97, 255}},
			{"objectid", "oid", "42", "42", int64(42)},
			{"single", "real", "1.25", "1.25", float64(1.25)},
			{"double", "double precision", "1.25", "1.25", float64(1.25)},
			{"decimal", "numeric(5,2)", "12.345", "12.35", "12.345"},
			{"identifier", "uuid", "'01234567-89ab-cdef-0123-456789abcdef'", "01234567-89ab-cdef-0123-456789abcdef", "01234567-89ab-cdef-0123-456789abcdef"},
			{"day", "date", "'2024-01-02'", "2024-01-02", time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
			{"clock", "time(3)", "'12:34:56.1234'", "12:34:56.123", "12:34:56.1234"},
			{"zclock", "timetz(3)", "'12:34:56.1234+02'", "12:34:56.123+02", "12:34:56.1234+02"},
			{"stamp", "timestamp(3)", "'2024-01-02 12:34:56.1234'", "2024-01-02 12:34:56.123", time.Date(2024, 1, 2, 12, 34, 56, 123400000, time.UTC)},
			{"zstamp", "timestamptz(3)", "'2024-01-02 12:34:56.1234+00'", "2024-01-02 12:34:56.123+00", time.Date(2024, 1, 2, 12, 34, 56, 123400000, time.UTC)},
			{"span", "interval day to second(3)", "'2 days 03:04:05.1234'", "2 days 03:04:05.123", "2 days 03:04:05.1234"},
			{"relation", "regclass", "'domain_s.anchor'", "domain_s.anchor", "domain_s.anchor"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				domain := "domain_s.d_" + tc.name
				table := "domain_s.t_" + tc.name
				run(fixture.Exec("CREATE DOMAIN "+domain+" AS "+tc.base+" NULL", 0))
				run(fixture.Exec("CREATE TABLE "+table+" (id integer, v "+domain+")", 0))
				run(fixture.Exec("INSERT INTO "+table+" VALUES (1,"+tc.literal+")", 1))
				parameter := "$1"
				if tc.name == "binary" {
					// pq knows the builtin BYTEA OID17, not a domain's dynamic OID.
					// This still performs a real base-to-domain assignment.
					parameter = "$1::bytea"
				}
				run(fixture.Exec("INSERT INTO "+table+" VALUES (2,"+parameter+")", 1, tc.arg))
				run(fixture.Prepared(fixture.Exec("INSERT INTO "+table+" VALUES (3,"+parameter+")", 1, tc.arg)))
				run(fixture.Exec("INSERT INTO "+table+" VALUES (4,NULL)", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO "+table+" VALUES (5,$1)", 1, nil)))
				run(fixture.Exec("INSERT INTO "+table+" (id) VALUES (6)", 1))
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT id,v::text AS v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), tc.text}, {int64(2), tc.text}, {int64(3), tc.text}, {int64(4), nil}, {int64(5), nil}, {int64(6), nil}})
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					domainReferenceScalar(t, p, conn, table, tc.name, backend.PG, prepared)
				}
				if tc.name == "binary" {
					// Explicit adapter boundary: native accepts inferred-domain binary
					// buffers; pq sends them as UTF8 for an unrecognized domain OID.
					for _, prepared := range []bool{false, true} {
						q := "INSERT INTO " + table + " VALUES (7,$1)"
						if backend.PG {
							want := fixture.ControlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0x00`)
							want.Context = "unnamed portal parameter $1"
							s := fixture.ErrorDiagnostic(q, want, tc.arg)
							s.Prepared = prepared
							s.ErrorStage = "execute"
							run(s)
						} else {
							s := fixture.Exec(q, 1, tc.arg)
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
							run(fixture.Select("SELECT v FROM "+table+" WHERE id=7", []string{"v"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 97, 255})}}))
							run(fixture.Exec("DELETE FROM "+table+" WHERE id=7", 1))
						}
					}
				}
			})
		}
		run(fixture.Exec("CREATE DOMAIN domain_s.tree AS pg_node_tree", 0))
		run(fixture.Exec("CREATE TABLE domain_s.t_tree (v domain_s.tree)", 0))
		run(fixture.Exec("INSERT INTO domain_s.t_tree VALUES (NULL),($1)", 2, nil))
		run(fixture.Select("SELECT v FROM domain_s.t_tree", []string{"v"}, []string{"PG_NODE_TREE"}, [][]any{{nil}, {nil}}))
		for _, prepared := range []bool{false, true} {
			want := fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type pg_node_tree")
			want.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic("INSERT INTO domain_s.t_tree VALUES ($1)", want, "payload")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Exec("CREATE DOMAIN domain_s.collated AS text COLLATE \"C\"", 0))
		run(fixture.Exec("CREATE TABLE domain_s.collation_values (v domain_s.collated)", 0))
		run(fixture.Exec("INSERT INTO domain_s.collation_values VALUES ('a'),('A'),(NULL)", 3))
		s := fixture.Select("SELECT v FROM domain_s.collation_values ORDER BY v NULLS LAST", []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"a"}, {nil}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Select("SELECT t.typname AS domain,c.collname AS collation FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_collation c ON c.oid=t.typcollation WHERE n.nspname='domain_s' AND t.typname IN ('collated','d_textual') ORDER BY t.typname", []string{"domain", "collation"}, []string{"NAME", "NAME"}, [][]any{{"collated", "C"}, {"d_textual", "default"}}))
		// Qualified quoted spelling, optional AS and current-schema resolution.
		run(fixture.Exec("CREATE DOMAIN domain_s.\"Quoted Domain\" integer", 0))
		run(fixture.Exec("SET search_path TO domain_s, public", 0))
		run(fixture.Exec("CREATE DOMAIN local_domain AS \"Quoted Domain\"", 0))
		run(fixture.Exec("CREATE TABLE local_table (v local_domain)", 0))
		run(fixture.Exec("INSERT INTO local_table VALUES ($1),(NULL)", 2, int64(9)))
		run(fixture.Select("SELECT v FROM local_table ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}, {nil}}))
	})
}

// Complete PostgreSQL metadata/raw contracts come from root's pinned oracle.
// Native representations retain the existing public base-codec contract.
func domainReferenceScalar(t *testing.T, p *fixture.Pair, conn *sql.Conn, table, family string, pg, prepared bool) {
	t.Helper()
	types := map[string]string{
		"small": "INT2", "integer": "INT4", "big": "INT8", "boolean": "BOOL",
		"textual": "TEXT", "named": "NAME", "fixed": "BPCHAR", "varying": "VARCHAR",
		"binary": "BYTEA", "objectid": "OID", "single": "FLOAT4", "double": "FLOAT8",
		"decimal": "NUMERIC", "identifier": "UUID", "day": "DATE", "clock": "TIME",
		"zclock": "TIMETZ", "stamp": "TIMESTAMP", "zstamp": "TIMESTAMPTZ",
		"span": "INTERVAL", "relation": "REGCLASS",
	}
	scan := reflect.TypeFor[string]()
	valuesByFamily := map[string]any{
		"small": int64(7), "integer": int64(7), "big": int64(7000000000), "boolean": true,
		"textual": "hi", "named": "alpha", "fixed": "ab ", "varying": "abc",
		"binary": string([]byte{0, 97, 255}), "objectid": int64(42), "single": float64(1.25), "double": float64(1.25),
		"decimal": "12.35", "identifier": "01234567-89ab-cdef-0123-456789abcdef",
		"day": "2024-01-02", "clock": "12:34:56.123", "zclock": "12:34:56.123+02",
		"stamp":  time.Date(2024, 1, 2, 12, 34, 56, 123000000, time.UTC),
		"zstamp": time.Date(2024, 1, 2, 12, 34, 56, 123000000, time.UTC),
		"span":   "2 days 03:04:05.123", "relation": "domain_s.anchor",
	}
	value := valuesByFamily[family]
	switch family {
	case "small", "integer", "big", "objectid":
		scan = reflect.TypeFor[int64]()
	case "boolean":
		scan = reflect.TypeFor[bool]()
	case "single", "double":
		scan = reflect.TypeFor[float64]()
	case "binary":
		scan = reflect.TypeFor[[]byte]()
	case "stamp", "zstamp":
		scan = reflect.TypeFor[time.Time]()
	}
	if pg {
		switch family {
		case "small":
			scan = reflect.TypeFor[int16]()
		case "integer":
			scan = reflect.TypeFor[int32]()
		case "single":
			scan = reflect.TypeFor[float32]()
		case "named", "fixed", "objectid", "decimal", "identifier", "span", "relation":
			scan = reflect.TypeFor[any]()
		case "day":
			scan = reflect.TypeFor[time.Time]()
			value = time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
		case "clock":
			scan = reflect.TypeFor[time.Time]()
			value = time.Date(0, 1, 1, 12, 34, 56, 123000000, time.UTC)
		case "zclock":
			scan = reflect.TypeFor[time.Time]()
			value = time.Date(0, 1, 1, 10, 34, 56, 123000000, time.UTC)
		}
		if family == "objectid" {
			value = "42"
		}
	}
	var wantLength, wantPrecision, wantScale int64
	wantLengthOK, wantDecimalOK := false, false
	switch family {
	case "textual":
		wantLength, wantLengthOK = math.MaxInt64, true
	case "binary":
		// pq advertises an unlimited byte length; native BYTEA length is unknown.
		if pg {
			wantLength, wantLengthOK = math.MaxInt64, true
		}
	case "fixed", "varying":
		wantLength, wantLengthOK = 3, true
	case "decimal":
		wantPrecision, wantScale, wantDecimalOK = 5, 2, true
	}
	for _, suffix := range []string{" ORDER BY id", " WHERE id>=4 ORDER BY id", " WHERE id=99"} {
		query := "SELECT v FROM " + table + suffix
		rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, conn, query, prepared)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if err := closeQuery(); err != nil {
					t.Error(err)
				}
			}()
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(columns) != 1 || columns[0].Name() != "v" || columns[0].DatabaseTypeName() != types[family] {
				t.Fatalf("domain base metadata family=%s columns=%#v", family, columns)
			}
			c := columns[0]
			length, lOK := c.Length()
			precision, scale, dOK := c.DecimalSize()
			nullable, nOK := c.Nullable()
			if c.ScanType() != scan {
				t.Fatalf("family=%s ScanType=%v want %v", family, c.ScanType(), scan)
			}
			if length != wantLength || lOK != wantLengthOK || precision != wantPrecision || scale != wantScale || dOK != wantDecimalOK || (pg && (nullable || nOK)) || (!pg && (!nullable || !nOK)) {
				t.Fatalf("family=%s metadata length=%d,%v decimal=%d,%d,%v nullable=%v,%v", family, length, lOK, precision, scale, dOK, nullable, nOK)
			}
			values := make([]any, 0)
			for rows.Next() {
				var value any
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				fixture.AssertRawValue(t, pg, types[family], value)
				values = append(values, fixture.Normalize(value))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			want := make([]any, 0)
			if suffix == " ORDER BY id" {
				want = append(want, value, value, value, nil, nil, nil)
			}
			if suffix == " WHERE id>=4 ORDER BY id" {
				want = append(want, nil, nil, nil)
			}
			if !reflect.DeepEqual(values, want) {
				t.Fatalf("family=%s query=%q values=%#v want %#v", family, query, values, want)
			}
		}()
	}
}
