package reference_test

import "testing"

func TestReferencePLpgSQLCursorOpen(t *testing.T) {
	plCursorBoundOrdering(t)
	plCursorCases(t, []plCursorCase{
		{"unbound", "c refcursor; x integer;", "OPEN c FOR SELECT 7 AS i; FETCH c INTO x; CLOSE c;", "x", "integer", int64(7)},
		{"values_query", "c refcursor; x integer;", "OPEN c FOR VALUES (7); FETCH c INTO x; CLOSE c;", "x", "integer", int64(7)},
		{"bound_values", "c CURSOR(a integer) FOR VALUES (a); x integer;", "OPEN c(7); FETCH c INTO x; CLOSE c;", "x", "integer", int64(7)},
		{"qualified_argument", "c CURSOR(a integer) FOR SELECT c.a AS i; x integer;", "OPEN c(7); FETCH c INTO x; CLOSE c;", "x", "integer", int64(7)},
		{"nested_open_lexical_scope", "a integer:=10; c CURSOR FOR SELECT a AS i; x integer;", "DECLARE a integer:=20; BEGIN OPEN c; END; FETCH c INTO x; CLOSE c;", "x", "integer", int64(10)},
		{"alias_cursor", "c refcursor; d ALIAS FOR c; x integer;", "OPEN d FOR SELECT 7; FETCH c INTO x; CLOSE d;", "x", "integer", int64(7)},
		{"shadow_found", "c refcursor; found integer:=99; x integer;", "OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c;", "found", "integer", int64(99)},
		{"initial_null", "c refcursor; b CURSOR FOR SELECT 7;", "", "c IS NULL AND b IS NULL", "boolean", true},
		{"bound_positional", "c CURSOR(a integer,b integer) FOR SELECT a+b AS i; x integer;", "OPEN c(7,8); FETCH c INTO x; CLOSE c;", "x", "integer", int64(15)},
		{"bound_named", "c CURSOR(a integer,b integer) FOR SELECT a+b AS i; x integer;", "OPEN c(b:=8,a:=7); FETCH c INTO x; CLOSE c;", "x", "integer", int64(15)},
		{"bound_arrow", "c CURSOR(a integer,b integer) IS SELECT a+b AS i; x integer;", "OPEN c(b=>8,a=>7); FETCH c INTO x; CLOSE c;", "x", "integer", int64(15)},
		{"bound_mixed", "c CURSOR(a integer,b integer) FOR SELECT a+b AS i; x integer;", "OPEN c(7,b=>8); FETCH c INTO x; CLOSE c;", "x", "integer", int64(15)},
		{"positional_after_named", "c CURSOR(a integer,b integer) FOR SELECT a+b AS i; x integer;", "OPEN c(a=>7,8); FETCH c INTO x; CLOSE c;", "x", "integer", int64(15)},
		{"capture_at_open", "a integer:=10; c CURSOR FOR SELECT a AS i; x integer;", "OPEN c; a:=20; FETCH c INTO x; CLOSE c;", "x", "integer", int64(10)},
		{"unbound_capture", "a integer:=10; c refcursor; x integer;", "OPEN c FOR SELECT a AS i; a:=20; FETCH c INTO x; CLOSE c;", "x", "integer", int64(10)},
		{"declaration_scope", "c CURSOR FOR SELECT i FROM pc_rows WHERE i=10; i integer:=30; x integer;", "OPEN c; FETCH c INTO x; CLOSE c;", "x", "integer", int64(10)},
		{"argument_namespace", "a integer:=100; c CURSOR(a integer) FOR SELECT a AS i; x integer;", "OPEN c(7); FETCH c INTO x; CLOSE c;", "x", "integer", int64(7)},
		{"explicit_name", "c refcursor:='pc_explicit'; x integer;", "OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c;", "c", "refcursor", "pc_explicit"},
		{"generated_identity", "c refcursor; d refcursor;", "OPEN c FOR SELECT 7; OPEN d FOR SELECT 8; CLOSE c; CLOSE d;", "c IS NOT NULL AND d IS NOT NULL AND c::text<>'' AND d::text<>'' AND c::text<>d::text", "boolean", true},
		{"nested_frame_survival", "c refcursor; x integer;", "BEGIN OPEN c FOR SELECT 7; END; FETCH c INTO x; CLOSE c;", "x", "integer", int64(7)},
		{"found_unbound", "c refcursor; f boolean;", "f:=FOUND; OPEN c FOR SELECT 7; CLOSE c;", "NOT f AND NOT FOUND", "boolean", true},
		{"found_bound_noargs", "c CURSOR FOR SELECT 7;", "OPEN c; CLOSE c;", "FOUND", "boolean", false},
		// Bound OPEN's internal SELECT INTO argument evaluation sets FOUND.
		{"found_bound_arguments", "c CURSOR(a integer) FOR SELECT a;", "OPEN c(7); CLOSE c;", "FOUND", "boolean", true},
	})
}
