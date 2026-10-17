//go:build !noservertests

package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerBooleanCallRelationList(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bcp_list_owner"})
	for _, schema := range []string{"bcp_list_a", "bcp_list_b", "bcp_list_off"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverDial(t, f.DSNFor(b))
			serverWantCode(t, w.login(t, f.DSNFor(b), 196608), "")
			check := func(path string, want [][]any) {
				t.Helper()
				frames := w.query(t, "SET search_path="+path)
				serverWantCode(t, frames, "")
				frames = w.query(t, fixture.BooleanCallRelationList)
				serverWantCode(t, frames, "")
				serverWantReady(t, frames, 'I')
				serverWantCompletions(t, frames, fmt.Sprintf("SELECT %d", len(want)))
				var values [][]string
				var text strings.Builder
				text.WriteString("SET\n")
				for _, row := range want {
					cells := make([]string, len(row))
					for i, v := range row {
						cells[i] = v.(string)
					}
					values = append(values, cells)
					text.WriteString(strings.Join(cells, "|"))
					text.WriteByte('\n')
				}
				serverWantValues(t, frames, values)
				desc := serverDescriptions(t, frames)
				if len(desc) != 1 || len(desc[0]) != 4 {
					t.Fatalf("relation-list descriptions %+v", desc)
				}
				for i, name := range []string{"Schema", "Name", "Type", "Owner"} {
					c := desc[0][i]
					if c.name != name || c.oid != []uint32{19, 19, 25, 19}[i] || c.typmod != -1 || c.format != 0 || c.length != []int16{64, 64, -1, 64}[i] {
						t.Fatalf("column %d: %+v", i, c)
					}
				}
				out, err := f.PSQL(t, b, "SET search_path="+path+";\n"+fixture.BooleanCallRelationList+"\n")
				if err != nil || out != text.String() {
					t.Fatalf("actual psql relation list %q want %q: %v", out, text.String(), err)
				}
			}
			check("public", nil)
			for _, q := range fixture.BooleanCallRelationSetup() {
				frames := w.query(t, q)
				serverWantCode(t, frames, "")
				serverWantReady(t, frames, 'I')
			}
			check("bcp_list_a,bcp_list_b,pg_catalog", fixture.BooleanCallRelationRows(false))
			check("bcp_list_b,bcp_list_a,pg_catalog", fixture.BooleanCallRelationRows(true))
			frames := w.query(t, "CREATE TABLE public.bcp_server_sentinel(id int)")
			serverWantCode(t, frames, "")
			q := "INSERT INTO public.bcp_server_sentinel VALUES(1); SELECT 1 WHERE pg_catalog.pg_table_is_visible(1) AND"
			frames = w.query(t, q)
			serverWantCode(t, frames, "42601")
			serverWantCompletions(t, frames)
			serverWantReady(t, frames, 'I')
			serverScalar(t, w, "SELECT count(*) FROM public.bcp_server_sentinel", "0")
		})
	}
}
