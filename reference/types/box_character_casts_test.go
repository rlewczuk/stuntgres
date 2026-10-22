package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Match the shared fixture's character metadata conventions on the actual
// direct/prepared/held cursor, including executions whose only cell is NULL.
func boxCharacterSelect(b fixture.Backend, query, typ string, want any, length int64, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		column := columns[0]
		scan := reflect.TypeFor[string]()
		if b.PG && typ == "BPCHAR" {
			scan = reflect.TypeFor[any]()
		}
		gotLength, known := column.Length()
		precision, scale, decimal := column.DecimalSize()
		nullable, nullKnown := column.Nullable()
		if column.Name() != "v" || column.DatabaseTypeName() != typ || gotLength != length || !known ||
			column.ScanType() != scan || precision != 0 || scale != 0 || decimal || nullable || nullKnown {
			t.Fatalf("character metadata name=%s type=%s length=%d/%t scan=%v decimal=%d,%d/%t nullable=%t/%t; want type=%s length=%d scan=%v",
				column.Name(), column.DatabaseTypeName(), gotLength, known, column.ScanType(), precision, scale, decimal, nullable, nullKnown, typ, length, scan)
		}
	}
	return s
}

func TestReferenceBoxCharacterCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, geometry := range []struct {
				typ, literal, input, text string
				padded                    string
				width                     string
			}{
				{"box", "box '1,1,0,0'", "1,1,0,0", "(1,1),(0,0)", "(1,1),(0,0)   ", "14"},
				{"point", "point '(1,2)'", "(1,2)", "(1,2)", "(1,2)   ", "8"},
			} {
				t.Run(geometry.typ, func(t *testing.T) {
					for _, target := range []struct {
						sql, typ, want string
						length         int64
					}{
						{"varchar(3)", "VARCHAR", "(1,", 3},
						{"char(3)", "BPCHAR", "(1,", 3},
						{"varchar(32)", "VARCHAR", geometry.text, 32},
						{"char(" + geometry.width + ")", "BPCHAR", geometry.padded, int64(len(geometry.padded))},
						{"text", "TEXT", geometry.text, math.MaxInt64},
					} {
						for _, query := range []string{
							"SELECT (" + geometry.literal + ")::" + target.sql + " AS v",
							"SELECT CAST(" + geometry.literal + " AS " + target.sql + ") AS v",
							"SELECT ($1::" + geometry.typ + ")::" + target.sql + " AS v",
						} {
							var args []any
							if strings.Contains(query, "$1") {
								args = []any{geometry.input}
							}
							for _, prepared := range []bool{false, true} {
								s := boxCharacterSelect(b, query, target.typ, target.want, target.length, args...)
								s.Prepared = prepared
								fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							}
						}
						q := "SELECT ($1::" + geometry.typ + ")::" + target.sql + " AS v"
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						for _, arg := range []any{geometry.input, []byte(geometry.input), nil, geometry.input} {
							var want any = target.want
							if arg == nil {
								want = nil
							}
							fixture.RunStep(t, p.Ctx, held, b.Recorder, boxCharacterSelect(b, q, target.typ, want, target.length, arg))
						}
						stmt.Close()
					}
				})
			}
			// A fetched BOX component is also a real POINT conversion source.
			for _, prepared := range []bool{false, true} {
				s := boxCharacterSelect(b, "SELECT ((box '1,1,0,0')[0])::varchar(3) AS v", "VARCHAR", "(1,", 3)
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Exec("CREATE TABLE box_character_assign(id integer,v varchar(3),c char(3))", 0))
			run(fixture.Exec("INSERT INTO box_character_assign VALUES(1,'old','old')", 1))
			unchanged := fixture.Select("SELECT id,v,c FROM box_character_assign ORDER BY id", []string{"id", "v", "c"}, []string{"INT4", "VARCHAR", "BPCHAR"}, [][]any{{int64(1), "old", "old"}})
			for _, tc := range []struct{ column, typ string }{{"v", "character varying(3)"}, {"c", "character(3)"}} {
				for _, source := range []struct{ literal, typ, input string }{{"box '1,1,0,0'", "box", "1,1,0,0"}, {"point '(1,2)'", "point", "(1,2)"}} {
					for _, prepared := range []bool{false, true} {
						for _, q := range []string{
							"UPDATE box_character_assign SET " + tc.column + "=" + source.literal + " WHERE id=1",
							"INSERT INTO box_character_assign(id," + tc.column + ") VALUES(2,'ok'),(3," + source.literal + ")",
						} {
							s := boxError(q, "22001", "value too long for type "+tc.typ, "", "", prepared, true)
							run(s)
							run(unchanged)
						}
						q := "UPDATE box_character_assign SET " + tc.column + "=$1::" + source.typ + " WHERE id=1"
						s := boxError(q, "22001", "value too long for type "+tc.typ, "", "", prepared, true, source.input)
						run(s)
						run(unchanged)
					}
					for _, insertion := range []bool{true, false} {
						q := "UPDATE box_character_assign SET " + tc.column + "=$1::" + source.typ + " WHERE id=1"
						if insertion {
							q = "INSERT INTO box_character_assign(id," + tc.column + ") VALUES(2,$1::" + source.typ + ")"
						}
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						failure := boxError(q, "22001", "value too long for type "+tc.typ, "", "", false, true, source.input)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
						run(unchanged)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, nil))
						want := [][]any{{int64(1), "old", "old"}}
						if insertion {
							want = append(want, []any{int64(2), nil, nil})
						} else if tc.column == "v" {
							want[0][1] = nil
						} else {
							want[0][2] = nil
						}
						run(fixture.Select(unchanged.Query, unchanged.Columns, unchanged.Types, want))
						stmt.Close()
						if insertion {
							run(fixture.Exec("DELETE FROM box_character_assign WHERE id=2", 1))
						} else {
							run(fixture.Exec("UPDATE box_character_assign SET v='old',c='old' WHERE id=1", 1))
						}
						run(unchanged)
					}
				}
			}
		})
	}
}
