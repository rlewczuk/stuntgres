package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func iscnMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 1 {
		t.Fatalf("columns=%d want 1", len(columns))
	}
	c := columns[0]
	length, lengthOK := c.Length()
	precision, scale, decimalOK := c.DecimalSize()
	nullable, nullableOK := c.Nullable()
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if c.Name() != "catalog_name" || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
		t.Fatalf("name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
	}
}

func TestReferenceInfoInformationSchemaCatalogNameDriverMetadata(t *testing.T) {
	iscnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
		for _, projection := range []string{"catalog_name", "*"} {
			for _, x := range []struct {
				predicate string
				args      []any
				present   bool
			}{{"", nil, true}, {"FALSE", nil, false}, {"catalog_name=$1", []any{nil}, false}} {
				q := iscnStep(projection, x.predicate, x.present).Query
				held := iscnHold(t, p, c, q)
				for _, route := range []string{"direct", "fresh", "held"} {
					var rows *sql.Rows
					var err error
					switch route {
					case "direct":
						rows, err = c.QueryContext(p.Ctx, q, x.args...)
					case "fresh":
						stmt, prepareErr := c.PrepareContext(p.Ctx, q)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx, x.args...)
					case "held":
						rows, err = held.Statement.QueryContext(p.Ctx, x.args...)
					}
					if err != nil {
						t.Fatal(route, q, err)
					}
					func() {
						defer rows.Close()
						columns, err := rows.Columns()
						if err != nil || !reflect.DeepEqual(columns, iscnColumns) {
							t.Fatal("column names", columns, err)
						}
						saved, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						iscnMetadata(t, saved, b.PG)
						count := 0
						for rows.Next() {
							var raw any
							if err := rows.Scan(&raw); err != nil {
								t.Fatal(err)
							}
							text, ok := raw.(string)
							if b.PG {
								bytes, bytesOK := raw.([]byte)
								text, ok = string(bytes), bytesOK
							}
							if !ok || text != iscnDatabase {
								t.Fatalf("raw=%T(%v)", raw, raw)
							}
							var scanned string
							if err := rows.Scan(&scanned); err != nil || scanned != iscnDatabase {
								t.Fatal("string scan", scanned, err)
							}
							current, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							iscnMetadata(t, current, b.PG)
							count++
						}
						want := 0
						if x.present {
							want = 1
						}
						if rows.Err() != nil || count != want {
							t.Fatalf("route=%s count=%d want=%d error=%v", route, count, want, rows.Err())
						}
						iscnMetadata(t, saved, b.PG)
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						iscnMetadata(t, saved, b.PG)
					}()
				}
			}
		}
	})
}
