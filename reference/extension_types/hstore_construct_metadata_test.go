package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreConstructMetadata(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, tc := range []struct {
			expression string
			value      any
		}{
			{"hstore('a','1')", `"a"=>"1"`}, {"hstore(NULL::text,'1')", nil},
			{"hstore(ARRAY[]::text[])", ""}, {"hstore(ARRAY['a'],NULL::text[])", `"a"=>NULL`},
		} {
			for _, suffix := range []string{"", " WHERE false"} {
				want := [][]any{{tc.value}}
				if suffix != "" {
					want = nil
				}
				s := hstoreSelect(b, "SELECT "+tc.expression+" AS constructed"+suffix, []string{"constructed"}, want, nil)
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
			}
		}
		for _, input := range []string{"'a=>1'::hstore", "NULL::hstore"} {
			for _, suffix := range []string{"", " WHERE false"} {
				want := [][]any{{"1", true, true}}
				if input == "NULL::hstore" {
					want = [][]any{{nil, nil, nil}}
				}
				if suffix != "" {
					want = nil
				}
				s := fixture.Select("SELECT "+input+"->'a' AS fetched,exist("+input+",'a') AS present,defined("+input+",'a') AS valued"+suffix, []string{"fetched", "present", "valued"}, []string{"TEXT", "BOOL", "BOOL"}, want)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for i, column := range columns {
						scan := reflect.TypeFor[bool]()
						if i == 0 {
							scan = reflect.TypeFor[string]()
						}
						if column.ScanType() != scan {
							t.Fatalf("%s ScanType=%v want %v", column.Name(), column.ScanType(), scan)
						}
						if nullable, known := column.Nullable(); nullable || known {
							t.Fatalf("computed nullable=%v/%v", nullable, known)
						}
						n, ok := column.Length()
						if i == 0 {
							if n != 1<<63-1 || !ok {
								t.Fatalf("text length=%d/%v", n, ok)
							}
						} else if n != 0 || ok {
							t.Fatalf("length=%d/%v", n, ok)
						}
						if p, s, ok := column.DecimalSize(); p != 0 || s != 0 || ok {
							t.Fatalf("decimal=%d/%d/%v", p, s, ok)
						}
					}
				}
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
			}
		}
		for _, tc := range []struct {
			expression string
			value      any
		}{
			{"'a=>1'::hstore->ARRAY['a']", "{1}"}, {"'a=>1'::hstore->ARRAY[NULL::text]", "{NULL}"},
			{"'a=>1'::hstore->ARRAY[]::text[]", "{}"}, {"'a=>1'::hstore->NULL::text[]", nil},
		} {
			for _, suffix := range []string{"", " WHERE false"} {
				want := [][]any{{tc.value}}
				if suffix != "" {
					want = nil
				}
				s := fixture.ArrayReferenceSelect("SELECT "+tc.expression+" AS requested"+suffix, []string{"requested"}, []string{"_TEXT"}, []string{"_TEXT"}, want, b.PG)
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
			}
		}
	})
}
