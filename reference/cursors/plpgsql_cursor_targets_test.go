package reference_test

import "testing"

func TestReferencePLpgSQLCursorTargets(t *testing.T) {
	plCursorCases(t, []plCursorCase{
		{"scalar_list", "c refcursor; i integer; t text;", "OPEN c FOR SELECT 7,'seven'::text; FETCH c INTO i,t; CLOSE c;", "i=7 AND t='seven'", "boolean", true},
		{"scalar_missing", "c refcursor; i integer; t text:='old';", "OPEN c FOR SELECT 7; FETCH c INTO i,t; CLOSE c;", "i=7 AND t IS NULL", "boolean", true},
		{"scalar_extra", "c refcursor; i integer;", "OPEN c FOR SELECT 7,'ignored'::text; FETCH c INTO i; CLOSE c;", "i", "integer", int64(7)},
		{"ordinal_conversion", "c refcursor; r pc_rows%ROWTYPE;", "OPEN c FOR SELECT '42'::text,7; FETCH c INTO r; CLOSE c;", "r.i=42 AND r.t='7'", "boolean", true},
		{"row_missing", "c refcursor; r pc_rows%ROWTYPE;", "r.i:=99; r.t:='old'; OPEN c FOR SELECT 7; FETCH c INTO r; CLOSE c;", "r.i=7 AND r.t IS NULL", "boolean", true},
		{"row_extra", "c refcursor; r pc_rows%ROWTYPE;", "OPEN c FOR SELECT 7,'seven'::text,99; FETCH c INTO r; CLOSE c;", "r.i=7 AND r.t='seven'", "boolean", true},
		{"scalar_clear", "c refcursor; i integer:=99; t text:='old';", "OPEN c FOR SELECT pc_rows.i,pc_rows.t FROM pc_rows WHERE false; FETCH c INTO i,t; CLOSE c;", "i IS NULL AND t IS NULL AND NOT FOUND", "boolean", true},
		{"populated_scalar_clear", "c refcursor; i integer; t text;", "OPEN c FOR SELECT 7,'seven'::text; FETCH c INTO i,t; FETCH c INTO i,t; CLOSE c;", "i IS NULL AND t IS NULL AND NOT FOUND", "boolean", true},
		{"empty_record_shape", "c refcursor; r record;", "OPEN c FOR SELECT i,t FROM pc_rows WHERE false; FETCH c INTO r; CLOSE c;", "r.i IS NULL AND r.t IS NULL AND NOT FOUND", "boolean", true},
		{"record_clear_and_refill", "c refcursor; r record;", "OPEN c SCROLL FOR SELECT 7 AS i,'seven'::text AS t; FETCH c INTO r; FETCH c INTO r; FETCH FIRST FROM c INTO r; CLOSE c;", "r.i=7 AND r.t='seven' AND FOUND", "boolean", true},
		{"record_clear", "c refcursor; r record;", "OPEN c FOR SELECT 7 AS i,'seven'::text AS t; FETCH c INTO r; FETCH c INTO r; CLOSE c;", "r.i IS NULL AND r.t IS NULL", "boolean", true},
		{"known_row_empty", "c refcursor; r pc_rows%ROWTYPE;", "OPEN c FOR SELECT i,t FROM pc_rows WHERE false; FETCH c INTO r; CLOSE c;", "r.i IS NULL AND r.t IS NULL", "boolean", true},
		{"field_target", "c refcursor; r pc_rows%ROWTYPE;", "OPEN c FOR SELECT 7,'seven'::text; FETCH c INTO r.i,r.t; CLOSE c;", "r.i=7 AND r.t='seven'", "boolean", true},
		{"zero_width_row", "c refcursor; r record;", "OPEN c FOR SELECT FROM pc_rows; FETCH c INTO r; CLOSE c;", "FOUND", "boolean", true},
		{"zero_width_empty", "c refcursor; r record;", "OPEN c FOR SELECT FROM pc_rows WHERE false; FETCH c INTO r; CLOSE c;", "FOUND", "boolean", false},
	})
}
