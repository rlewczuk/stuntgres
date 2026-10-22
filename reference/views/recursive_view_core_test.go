package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewCore(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, s := range []fixture.Step{
					fixture.Exec("CREATE RECURSIVE VIEW rv_core(n) AS SELECT 1 UNION ALL SELECT n+1 FROM rv_core WHERE n<3", 0),
					fixture.Exec("CREATE RECURSIVE VIEW rv_big(n) AS SELECT 1::bigint UNION ALL SELECT n+1 FROM rv_big WHERE n<3", 0),
					fixture.Exec("CREATE RECURSIVE VIEW rv_empty(n) AS SELECT 1 WHERE FALSE UNION ALL SELECT n+1 FROM rv_empty WHERE n<3", 0),
					fixture.Exec("CREATE RECURSIVE VIEW rv_typmod(label,amount) AS SELECT 'abc'::varchar(3),1.20::numeric(5,2) WHERE FALSE", 0),
					fixture.Exec("CREATE RECURSIVE VIEW rv_union(n) AS SELECT 1 UNION SELECT 10-n FROM rv_union", 0),
					fixture.Exec("CREATE RECURSIVE VIEW rv_nonself(n) AS SELECT 7", 0),
					fixture.Exec("CREATE RECURSIVE VIEW public.\"RvQuoted\"(n) AS SELECT 1 UNION ALL SELECT n+1 FROM \"RvQuoted\" WHERE n<3", 0),
					recursiveViewRows("SELECT n FROM rv_core", "INT4", 1, 2, 3),
					recursiveViewRows("SELECT n FROM rv_big", "INT8", 1, 2, 3),
					recursiveViewRows("SELECT n FROM rv_union", "INT4", 1, 9),
					recursiveViewRows("SELECT n FROM rv_nonself", "INT4", 7),
					recursiveViewRows("SELECT n FROM \"RvQuoted\"", "INT4", 1, 2, 3),
				} {
					run(s)
				}
				empty := recursiveViewRows("SELECT n FROM rv_empty", "INT4")
				empty.CheckMetadata = recursiveViewMetadata(b, empty.Columns, empty.Types)
				run(empty)
				typed := fixture.Select("SELECT label,amount FROM rv_typmod", []string{"label", "amount"}, []string{"VARCHAR", "NUMERIC"}, nil)
				typed.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					recursiveViewMetadata(b, typed.Columns, typed.Types)(t, rows)
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if length, ok := columns[0].Length(); !ok || length != 3 {
						t.Fatal("varchar length", length, ok)
					}
					if precision, scale, ok := columns[1].DecimalSize(); !ok || precision != 5 || scale != 2 {
						t.Fatal("numeric typmod", precision, scale, ok)
					}
				}
				run(typed)
				held := fixture.ViewHold(t, p, b, "SELECT n FROM rv_core WHERE n>=$1 ORDER BY n")
				for _, tc := range []struct {
					arg  int64
					rows [][]any
				}{{2, recursiveViewInts(2, 3)}, {3, recursiveViewInts(3)}, {1, recursiveViewInts(1, 2, 3)}} {
					s := fixture.Select(held.SQL, []string{"n"}, []string{"INT4"}, tc.rows, tc.arg)
					s.CheckMetadata = recursiveViewMetadata(b, s.Columns, s.Types)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				run(fixture.Exec("CREATE TABLE rv_core_sink(n INT)", 0))
				run(fixture.Exec("INSERT INTO rv_core_sink SELECT n FROM rv_core", 3))
				run(recursiveViewRows("SELECT n FROM rv_core_sink", "INT4", 1, 2, 3))
			})
		})
	}
}
