package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowValueMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, empty := range []bool{false, true} {
		q := "SELECT first_value(id) OVER(ORDER BY id),last_value(id) OVER(ORDER BY id),nth_value(id,100) OVER(ORDER BY id) FROM wv"
		var expected [][]any
		if empty {
			q += " WHERE FALSE"
		} else {
			q += " ORDER BY id"
			for i := 1; i <= 7; i++ {
				expected = append(expected, []any{int64(1), int64(i), nil})
			}
		}
		s := fixture.Select(q, []string{"first_value", "last_value", "nth_value"}, []string{"INT4", "INT4", "INT4"}, expected)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 3 {
				t.Fatal(columns, err)
			}
			for i, column := range columns {
				wantScan := reflect.TypeFor[int64]()
				if b.PG {
					wantScan = reflect.TypeFor[int32]()
				}
				if column.Name() != s.Columns[i] || column.DatabaseTypeName() != "INT4" || column.ScanType() != wantScan {
					t.Fatal("value metadata", i, column)
				}
				nullable, known := column.Nullable()
				if nullable != !b.PG || known != !b.PG {
					t.Fatal("computed nullable", i, nullable, known)
				}
				if n, ok := column.Length(); n != 0 || ok {
					t.Fatal("length", i, n, ok)
				}
				if precision, scale, ok := column.DecimalSize(); precision != 0 || scale != 0 || ok {
					t.Fatal("decimal", i, precision, scale, ok)
				}
			}
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
			var f, l, n sql.NullInt64
			if err := rows.Scan(&f, &l, &n); err != nil || !f.Valid || f.Int64 != 1 || !l.Valid || l.Int64 != int64(index+1) || n.Valid {
				t.Fatal(f, l, n, err)
			}
		}
		queryWindowRoutes(t, p, b, s)
	}
}

func TestReferenceQueryWindowValueMetadata(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowValueSeed(t, p, b)
		t.Run("int4", func(t *testing.T) { queryWindowValueMetadata(t, p, b) })
		for _, q := range []string{
			"CREATE TYPE wv_mood AS ENUM('z','a')",
			"CREATE DOMAIN wv_domain AS wv_mood",
			"CREATE TABLE wv_types(id INT,t TEXT COLLATE \"C\",b BYTEA,num NUMERIC,m wv_mood,d wv_domain)",
		} {
			queryWindowRun(t, p, b, fixture.Exec(q, 0))
		}
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wv_types VALUES(1,'z','\\x00ff',1.25,'z','z'),(2,NULL,NULL,NULL,NULL,NULL)", 2))
		for _, tc := range []struct {
			expr, typ string
			value     any
		}{
			{"t COLLATE \"C\"", "TEXT", "z"}, {"b", "BYTEA", string([]byte{0, 255})}, {"num", "NUMERIC", "1.25"},
			{"m", "", "z"}, {"d", "", "z"}, {"ARRAY[id,NULL::int]", "_INT4", "{1,NULL}"}, {"'{\"x\":1}'::json", "JSON", `{"x":1}`},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, suffix := range []string{"", " WHERE FALSE"} {
					var expected [][]any
					if suffix == "" {
						last := any(nil)
						if tc.typ == "_INT4" {
							last = "{2,NULL}"
						}
						if tc.typ == "JSON" {
							last = tc.value
						}
						expected = [][]any{{tc.value, last, nil}, {tc.value, last, nil}}
					}
					q := "SELECT first_value(" + tc.expr + ") OVER w AS f,last_value(" + tc.expr + ") OVER w AS l,nth_value(" + tc.expr + ",100) OVER w AS n FROM wv_types" + suffix + " WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY id"
					s := fixture.Select(q, []string{"f", "l", "n"}, []string{tc.typ, tc.typ, tc.typ}, expected)
					if tc.typ == "BYTEA" {
						s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
							if value == nil {
								return false
							}
							bytes, ok := value.([]byte)
							if !ok || len(bytes) != 2 || bytes[0] != 0 || bytes[1] != 255 {
								t.Fatalf("BYTEA raw %T %v", value, value)
							}
							return true
						}
					}
					for _, prepared := range []bool{false, true} {
						s.Prepared = prepared
						if tc.typ == "" {
							fixture.EnumReferenceRunStep(t, p.Ctx, b.DB, b.Recorder, s)
						} else {
							queryWindowRun(t, p, b, s)
						}
					}
				}
			})
		}
		t.Run("identities", func(t *testing.T) {
			expected := [][]any{{int64(3112), int64(3113), int64(3114)}}
			if b.PG {
				expected = [][]any{{"3112", "3113", "3114"}}
			}
			s := fixture.Select("SELECT 'pg_catalog.first_value(anyelement)'::regprocedure::oid AS f,'pg_catalog.last_value(anyelement)'::regprocedure::oid AS l,'pg_catalog.nth_value(anyelement,integer)'::regprocedure::oid AS n", []string{"f", "l", "n"}, []string{"OID", "OID", "OID"}, expected)
			s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
				if b.PG {
					bytes, ok := value.([]byte)
					if !ok || string(bytes) != []string{"3112", "3113", "3114"}[column] {
						t.Fatalf("PostgreSQL OID raw %T %v", value, value)
					}
				} else if number, ok := value.(int64); !ok || number != int64(3112+column) {
					t.Fatalf("native OID raw %T %v", value, value)
				}
				return true
			}
			queryWindowRoutes(t, p, b, s)
		})
	})
}
