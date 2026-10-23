package reference_test

import "testing"

func TestReferencePLpgSQLCursorMotion(t *testing.T) {
	const decl = "c SCROLL CURSOR FOR SELECT i FROM pc_rows ORDER BY i; x integer; f boolean; n integer:=2; "
	const open = "OPEN c; "
	const close = " CLOSE c;"
	plCursorCases(t, []plCursorCase{
		{"initial_found", decl, "", "FOUND", "boolean", false},
		{"next", decl, open + "FETCH NEXT FROM c INTO x; f:=FOUND;" + close, "x=10 AND f AND FOUND", "boolean", true},
		{"move_then_next", decl, open + "FETCH c INTO x; MOVE FORWARD 1 FROM c; f:=FOUND; FETCH c INTO x;" + close, "x=30 AND f AND FOUND", "boolean", true},
		{"exhaustion", decl, open + "MOVE FORWARD ALL FROM c; FETCH c INTO x; f:=FOUND;" + close, "x IS NULL AND NOT f AND NOT FOUND", "boolean", true},
		{"repeated_exhaustion", decl, open + "MOVE ALL FROM c; FETCH c INTO x; FETCH c INTO x;" + close, "x IS NULL AND NOT FOUND", "boolean", true},
		{"prior_after_end", decl, open + "MOVE ALL FROM c; FETCH c INTO x; FETCH PRIOR FROM c INTO x;" + close, "x=30 AND FOUND", "boolean", true},
		{"first", decl, open + "MOVE ALL FROM c; FETCH FIRST FROM c INTO x;" + close, "x", "integer", int64(10)},
		{"last", decl, open + "FETCH LAST FROM c INTO x;" + close, "x", "integer", int64(30)},
		{"absolute_expression", decl, open + "FETCH ABSOLUTE n FROM c INTO x;" + close, "x", "integer", int64(20)},
		{"relative_negative", decl, open + "FETCH ABSOLUTE 2 FROM c INTO x; FETCH RELATIVE -1 FROM c INTO x;" + close, "x", "integer", int64(10)},
		{"absolute_zero", decl, open + "FETCH LAST FROM c INTO x; FETCH ABSOLUTE 0 FROM c INTO x; f:=FOUND;" + close, "x IS NULL AND NOT f", "boolean", true},
		{"next_after_zero", decl, open + "FETCH ABSOLUTE 0 FROM c INTO x; FETCH c INTO x;" + close, "x", "integer", int64(10)},
		{"relative_zero", decl, open + "FETCH c INTO x; FETCH RELATIVE 0 FROM c INTO x;" + close, "x=10 AND FOUND", "boolean", true},
		{"absolute_negative", decl, open + "FETCH ABSOLUTE -1 FROM c INTO x;" + close, "x", "integer", int64(30)},
		{"backward_default", decl, open + "FETCH LAST FROM c INTO x; FETCH BACKWARD FROM c INTO x;" + close, "x", "integer", int64(20)},
		{"forward_default", decl, open + "FETCH FORWARD FROM c INTO x;" + close, "x", "integer", int64(10)},
		{"before_first", decl, open + "FETCH PRIOR FROM c INTO x;" + close, "x IS NULL AND NOT FOUND", "boolean", true},
		{"far_absolute", decl, open + "FETCH ABSOLUTE 99 FROM c INTO x; f:=FOUND; FETCH PRIOR FROM c INTO x;" + close, "NOT f AND x=30 AND FOUND", "boolean", true},
		{"far_negative_absolute", decl, open + "FETCH ABSOLUTE -99 FROM c INTO x; f:=FOUND; FETCH c INTO x;" + close, "NOT f AND x=10", "boolean", true},
		{"move_zero_onrow", decl, open + "FETCH c INTO x; MOVE FORWARD 0 FROM c; f:=FOUND; FETCH c INTO x;" + close, "f AND x=20", "boolean", true},
		{"move_zero_boundary", decl, open + "MOVE FORWARD 0 FROM c; f:=FOUND;" + close, "f", "boolean", false},
		{"move_all_back", decl, open + "MOVE FORWARD ALL FROM c; MOVE BACKWARD ALL FROM c; f:=FOUND; FETCH c INTO x;" + close, "f AND x=10", "boolean", true},
		{"move_many", decl, open + "MOVE FORWARD n FROM c; f:=FOUND; FETCH c INTO x;" + close, "f AND x=30", "boolean", true},
		{"move_negative", decl, open + "FETCH LAST FROM c INTO x; MOVE FORWARD -1 FROM c; FETCH c INTO x;" + close, "x", "integer", int64(30)},
		{"move_backward_negative", decl, open + "MOVE BACKWARD -2 FROM c; FETCH c INTO x;" + close, "x", "integer", int64(30)},
		{"move_empty", "c SCROLL CURSOR FOR SELECT i FROM pc_rows WHERE false;", "OPEN c; MOVE ALL FROM c; CLOSE c;", "FOUND", "boolean", false},
		{"empty_both_directions", "c SCROLL CURSOR FOR SELECT i FROM pc_rows WHERE false; x integer; f boolean;", "OPEN c; FETCH c INTO x; f:=FOUND; FETCH PRIOR FROM c INTO x; CLOSE c;", "x IS NULL AND NOT f AND NOT FOUND", "boolean", true},
		{"minimum_integer_absolute", decl, open + "FETCH ABSOLUTE (-2147483647-1) FROM c INTO x;" + close, "x IS NULL AND NOT FOUND", "boolean", true},
		{"minimum_integer_move", decl, open + "MOVE FORWARD (-2147483647-1) FROM c;" + close, "FOUND", "boolean", false},
		{"numeric_position_conversion", decl, open + "FETCH ABSOLUTE 1.9::numeric FROM c INTO x;" + close, "x", "integer", int64(20)},
		{"move_absolute_zero", decl, open + "MOVE LAST FROM c; MOVE ABSOLUTE 0 FROM c; f:=FOUND; FETCH c INTO x;" + close, "NOT f AND x=10", "boolean", true},
		{"move_relative_zero", decl, open + "FETCH c INTO x; MOVE RELATIVE 0 FROM c; f:=FOUND; FETCH c INTO x;" + close, "f AND x=20", "boolean", true},
		{"move_backward_zero_onrow", decl, open + "FETCH LAST FROM c INTO x; MOVE BACKWARD 0 FROM c; f:=FOUND; FETCH PRIOR FROM c INTO x;" + close, "f AND x=20", "boolean", true},
		{"move_after_end", decl, open + "MOVE ALL FROM c; MOVE FORWARD 1 FROM c;" + close, "FOUND", "boolean", false},
		// SPI chooses default scrollability from the query plan. This witness
		// deliberately has an uncomplicated, rewindable ordered query.
		{"default_scroll", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; x integer;", "OPEN c; FETCH LAST FROM c INTO x; FETCH PRIOR FROM c INTO x; CLOSE c;", "x", "integer", int64(20)},
		{"no_scroll_forward", "c NO SCROLL CURSOR FOR SELECT i FROM pc_rows ORDER BY i; x integer;", "OPEN c; FETCH c INTO x; CLOSE c;", "x", "integer", int64(10)},
		{"unbound_scroll", "c refcursor; x integer;", "OPEN c SCROLL FOR SELECT i FROM pc_rows ORDER BY i; FETCH LAST FROM c INTO x; CLOSE c;", "x", "integer", int64(30)},
		{"function_label_found", decl, open + "FETCH c INTO x;" + close, "pc_case.found", "boolean", true},
	})
}
