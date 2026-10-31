package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func nepaliOutputDescriptors(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, populated := range []bool{true, false} {
			debug := nepaliDebugStep()
			vector := fixture.Select("SELECT to_tsvector('nepali','RATS र घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':1 'घर':3"}})
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
				s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
					values := make([]any, len(s.Types))
					dest := make([]any, len(values))
					for i := range dest {
						dest[i] = &values[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, value := range values {
						if s.Rows[row][i] == nil {
							if value != nil {
								t.Fatalf("raw NULL %s = %T", s.Columns[i], value)
							}
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
				nepaliSmokeBoth(t, p, b, c, s)
			}
		}
	})
}
