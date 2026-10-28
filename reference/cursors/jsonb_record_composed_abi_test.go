package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordComposedABI(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE DOMAIN jbr_abi_positive AS int CONSTRAINT jbr_abi_check CHECK(VALUE>0)`)
		for _, q := range []string{
			`SELECT a FROM jsonb_populate_record(ROW('abc'::text),'{}'::jsonb) AS r(a varchar(1))`,
			`SELECT a FROM jsonb_populate_record(ROW('abc'::varchar(3)),'{}'::jsonb) AS r(a varchar(1))`,
		} {
			s := fixture.Select(q, []string{"a"}, []string{"VARCHAR"}, [][]any{{"abc"}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 1 {
					t.Fatal(columns, err)
				}
				if n, known := columns[0].Length(); n != 1 || !known {
					t.Fatal("caller typmod metadata", n, known)
				}
			}
			fixture.JSONEachBoth(t, p, b, c, s)
		}
		for _, tc := range []struct{ q, want string }{
			{`SELECT a FROM jsonb_populate_record(ROW('abc'::varchar(3)),'{}'::jsonb) AS r(a text)`, "abc"},
			{`SELECT a::text AS a FROM jsonb_populate_record(ROW(-1::int),'{}'::jsonb) AS r(a oid)`, "4294967295"},
			{`SELECT a::text AS a FROM jsonb_populate_record(ROW(1::jbr_abi_positive),'{}'::jsonb) AS r(a int)`, "1"},
		} {
			fixture.JSONEachBoth(t, p, b, c, fixture.Select(tc.q, []string{"a"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		for _, tc := range []struct{ q, detail string }{
			{`SELECT * FROM jsonb_populate_record(ROW(1::int),'{}'::jsonb) AS r(a bigint)`, "Returned type integer at ordinal position 1, but query expects bigint."},
			{`SELECT * FROM jsonb_populate_record(ROW('abc'::char(3)),'{}'::jsonb) AS r(a text)`, "Returned type character at ordinal position 1, but query expects text."},
			{`SELECT * FROM jsonb_populate_record(ROW(1::int),'{}'::jsonb) AS r(a jbr_abi_positive)`, "Returned type integer at ordinal position 1, but query expects jbr_abi_positive."},
			{`SELECT * FROM jsonb_populate_record(ROW(ARRAY['x'::text]),'{}'::jsonb) AS r(a varchar[])`, "Returned type text[] at ordinal position 1, but query expects character varying[]."},
			{`SELECT * FROM jsonb_populate_recordset(ROW(1::int),'[]'::jsonb) AS r(a bigint)`, "Returned type integer at ordinal position 1, but query expects bigint."},
		} {
			d := fixture.ControlsReferenceDiagnostic("42804", "function return row and query-specified return row do not match")
			d.Detail = tc.detail
			t.Run(tc.q, func(t *testing.T) { fixture.CompositeReferenceError(t, p, c, tc.q, "execute", d) })
		}
		fixture.CompositeReferenceError(t, p, c, `SELECT * FROM jsonb_populate_record(ROW(1::int),'{"f1":"bad"}'::jsonb) AS r(a bigint)`, "execute", fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`))
		d := fixture.ControlsReferenceDiagnostic("23514", `value for domain jbr_abi_positive violates check constraint "jbr_abi_check"`)
		d.Schema, d.DataType, d.Constraint = "public", "jbr_abi_positive", "jbr_abi_check"
		fixture.CompositeReferenceError(t, p, c, `SELECT * FROM jsonb_populate_record(ROW(1::jbr_abi_positive),'{"f1":0}'::jsonb) AS r(a int)`, "execute", d)
		fixture.JSONEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_recordset(ROW(1::int),NULL::jsonb) AS r(a bigint)`, []string{"a"}, []string{"INT8"}, nil))
	})
}
