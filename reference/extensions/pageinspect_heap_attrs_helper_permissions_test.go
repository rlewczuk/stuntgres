package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// SQL-body helper ACL admission precedes row demand, including NULL pages and
// NULL tuple data. Outer grants never substitute for the helpers' current-role
// permissions; the independently specified matrix also runs on pinned 18.6.
func TestReferencePageinspectHeapAttrsHelperPermissions(t *testing.T) {
	for _, helper := range []struct{ name, signature, query string }{
		{"attrs3", "heap_page_item_attrs(bytea,regclass,boolean)", attrsSource2},
		{"heap15", "heap_page_items(bytea)", attrsSource3},
		{"split6", "tuple_data_split(oid,bytea,integer,integer,text,boolean)", attrsSource3},
	} {
		for _, input := range []string{"null-page", "zero-rows", "nonempty", "invalid-tail", "null-tuple-data"} {
			t.Run(helper.name+"-"+input, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"attrs_helper_reader"})
				attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE ROLE attrs_helper_reader", "GRANT USAGE ON SCHEMA public,heap_part TO attrs_helper_reader", "REVOKE EXECUTE ON FUNCTION heap_part." + helper.signature + " FROM PUBLIC"} {
						run(fixture.Exec(q, 0))
					}
					var page any
					switch input {
					case "zero-rows":
						page = make([]byte, 24)
					case "nonempty":
						page = attrsImage(attrsBytes("0102030405060708"), 0, 2, nil, false)
					case "invalid-tail":
						raw := attrsImage(attrsBytes("0102030405060708"), 0, 2, nil, false)
						raw[54] = 23
						page = raw
					case "null-tuple-data":
						raw := attrsImage(attrsBytes("0102030405060708"), 0, 2, nil, false)
						heapPointer(raw, 0, 32767, 1, 32)
						page = raw
					}
					args := []any{page}
					if helper.query == attrsSource3 {
						args = append(args, false)
					}
					st, err := c.PrepareContext(p.Ctx, helper.query)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					held := fixture.HeldStatement{Executor: c, Statement: st, SQL: helper.query}
					run(fixture.Exec("SET SESSION AUTHORIZATION attrs_helper_reader", 0))
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					name := "heap_page_item_attrs"
					if helper.name == "heap15" {
						name = "heap_page_items"
					}
					if helper.name == "split6" {
						name = "tuple_data_split"
					}
					s := heapError(helper.query, "42501", "permission denied for function "+name, args...)
					s.Diagnostic.Context = "SQL function \"heap_page_item_attrs\" statement 1"
					heapBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				})
			})
		}
	}
}
