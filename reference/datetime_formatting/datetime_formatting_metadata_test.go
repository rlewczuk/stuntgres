package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeFormattingMetadata(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		date := any("2000-12-05")
		if b.PG {
			date = time.Date(2000, 12, 5, 0, 0, 0, 0, time.UTC)
		}
		instant := time.Date(2006, 10, 19, 15, 12, 2, 21230000, time.UTC)
		expressions := []string{
			"to_char(TIMESTAMP '2006-10-19','YYYY')",
			"to_char(TIMESTAMPTZ '2006-10-19+00','YYYY')",
			"to_char(INTERVAL '36 hours','HH24')",
			"to_date('05 Dec 2000','DD Mon YYYY')",
			"to_timestamp('2006-10-19 15:12:02.021230','YYYY-MM-DD HH24:MI:SS.FF6')",
		}
		types := []string{"TEXT", "TEXT", "TEXT", "DATE", "TIMESTAMPTZ"}
		wants := []any{"2006", "2006", "36", date, instant}
		for i, expression := range expressions {
			for _, shape := range []string{"value", "null", "empty"} {
				for _, prepared := range []bool{false, true} {
					q := "SELECT " + expression + " AS result"
					rows := [][]any{{wants[i]}}
					if shape == "null" {
						q = "SELECT CASE WHEN false THEN " + expression + " ELSE NULL END AS result"
						rows = [][]any{{nil}}
					}
					if shape == "empty" {
						q += " WHERE false"
						rows = nil
					}
					s := fixture.Select(q, []string{"result"}, []string{types[i]}, rows)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if len(columns) != 1 {
							t.Fatal(columns)
						}
						c := columns[0]
						wantScan := reflect.TypeFor[string]()
						if types[i] == "TIMESTAMPTZ" || (types[i] == "DATE" && b.PG) {
							wantScan = reflect.TypeFor[time.Time]()
						}
						if c.Name() != "result" || c.DatabaseTypeName() != types[i] || c.ScanType() != wantScan {
							t.Errorf("descriptor name=%s type=%s scan=%v want=%v", c.Name(), c.DatabaseTypeName(), c.ScanType(), wantScan)
						}
						// PostgreSQL 18.0 exposes no computed nullability or
						// decimal typmod, including empty/all-NULL and FF rows.
						n, nok := c.Nullable()
						l, lok := c.Length()
						prec, scale, dok := c.DecimalSize()
						wantLength := int64(0)
						wantLengthKnown := types[i] == "TEXT"
						if wantLengthKnown {
							wantLength = 9223372036854775807
						}
						if n || nok || l != wantLength || lok != wantLengthKnown || prec != 0 || scale != 0 || dok {
							t.Errorf("descriptor query=%q nullable=%t/%t length=%d/%t decimal=%d,%d/%t", q, n, nok, l, lok, prec, scale, dok)
						}
					}
					s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
						var raw any
						if err := rows.Scan(&raw); err != nil {
							t.Fatal(err)
						}
						fixture.AssertRawValue(t, b.PG, types[i], raw)
					}
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
			}
		}
	})
}
