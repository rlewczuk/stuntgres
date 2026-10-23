package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePLpgSQLCursorFoundNamespace(t *testing.T) {
	// Released pl_comp inserts all named/$n formals canonically, then adds
	// the magic FOUND variable through ordinary lexical namespace insertion.
	// These independently specified witnesses distinguish lookup from formal
	// storage without assuming either namespace can be discarded.
	cases := []struct {
		name, args, result, body, call, typ string
		want                                any
	}{
		{"input_bare", "found boolean", "boolean", "BEGIN RETURN found; END", "true", "BOOL", false},
		{"input_position", "found boolean", "boolean", "BEGIN RETURN $1; END", "true", "BOOL", true},
		{"input_qualified", "found boolean", "boolean", "BEGIN RETURN pc_found_gate.found; END", "true", "BOOL", false},
		{"fetch_bare", "found boolean", "boolean", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c; RETURN found; END", "false", "BOOL", true},
		{"fetch_position", "found boolean", "boolean", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c; RETURN $1; END", "false", "BOOL", false},
		{"fetch_qualified", "found boolean", "boolean", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c; RETURN pc_found_gate.found; END", "false", "BOOL", true},
		{"local_shadow", "found boolean", "boolean", "DECLARE found boolean:=true; BEGIN RETURN found; END", "false", "BOOL", true},
		{"local_qualified_outer", "found boolean", "boolean", "DECLARE found boolean:=true; BEGIN RETURN pc_found_gate.found; END", "true", "BOOL", false},
		{"alias_position", "found boolean", "boolean", "DECLARE a ALIAS FOR $1; BEGIN RETURN a; END", "true", "BOOL", true},
		{"alias_magic", "found boolean", "boolean", "DECLARE a ALIAS FOR found; BEGIN RETURN a; END", "true", "BOOL", false},
		{"copied_magic_type", "found integer", "boolean", "DECLARE x found%TYPE:=true; BEGIN RETURN NOT x; END", "7", "BOOL", false},
		{"copied_position_alias_type", "found integer", "integer", "DECLARE a ALIAS FOR $1; x a%TYPE:=7; BEGIN RETURN x+1; END", "7", "INT4", int64(8)},
		{"out_bare", "OUT found boolean", "boolean", "BEGIN found:=true; RETURN; END", "", "BOOL", nil},
		{"out_position", "OUT found boolean", "boolean", "BEGIN $1:=true; RETURN; END", "", "BOOL", true},
		{"out_qualified", "OUT found boolean", "boolean", "BEGIN pc_found_gate.found:=true; RETURN; END", "", "BOOL", nil},
		{"inout_bare", "INOUT found boolean", "boolean", "BEGIN found:=false; RETURN; END", "true", "BOOL", true},
		{"inout_position", "INOUT found boolean", "boolean", "BEGIN $1:=false; RETURN; END", "true", "BOOL", false},
		{"inout_qualified", "INOUT found boolean", "boolean", "BEGIN pc_found_gate.found:=false; RETURN; END", "true", "BOOL", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorFunction(t, p, b, c, "pc_found_gate", tc.args, tc.result, tc.body)
				plCursorValue(t, p, b, c, "SELECT pc_found_gate("+tc.call+") AS v", tc.typ, tc.want)
			})
		})
	}
}
