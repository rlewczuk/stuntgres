package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func porterOutputDescriptors(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		porterSetup(t, p, b, c, "porter_dict", "porter_config", true)
		for _, populated := range []bool{true, false} {
			debug := porterDebugStep()
			vector := fixture.Select("SELECT to_tsvector('porter_config','The skies dying') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dy':3 'ski':2"}})
			nullVector := fixture.Select("SELECT to_tsvector('porter_config',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}})
			nullDebug := fixture.Select("SELECT * FROM ts_debug('porter_config',NULL::text)", debug.Columns, debug.Types, nil)
			for _, s := range []fixture.Step{debug, vector, nullVector, nullDebug} {
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
						if value == nil && len(s.Rows) == 1 && s.Rows[0][i] == nil {
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
				porterSmokeBoth(t, p, b, c, s)
			}
		}
	})
}
