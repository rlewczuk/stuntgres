package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFinnishSnowballMetadata(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT ts_lexize($1,$2) AS lexemes", "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"finnish_stem", "talossa"}, "{talo}"}, {[]any{[]byte("finnish_stem"), []byte("ONLY")}, "{only}"}, {[]any{"finnish_stem", ""}, "{}"}, {[]any{"finnish_stem", []byte{}}, "{}"}, {[]any{"finnish_stem", nil}, nil}, {[]any{nil, "talossa"}, nil},
				{[]any{"simple", "TALOSSA"}, "{talossa}"},
				{[]any{"english_stem", "RATS"}, "{rat}"},
				{[]any{"finnish_stem", "ON"}, "{}"},
				{[]any{"finnish_stem", "A B"}, `{"a b"}`},
				{[]any{"finnish_stem", `A,B"C\D{ E}!`}, `{"a,b\"c\\d{ e}!"}`},
			} {
				dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
				dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.args...)
			}
			var want any = "{}"
			if b.PG {
				want = nil
			}
			dictionaryResult(t, p, b, c, q, want, "finnish_stem", []byte(nil))
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want, "finnish_stem", []byte(nil))
			regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
			stmt.Close()
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('finnish_stem','talossa') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 {
					t.Fatal(types, err)
				}
				ct := types[0]
				scan := reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
				length, lk := ct.Length()
				precision, scale, dk := ct.DecimalSize()
				nullable, nk := ct.Nullable()
				if ct.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
					t.Fatal("empty lexize descriptors", ct)
				}
			}
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
