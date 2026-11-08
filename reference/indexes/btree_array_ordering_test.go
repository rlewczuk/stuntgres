package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceBtreeArrayOrdering pins the C-locale PostgreSQL 18.6 array
// ordering rows. Every witness runs through direct and prepared routes plus
// one held statement. The row sort orders through the same element comparator
// that the btree array_ops kernel uses; it is not an index-scan proof.
func TestReferenceBtreeArrayOrdering(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)

		run(fixture.Exec("CREATE TABLE grin_arr_text(id integer, v text[])", 0))
		run(fixture.Exec("CREATE INDEX grin_arr_text_idx ON grin_arr_text(v)", 0))
		run(fixture.Exec(`INSERT INTO grin_arr_text VALUES
			(1,'{}'),
			(2,'{A}'),
			(3,'{a}'),
			(4,'{a,b}'),
			(5,'{a,c}'),
			(6,'{a,NULL}'),
			(7,'{b}'),
			(8,'{NULL}'),
			(9,NULL)`, 9))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_text ORDER BY v", []string{"id"}, []string{"INT4"}, [][]any{
			{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}, {int64(8)}, {int64(9)},
		}))
		const textDesc = "SELECT id FROM grin_arr_text ORDER BY v DESC"
		desc := fixture.Select(textDesc, []string{"id"}, []string{"INT4"}, [][]any{
			{int64(9)}, {int64(8)}, {int64(7)}, {int64(6)}, {int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)},
		})
		classRoutes(t, p, b, conn, desc)
		heldText := classHeld(t, p, conn, textDesc)
		fixture.RunStep(t, p.Ctx, heldText, b.Recorder, desc)

		run(fixture.Exec("CREATE TABLE grin_arr_int(id integer, v integer[])", 0))
		run(fixture.Exec("CREATE INDEX grin_arr_int_idx ON grin_arr_int(v)", 0))
		run(fixture.Exec(`INSERT INTO grin_arr_int VALUES
			(1,'{}'),
			(2,'{-1}'),
			(3,'{1}'),
			(4,'{1,2}'),
			(5,'{2}'),
			(6,'{10}'),
			(7,'{NULL}')`, 7))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_int ORDER BY v", []string{"id"}, []string{"INT4"}, [][]any{
			{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_int WHERE v > ARRAY[1] ORDER BY v", []string{"id"}, []string{"INT4"}, [][]any{
			{int64(4)}, {int64(5)}, {int64(6)}, {int64(7)},
		}))

		run(fixture.Exec("CREATE TYPE grin_mood AS ENUM('sad','ok','happy')", 0))
		run(fixture.Exec("CREATE TABLE grin_arr_enum(id integer, v grin_mood[])", 0))
		run(fixture.Exec("CREATE INDEX grin_arr_enum_idx ON grin_arr_enum(v)", 0))
		run(fixture.Exec(`INSERT INTO grin_arr_enum VALUES
			(1,'{}'),
			(2,'{sad}'),
			(3,'{sad,happy}'),
			(4,'{ok}'),
			(5,'{happy}')`, 5))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_enum ORDER BY v", []string{"id"}, []string{"INT4"}, [][]any{
			{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_enum ORDER BY v DESC", []string{"id"}, []string{"INT4"}, [][]any{
			{int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)},
		}))
	})
}