package reference_test

import (
	"database/sql"
	"slices"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoParametersBuiltinNames(t *testing.T) {
	t.Run("named", func(t *testing.T) {
		ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
			// One batched read per consumer covers every declaration, in identity
			// and then ordinal order.
			byIdentity := slices.Clone(ipBuiltinNames)
			identity := func(d ipBuiltinName) string { return d.name + "_" + strconv.Itoa(d.oid) }
			slices.SortFunc(byIdentity, func(x, y ipBuiltinName) int { return strings.Compare(identity(x), identity(y)) })
			var identities []string
			var want [][]any
			for _, d := range byIdentity {
				identities = append(identities, "'"+identity(d)+"'")
				for i, name := range d.names {
					want = append(want, []any{identity(d), int64(i + 1), name})
				}
			}
			irCheck(t, p, b, c, fixture.Select("SELECT specific_name,ordinal_position,parameter_name FROM information_schema.parameters WHERE specific_schema='pg_catalog' AND specific_name IN ("+strings.Join(identities, ",")+") ORDER BY specific_name,ordinal_position", []string{"specific_name", "ordinal_position", "parameter_name"}, []string{"NAME", "INT4", "NAME"}, want))
			// This assertion independently fences the shared-name cutover's pg_proc
			// behavior rather than merely comparing the two consumers with each other.
			byOID := slices.Clone(ipBuiltinNames)
			slices.SortFunc(byOID, func(x, y ipBuiltinName) int { return x.oid - y.oid })
			var oids []string
			var names [][]any
			for _, d := range byOID {
				oids = append(oids, strconv.Itoa(d.oid))
				names = append(names, []any{int64(d.oid), "{" + strings.Join(d.names, ",") + "}"})
			}
			irCheck(t, p, b, c, fixture.Select("SELECT oid::integer AS oid,proargnames::text AS names FROM pg_catalog.pg_proc WHERE oid IN ("+strings.Join(oids, ",")+") ORDER BY oid", []string{"oid", "names"}, []string{"INT4", "TEXT"}, names))
		})
	})
	t.Run("unnamed", func(t *testing.T) {
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
	})
}
