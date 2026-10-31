package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func romanianOutputDescriptors(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, populated := range []bool{true, false} {
			debug := romanianDebugStep()
			vector := fixture.Select("SELECT to_tsvector('romanian','ocolul și abruptă THE') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abrupt':3 'ocol':1 'the':4 'și':2"}})
			for _, s := range []fixture.Step{debug, vector} {
				if !populated {
					s.Query += " WHERE false"
					s.Rows = nil
				}
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != len(s.Types) {
						t.Fatal(columns, err)
					}
					for i, column := range columns {
						scan := reflect.TypeFor[string]()
						if b.PG && s.Types[i] != "TEXT" {
							scan = reflect.TypeFor[any]()
						}
						var length int64
						if s.Types[i] == "TEXT" {
							length = math.MaxInt64
						}
						l, lk := column.Length()
						precision, scale, dk := column.DecimalSize()
						n, nk := column.Nullable()
						if column.Name() != s.Columns[i] || column.DatabaseTypeName() != s.Types[i] || column.ScanType() != scan || l != length || lk != (length != 0) || precision != 0 || scale != 0 || dk || n || nk {
							t.Fatalf("descriptor %s: type=%s scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", column.Name(), column.DatabaseTypeName(), column.ScanType(), l, lk, precision, scale, dk, n, nk)
						}
					}
				}
				s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					values := make([]any, len(s.Types))
					dest := make([]any, len(values))
					for i := range dest {
						dest[i] = &values[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, value := range values {
						if value == nil {
							continue
						}
						want := reflect.TypeFor[string]()
						if b.PG && s.Types[i] != "TEXT" {
							want = reflect.TypeFor[[]byte]()
						}
						if reflect.TypeOf(value) != want {
							t.Fatalf("raw %s = %T, want %v", s.Columns[i], value, want)
						}
					}
				}
				romanianSmokeBoth(t, p, b, c, s)
			}
		}
	})
}
