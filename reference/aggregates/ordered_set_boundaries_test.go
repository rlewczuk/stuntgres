package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// PG-positive witnesses deliberately distinguish current native codec limits.
func TestReferenceOrderedSetAggregateCarrierBoundaries(t *testing.T) {
	orderedSetScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TYPE os_composite AS (a INT,b TEXT)", 0))
		q := "SELECT mode() WITHIN GROUP(ORDER BY ROW(1,'a')::os_composite)::text AS v"
		s := orderedSetValue(q, "TEXT", "(1,a)")
		if !b.PG {
			s = orderedSetDiagnostic(q, "0A000", "aggregate ordering is unsupported for type public.os_composite", "ROW", "")
		}
		orderedSetBoth(t, p, c, b, s)
		// Private trees have a catalog carrier, not a text input codec. Keep
		// the oracle independent of PostgreSQL's serialized expression format.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE os_tree_boundary(v INT DEFAULT 1)", 0))
		q = "SELECT mode() WITHIN GROUP(ORDER BY adbin) IS NOT NULL AS v FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='os_tree_boundary'"
		s = orderedSetValue(q, "BOOL", true)
		if !b.PG {
			s = orderedSetDiagnostic(q, "0A000", "aggregate ordering is unsupported for type pg_node_tree", "adbin)", "")
		}
		orderedSetBoth(t, p, c, b, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE os_char_boundary(id INT)", 0))
		for _, tc := range []struct{ typ, value, want string }{
			{"inet", "10.0.0.1", "{10.0.0.1}"}, {"cidr", "10.0.0.0/24", "{10.0.0.0/24}"}, {"xid8", "1", "{1}"}, {"money", "1", "{$1.00}"}, {"regoper", "1", "{1}"}, {"regoperator", "1", "{1}"},
			{"regconfig", "-", "{-}"}, {"regdictionary", "-", "{-}"},
		} {
			q := "SELECT percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY '" + tc.value + "'::" + tc.typ + ")::text AS v"
			s := orderedSetValue(q, "TEXT", tc.want)
			if !b.PG && tc.typ != "regconfig" && tc.typ != "regdictionary" {
				// resolveArrayAggregateSignature's existing no-executable-array
				// diagnostic has no Position; native implementation must reuse it.
				s = orderedSetDiagnostic(q, "42704", "could not find array type for data type "+tc.typ, "", "")
			}
			orderedSetBoth(t, p, c, b, s)
		}
		// Internal "char" has an executable array companion. A table's
		// relkind is r, so percentile_disc must produce {r} on both backends.
		// Text-search aliases above also retain executable array companions.
		q = "SELECT percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY relkind)::text AS v FROM pg_catalog.pg_class WHERE relname='os_char_boundary'"
		s = orderedSetValue(q, "TEXT", "{r}")
		orderedSetBoth(t, p, c, b, s)
		// Reach ordering admission through existing storage codecs. A raw
		// literal CAST is not an admitted route for every range family.
		for _, tc := range []struct{ name, typ, input, want string }{
			{"arrays", "integer[]", "{1,2}", "{1,2}"},
			{"int4range", "int4range", "[1,3)", "[1,3)"},
			{"int8range", "int8range", "empty", "empty"},
			{"numrange", "numrange", "empty", "empty"},
			{"daterange", "daterange", "empty", "empty"},
			{"tsrange", "tsrange", "empty", "empty"},
			{"tstzrange", "tstzrange", "empty", "empty"},
			{"int4multirange", "int4multirange", "{}", "{}"},
			{"int8multirange", "int8multirange", "{}", "{}"},
			{"nummultirange", "nummultirange", "{}", "{}"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				table := "os_ordering_" + tc.name
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(item "+tc.typ+")", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES('"+tc.input+"'),(NULL)", 2))
				q := "SELECT mode() WITHIN GROUP(ORDER BY item)::text AS v FROM " + table
				s := orderedSetValue(q, "TEXT", tc.want)
				if !b.PG {
					s = orderedSetDiagnostic(q, "0A000", "aggregate ordering is unsupported for type "+tc.typ, "item)", "")
				}
				orderedSetBoth(t, p, c, b, s)
			})
		}
		// BIT/VARBIT storage is admitted; general explicit casts are not.
		// Reach the actual ordering guard through typed table columns.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE os_bit_boundary(b BIT(1),v VARBIT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO os_bit_boundary VALUES(B'1',B'10')", 1))
		for _, tc := range []struct{ column, typ, want string }{{"b", "bit", "1"}, {"v", "bit varying", "10"}} {
			q := "SELECT mode() WITHIN GROUP(ORDER BY " + tc.column + ")::text AS v FROM os_bit_boundary"
			s := orderedSetValue(q, "TEXT", tc.want)
			if !b.PG {
				s = orderedSetDiagnostic(q, "0A000", "aggregate ordering is unsupported for type "+tc.typ, tc.column+")", "")
			}
			orderedSetBoth(t, p, c, b, s)
		}
	})
}
