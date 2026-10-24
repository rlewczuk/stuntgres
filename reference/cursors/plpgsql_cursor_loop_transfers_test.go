package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorLoopTransfers(t *testing.T) {
	for _, tc := range []struct {
		name, declarations, body, result, typ string
		want                                  any
	}{
		{"exit", "n integer:=0;", "FOR r IN c LOOP n:=n+r.i; EXIT; END LOOP;", "n", "INT4", int64(10)},
		{"continue", "n integer:=0;", "FOR r IN c LOOP CONTINUE WHEN r.i=20; n:=n+r.i; END LOOP;", "n", "INT4", int64(40)},
		{"exit_found", "", "FOR r IN c LOOP EXIT; END LOOP;", "FOUND", "BOOL", true},
		{"exit_null_restored", "", "FOR r IN c LOOP EXIT; END LOOP;", "c IS NULL", "BOOL", true},
		{"exit_explicit_preserved", "", "c:='pc_transfer_owned'; FOR r IN c LOOP EXIT; END LOOP;", "c", "REFCURSOR", "pc_transfer_owned"},
		{"outer_continue_reopens", "i integer:=0; n integer:=0;", "<<o>> LOOP i:=i+1; EXIT WHEN i>3; FOR r IN c LOOP n:=n+r.i; CONTINUE o; END LOOP; n:=999; END LOOP;", "n", "INT4", int64(30)},
		{"outer_exit", "n integer:=0;", "<<o>> LOOP FOR r IN c LOOP n:=n+r.i; EXIT o; END LOOP; n:=999; END LOOP;", "n", "INT4", int64(10)},
		{"block_exit_found", "", "<<b>> BEGIN FOR r IN c LOOP EXIT b; END LOOP; END b;", "FOUND", "BOOL", true},
		{"block_exit_null_restored", "", "<<b>> BEGIN FOR r IN c LOOP EXIT b; END LOOP; END b;", "c IS NULL", "BOOL", true},
		{"target_outer_cursor_continue", "n integer:=0;", "<<rows_loop>> FOR r IN c LOOP LOOP CONTINUE rows_loop WHEN r.i=20; n:=n+r.i; EXIT; END LOOP; END LOOP rows_loop;", "n", "INT4", int64(40)},
		{"target_outer_cursor_exit", "n integer:=0;", "<<rows_loop>> FOR r IN c LOOP LOOP n:=n+r.i; EXIT rows_loop WHEN r.i=20; EXIT; END LOOP; END LOOP rows_loop;", "n", "INT4", int64(30)},
		{"nested_cursor_reopens", "d CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0;", "<<outer_rows>> FOR r IN c LOOP FOR s IN d LOOP n:=n+r.i+s.i; CONTINUE outer_rows; END LOOP; n:=999; END LOOP;", "n", "INT4", int64(90)},
		{"continue_keeps_portal", "x integer; n integer:=0;", "FOR r IN c LOOP CONTINUE WHEN r.i=10; n:=n+r.i; END LOOP; FOR r IN c LOOP n:=n+r.i; END LOOP;", "n", "INT4", int64(110)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					plCursorRows(t, p, b, c)
					plCursorFunction(t, p, b, c, "pc_transfer", "", map[string]string{"INT4": "integer", "BOOL": "boolean", "REFCURSOR": "refcursor"}[tc.typ], "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; "+tc.declarations+" BEGIN "+tc.body+" RETURN "+tc.result+"; END")
					// Keep an explicit transaction so pg_cursors observes cleanup
					// before end-of-transaction portal disposal could hide a leak.
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					defer func() { _, _ = c.ExecContext(p.Ctx, "ROLLBACK") }()
					plCursorValue(t, p, b, c, "SELECT pc_transfer() AS v", tc.typ, tc.want)
					plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='pc_transfer_owned' OR statement='SELECT i FROM pc_rows ORDER BY i'", "INT8", int64(0))
				})
			}
		})
	}
}
