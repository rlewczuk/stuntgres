package reference_test

import "testing"

// These exact contextual-SLICE lookaheads exercise the two counted-motion
// reductions added to the grammar's conflict inventory. Expected cursor positions
// follow ordered 10/20/30 input, independently of the native implementation.
func TestReferencePLpgSQLForeachCursorKeyword(t *testing.T) {
	const declarations = "slice integer:=1; c SCROLL CURSOR FOR SELECT i FROM pc_rows ORDER BY i; x integer;"
	plCursorCases(t, []plCursorCase{
		{"forward_slice_count", declarations, "OPEN c; MOVE FORWARD slice FROM c; FETCH c INTO x; CLOSE c;", "x", "integer", int64(20)},
		{"backward_slice_count", declarations, "OPEN c; FETCH LAST FROM c INTO x; MOVE BACKWARD slice FROM c; FETCH RELATIVE 0 FROM c INTO x; CLOSE c;", "x", "integer", int64(20)},
	})
}
