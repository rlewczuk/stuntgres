package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoParametersBuiltinNames(t *testing.T) {
	for _, d := range ipBuiltinNames {
		t.Run(d.name+"_"+strconv.Itoa(d.oid), func(t *testing.T) {
			ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
				var want [][]any
				for i, name := range d.names {
					want = append(want, []any{int64(i + 1), name})
				}
				identity := d.name + "_" + strconv.Itoa(d.oid)
				s := fixture.Select("SELECT ordinal_position,parameter_name FROM information_schema.parameters WHERE specific_schema='pg_catalog' AND specific_name='"+identity+"' ORDER BY ordinal_position", []string{"ordinal_position", "parameter_name"}, []string{"INT4", "NAME"}, want)
				irCheck(t, p, b, c, s)
				// This assertion independently fences the shared-name cutover's pg_proc
				// behavior rather than merely comparing the two consumers with each other.
				names := fixture.Select("SELECT proargnames::text AS names FROM pg_catalog.pg_proc WHERE oid="+strconv.Itoa(d.oid), []string{"names"}, []string{"TEXT"}, [][]any{{"{" + strings.Join(d.names, ",") + "}"}})
				irCheck(t, p, b, c, names)
			})
		})
	}
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
		// Genuinely unnamed declarations remain unnamed, including the one-input
		// array_sort overload and both normalization slots despite their defaults.
		for _, x := range []struct {
			id    string
			arity int
		}{{"array_sort_6388", 1}, {"normalize_4350", 2}, {"is_normalized_4351", 2}} {
			var want [][]any
			for i := range x.arity {
				want = append(want, []any{int64(i + 1), nil})
			}
			irCheck(t, p, b, c, fixture.Select("SELECT ordinal_position,parameter_name FROM information_schema.parameters WHERE specific_name='"+x.id+"' ORDER BY ordinal_position", []string{"ordinal_position", "parameter_name"}, []string{"INT4", "NAME"}, want))
		}
	})
}
