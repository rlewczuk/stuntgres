package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordMetadata(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_meta AS (a int,b varchar(3),c numeric(4,1))`)
		for _, tc := range []struct {
			q            string
			names, types []string
			rows         [][]any
			args         []any
		}{
			{`SELECT jsonb_populate_record(NULL::jbr_meta,'{}'::jsonb) AS v`, []string{"v"}, []string{""}, [][]any{{"(,,)"}}, nil},
			{`SELECT jsonb_populate_record(NULL::jbr_meta,NULL::jsonb) AS v`, []string{"v"}, []string{""}, [][]any{{nil}}, nil},
			{`SELECT jsonb_populate_recordset(NULL::jbr_meta,'[]'::jsonb) AS v`, []string{"v"}, []string{""}, nil, nil},
			{`SELECT jsonb_to_record(NULL::jsonb) AS r`, []string{"r"}, []string{"RECORD"}, [][]any{{nil}}, nil},
			{`SELECT r FROM jsonb_to_record($1::jsonb) AS r(a int,b varchar(3),c numeric(4,1))`, []string{"r"}, []string{"RECORD"}, [][]any{{"(1,abc,12.3)"}}, []any{`{"a":1,"b":"abc","c":12.34}`}},
			{`SELECT * FROM jsonb_to_record($1::jsonb) AS r(a int,b varchar(3),c numeric(4,1))`, []string{"a", "b", "c"}, []string{"INT4", "VARCHAR", "NUMERIC"}, [][]any{{nil, nil, nil}}, []any{nil}},
			{`SELECT * FROM jsonb_to_recordset($1::jsonb) AS r(a int,b varchar(3),c numeric(4,1))`, []string{"a", "b", "c"}, []string{"INT4", "VARCHAR", "NUMERIC"}, nil, []any{`[]`}},
			{`SELECT * FROM ROWS FROM(jsonb_to_recordset($1::jsonb) AS (a int,b varchar(3),c numeric(4,1))) WITH ORDINALITY AS r(a,b,c,n)`, []string{"a", "b", "c", "n"}, []string{"INT4", "VARCHAR", "NUMERIC", "INT8"}, [][]any{{int64(1), "abc", "12.3", int64(1)}}, []any{`[{"a":1,"b":"abc","c":12.34}]`}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, tc.names, tc.types, tc.rows, tc.args...)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(columns) != len(tc.names) {
						t.Fatalf("metadata width=%d want=%d", len(columns), len(tc.names))
					}
					for i, ct := range columns {
						scan := reflect.TypeFor[string]()
						switch tc.types[i] {
						case "INT4":
							scan = reflect.TypeFor[int64]()
							if b.PG {
								scan = reflect.TypeFor[int32]()
							}
						case "INT8":
							scan = reflect.TypeFor[int64]()
						case "", "RECORD", "NUMERIC":
							if b.PG {
								scan = reflect.TypeFor[any]()
							}
						}
						if ct.Name() != tc.names[i] || ct.DatabaseTypeName() != tc.types[i] || ct.ScanType() != scan {
							t.Errorf("metadata column=%d got=%q/%q/%v want=%q/%q/%v", i, ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), tc.names[i], tc.types[i], scan)
						}
						wantLength, hasLength := int64(0), false
						if tc.types[i] == "VARCHAR" {
							wantLength, hasLength = 3, true
						}
						if length, known := ct.Length(); length != wantLength || known != hasLength {
							t.Errorf("column=%d Length=%d/%t want=%d/%t", i, length, known, wantLength, hasLength)
						}
						precision, scale, decimal := int64(0), int64(0), false
						if tc.types[i] == "NUMERIC" {
							precision, scale, decimal = 4, 1, true
						}
						if gotP, gotS, known := ct.DecimalSize(); gotP != precision || gotS != scale || known != decimal {
							t.Errorf("column=%d DecimalSize=%d,%d/%t want=%d,%d/%t", i, gotP, gotS, known, precision, scale, decimal)
						}
						if nullable, known := ct.Nullable(); nullable || known {
							t.Errorf("column=%d Nullable=%t/%t want=false/false", i, nullable, known)
						}
					}
				}
				s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
					cells := make([]any, len(tc.names))
					dest := make([]any, len(cells))
					for i := range cells {
						dest[i] = &cells[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, v := range cells {
						if tc.types[i] == "" || tc.types[i] == "RECORD" {
							if v != nil {
								if b.PG {
									if _, ok := v.([]byte); !ok {
										t.Fatalf("composite raw %T want []byte", v)
									}
								} else {
									if _, ok := v.(string); !ok {
										t.Fatalf("composite raw %T want string", v)
									}
								}
							}
						}
						if !reflect.DeepEqual(fixture.Normalize(v), tc.rows[index][i]) {
							t.Errorf("rescan row=%d column=%d raw=%#v want=%#v", index, i, v, tc.rows[index][i])
						}
					}
				}
				jsonbRecordCompositeBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceJSONBRecordHeld(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_held AS (a int,b text)`)
		for _, tc := range []struct {
			q   string
			set bool
		}{
			{`SELECT * FROM jsonb_to_record($1::jsonb) AS r(a int,b text)`, false},
			{`SELECT * FROM jsonb_to_recordset($1::jsonb) AS r(a int,b text)`, true},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_held,$1)`, false},
			{`SELECT * FROM jsonb_populate_recordset(NULL::jbr_held,$1)`, true},
		} {
			t.Run(tc.q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
				for _, v := range []struct {
					input any
					rows  [][]any
					bad   bool
				}{
					{`{"a":1,"b":"one"}`, [][]any{{int64(1), "one"}}, false},
					{nil, [][]any{{nil, nil}}, false},
					{`{}`, [][]any{{nil, nil}}, false},
					{`{"a":"bad"}`, nil, true},
					{`{"a":3,"b":"three"}`, [][]any{{int64(3), "three"}}, false},
				} {
					input, want := v.input, v.rows
					if tc.set {
						if input == nil || input == `{}` {
							input = `[]`
							want = nil
						} else {
							input = "[" + input.(string) + "]"
						}
					}
					if v.bad {
						jsonKeysError(t, p, b, held, tc.q, false, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), "execute", input)
						continue
					}
					s := fixture.Select(tc.q, []string{"a", "b"}, []string{"INT4", "TEXT"}, want, input)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		}
		// Bound byte ownership is observed via persisted state after the caller mutates it.
		jsonbRecordSetup(t, p, b, c, `CREATE TABLE jbr_held_docs(j jsonb)`)
		buffer := []byte(`{"a":8,"b":"owned"}`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO jbr_held_docs VALUES($1)`, 1, buffer)))
		for i := range buffer {
			buffer[i] = 'x'
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT r.a,r.b FROM jbr_held_docs d,jsonb_populate_record(NULL::jbr_held,d.j) r`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "owned"}}))
		// Rename identity uses an admitted physical table-rowtype operation;
		// standalone composite ADD ATTRIBUTE is tested independently below.
		t.Run("held rename identity", func(t *testing.T) {
			jsonbRecordSetup(t, p, b, c, `CREATE TABLE jbr_held_identity(a int,b text)`)
			var before int64
			if err := c.QueryRowContext(p.Ctx, `SELECT 'jbr_held_identity'::regtype::oid`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			q := `SELECT * FROM jsonb_populate_record(NULL::jbr_held_identity,'{"a":2,"b":"retained"}'::jsonb)`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			check := fixture.Select(q, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "retained"}})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
			jsonbRecordSetup(t, p, b, c, `ALTER TABLE jbr_held_identity RENAME TO jbr_held_identity_renamed`)
			var after int64
			if err := c.QueryRowContext(p.Ctx, `SELECT 'jbr_held_identity_renamed'::regtype::oid`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("rowtype identity changed across rename: %d -> %d", before, after)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
			jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_record(NULL::jbr_held_identity_renamed,'{"a":2,"b":"retained"}'::jsonb)`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "retained"}}))
		})
		q2 := `SELECT * FROM jsonb_populate_record(NULL::jbr_held,'{"a":2,"b":"retained"}'::jsonb)`
		fixed, err := c.PrepareContext(p.Ctx, q2)
		if err != nil {
			t.Fatal(err)
		}
		defer fixed.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: fixed, SQL: q2}, b.Recorder, fixture.Select(q2, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "retained"}}))
		jsonbRecordSetup(t, p, b, c, `ALTER TYPE jbr_held ADD ATTRIBUTE c int`)
		t.Run("held changed result contract", func(t *testing.T) {
			// The executed held plan succeeded on PostgreSQL18.0. Its analyzed
			// SELECT * target list precedes the added member; fresh analysis below
			// expands all three. Assert both exact public shapes, not a guessed error.
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: fixed, SQL: q2}, b.Recorder, fixture.Select(q2, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "retained"}}))
		})
		jsonEachBoth(t, p, b, c, fixture.Select(q2, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(2), "retained", nil}}))
	})
}
