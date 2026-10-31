package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapNulls(t *testing.T) {
	heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		heapHeld(t, p, b, c, heapSource, []fixture.Step{heapStep(heapSource, nil), heapStep(heapSource, heapImage(), heapRow(b.PG)), heapError(heapSource, "22023", "input page too small (0 bytes)", []byte{}), heapStep(heapSource, nil)})
		var source, scalar []fixture.Step
		for _, args := range [][]any{{nil, int64(0)}, {int64(0), nil}, {nil, nil}} {
			source = append(source, heapFlagsStep(heapFlagsSource, args[0], args[1], nil, nil))
			scalar = append(scalar, fixture.Select(heapFlagsScalar, []string{"heap_tuple_infomask_flags"}, []string{"RECORD"}, [][]any{{nil}}, args...))
		}
		source = append(source, heapFlagsStep(heapFlagsSource, int64(0), int64(0), "{}", "{}"))
		heapHeld(t, p, b, c, heapFlagsSource, source)
		heapHeld(t, p, b, c, heapFlagsScalar, scalar)
		for _, q := range []string{
			"SELECT * FROM heap_part.heap_tuple_infomask_flags($1,10/$2::integer)",
			"SELECT * FROM heap_part.heap_tuple_infomask_flags(10/$2::integer,$1)",
			"SELECT * FROM heap_part.heap_tuple_infomask_flags(t_infomask2=>10/$2::integer,t_infomask=>$1)",
			"SELECT * FROM heap_part.heap_tuple_infomask_flags(t_infomask2=>$1,t_infomask=>10/$2::integer)",
		} {
			heapHeld(t, p, b, c, q, []fixture.Step{heapError(q, "22012", "division by zero", nil, int64(0)), heapFlagsStep(q, nil, int64(1), nil, nil)})
		}
		for _, q := range []string{
			"SELECT * FROM heap_part.heap_tuple_infomask_flags($1,$2::text::integer)",
			"SELECT * FROM heap_part.heap_tuple_infomask_flags($2::text::integer,$1)",
			"SELECT * FROM heap_part.heap_tuple_infomask_flags(t_infomask2=>$2::text::integer,t_infomask=>$1)",
		} {
			heapHeld(t, p, b, c, q, []fixture.Step{heapError(q, "22P02", "invalid input syntax for type integer: \"bad\"", nil, "bad"), heapFlagsStep(q, nil, "0", nil, nil)})
		}
	})
}
