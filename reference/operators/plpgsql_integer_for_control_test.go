package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLIntegerForControl(t *testing.T) {
	for _, tc := range []struct {
		name, declarations, body, typ string
		want                          any
	}{
		{"continue", "s integer:=0;", "FOR i IN 1..5 LOOP CONTINUE WHEN i%2=0; s:=s+i; END LOOP; RETURN s;", "INT4", int64(9)},
		{"exit", "s integer:=0;", "FOR i IN 1..5 LOOP s:=i; EXIT; END LOOP; IF FOUND THEN RETURN s; END IF; RETURN -1;", "INT4", int64(1)},
		{"label_counter", "s integer:=0;", "<<o>> FOR i IN 1..2 LOOP <<n>> FOR i IN 3..4 LOOP s:=s+o.i*10+n.i; END LOOP n; END LOOP o; RETURN s;", "INT4", int64(74)},
		{"nearest_duplicate", "s integer:=0;", "<<x>> FOR i IN 1..2 LOOP <<x>> FOR i IN 3..4 LOOP s:=s+x.i; EXIT x; END LOOP; END LOOP; RETURN s;", "INT4", int64(6)},
		{"unicode_label", "s integer:=0;", "<<\"é終\">> FOR i IN 1..3 LOOP s:=s+\"é終\".i; CONTINUE \"é終\"; s:=99; END LOOP \"é終\"; RETURN s;", "INT4", int64(6)},
		{"outward_continue", "s text:='';", "<<o>> FOR i IN 1..3 LOOP FOR j IN 1..3 LOOP s:=s||i::text||j::text; CONTINUE o; END LOOP; s:=s||'bad'; END LOOP; RETURN s;", "TEXT", "112131"},
		{"outward_exit", "s integer:=0;", "<<o>> FOR i IN 1..3 LOOP FOR j IN 1..3 LOOP s:=s+1; EXIT o; END LOOP; s:=99; END LOOP; RETURN s;", "INT4", int64(1)},
		{"block_exit", "s integer:=0;", "<<b>> BEGIN FOR i IN 1..3 LOOP s:=i; EXIT b; END LOOP; s:=99; END b; RETURN s;", "INT4", int64(1)},
		{"return", "", "FOR i IN 1..3 LOOP RETURN i; END LOOP; RETURN 99;", "INT4", int64(1)},
		{"found_before_after", "s text:='';", "FOR i IN 1..1 LOOP s:=s||FOUND::text; FOUND:=false; END LOOP; RETURN s||FOUND::text;", "TEXT", "falsetrue"},
		{"empty_resets", "", "FOUND:=true; FOR i IN 3..1 LOOP RETURN true; END LOOP; RETURN FOUND;", "BOOL", false},
		{"nested_empty_found", "s text:='';", "FOR i IN 1..2 LOOP FOR j IN 2..1 LOOP END LOOP; s:=s||FOUND::text; END LOOP; RETURN s||FOUND::text;", "TEXT", "falsefalsetrue"},
		{"counter_found", "s text:='';", "FOR found IN 1..2 LOOP s:=s||if_type_name(found)||if_control.found::text; END LOOP; RETURN s||FOUND::text;", "TEXT", "integerfalseintegerfalsetrue"},
		{"error_no_publish", "", "FOUND:=false; BEGIN FOR i IN 1..2 LOOP IF i=2 THEN RETURN 1/0; END IF; END LOOP; EXCEPTION WHEN division_by_zero THEN RETURN FOUND; END; RETURN true;", "BOOL", false},
		{"unreachable_binding", "", "IF false THEN FOR i IN missing_integer_bound()..3 LOOP END LOOP; END IF; RETURN 7;", "INT4", int64(7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.name == "counter_found" {
					integerForTypeWitness(t, p, b, c)
				}
				d := ""
				if tc.declarations != "" {
					d = "DECLARE " + tc.declarations
				}
				ret := map[string]string{"INT4": "integer", "TEXT": "text", "BOOL": "boolean"}[tc.typ]
				userOperatorExec(t, p, b, c, "CREATE FUNCTION if_control() RETURNS "+ret+" LANGUAGE plpgsql AS $$"+d+" BEGIN "+tc.body+" END$$")
				plpgsqlValue(t, p, b, c, "SELECT if_control() AS v", tc.typ, tc.want)
			})
		})
	}
}
