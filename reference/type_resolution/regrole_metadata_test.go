package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegroleMetadata(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE rr_meta(id int,r regrole,a regrole[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_meta VALUES(1,'rr_role','{rr_role,NULL,0,4294967295}'),(2,NULL,NULL),(3,'0',NULL),(4,'4294967295',NULL)", 4))
		for _, q := range []string{"SELECT r,a FROM rr_meta ORDER BY id", "SELECT r,a FROM rr_meta WHERE id=99", "SELECT NULL::regrole AS r"} {
			for _, prepared := range []bool{false, true} {
				t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					var stmt *sql.Stmt
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = c.QueryContext(p.Ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for i, column := range columns {
						wantName, wantType := "r", "REGROLE"
						if i == 1 {
							wantName, wantType = "a", "_REGROLE"
						}
						if column.Name() != wantName || column.DatabaseTypeName() != wantType {
							t.Errorf("column=%s/%s want %s/%s", column.Name(), column.DatabaseTypeName(), wantName, wantType)
						}
						scan := reflect.TypeFor[string]()
						if b.PG {
							scan = reflect.TypeFor[any]()
						}
						if column.ScanType() != scan {
							t.Errorf("ScanType=%v want %v", column.ScanType(), scan)
						}
						n, nk := column.Nullable()
						l, lk := column.Length()
						precision, scale, dk := column.DecimalSize()
						wantNullable := !b.PG && q != "SELECT NULL::regrole AS r"
						if n != wantNullable || nk != wantNullable || l != 0 || lk || precision != 0 || scale != 0 || dk {
							t.Errorf("flags nullable=%t/%t length=%d/%t decimal=%d,%d/%t want nullable=%t/%t", n, nk, l, lk, precision, scale, dk, wantNullable, wantNullable)
						}
					}
					want := [][]any{{"rr_role", "{rr_role,NULL,-,4294967295}"}, {nil, nil}, {"-", nil}, {"4294967295", nil}}
					if q == "SELECT r,a FROM rr_meta WHERE id=99" {
						want = nil
					}
					if q == "SELECT NULL::regrole AS r" {
						want = [][]any{{nil}}
					}
					count := 0
					for rows.Next() {
						values := make([]any, len(columns))
						pointers := make([]any, len(columns))
						for i := range values {
							pointers[i] = &values[i]
						}
						if err := rows.Scan(pointers...); err != nil {
							t.Fatal(err)
						}
						if count >= len(want) {
							t.Fatal("extra metadata result row")
						}
						for i, value := range values {
							fixture.AssertRawValue(t, b.PG, columns[i].DatabaseTypeName(), value)
							if fixture.Normalize(value) != want[count][i] {
								t.Errorf("row=%d column=%d value=%#v want %#v", count, i, fixture.Normalize(value), want[count][i])
							}
						}
						count++
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if count != len(want) {
						t.Errorf("rows=%d want %d", count, len(want))
					}
				})
			}
		}
	})
}
