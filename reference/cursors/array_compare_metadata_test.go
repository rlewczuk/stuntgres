package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayCompareMetadata(t *testing.T) {
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ac_meta(x INT,a INT[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ac_meta_empty(x INT,a INT[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ac_meta VALUES(1,'{1,NULL}'),(NULL,NULL)", 2))
		for _, tc := range []struct {
			name, source string
			want         [][]any
		}{
			{"populated", "ac_meta", [][]any{{true, false, true, nil}, {nil, nil, nil, nil}}},
			{"empty", "ac_meta_empty", nil},
			{"rejected", "ac_meta WHERE FALSE", nil},
			{"unknown_filter", "ac_meta WHERE x NOT IN(1,NULL)", nil},
			{"all_null", "ac_meta WHERE x IS NULL", [][]any{{nil, nil, nil, nil}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT x IN(1,2),x NOT IN(1,2) AS outside,x=ANY(a) AS member,x=ALL(a) FROM " + tc.source
				names := []string{"?column?", "outside", "member", "?column?"}
				types := []string{"BOOL", "BOOL", "BOOL", "BOOL"}
				check := func(metadata []*sql.ColumnType) {
					t.Helper()
					fixture.ScalarAssertDescription(t, metadata, names, types)
					fixture.ScalarAssertMetadata(t, b.PG, metadata, 0, 1, 2, 3)
					for _, column := range metadata {
						if column.ScanType() != reflect.TypeFor[bool]() {
							t.Fatalf("BOOL ScanType=%v", column.ScanType())
						}
						if length, known := column.Length(); length != 0 || known {
							t.Fatalf("BOOL Length=%d/%t", length, known)
						}
						if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
							t.Fatalf("BOOL DecimalSize=%d/%d/%t", precision, scale, known)
						}
					}
				}
				for _, prepared := range []bool{false, true} {
					var retained []*sql.ColumnType
					s := fixture.Select(q, names, types, tc.want)
					s.Bag, s.Prepared = true, prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						var err error
						retained, err = rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						check(retained)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					check(retained)
				}
			})
		}
	})
}
