package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func regLookupRelTypeTypmodChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		input    string
		oid, mod int64
	}{
		{"integer", 23, -1}, {"varchar", 1043, -1}, {"varchar(12)", 1043, 16},
		{"varchar(12)[]", 1015, 16}, {"numeric(5,2)", 1700, 327686},
		{"numeric(7,4)[]", 1231, 458760}, {"numeric(2,-3)", 1700, 133121},
		{"time(2)", 1083, 2}, {"timestamp(4) with time zone", 1184, 4},
		{"bit(3)", 1560, 3}, {"bit varying(8)", 1562, 8},
		{"float(24)", 700, -1}, {"float(25)", 701, -1},
		{"char", 1042, 5}, {"character", 1042, 5}, {"char[]", 1014, 5},
		{"character[]", 1014, 5}, {"bit", 1560, 1}, {"bit[]", 1561, 1},
		{"bpchar", 1042, -1}, {"pg_catalog.bpchar", 1042, -1},
		{"pg_catalog.bit", 1560, -1}, {`"bit"`, 1560, -1},
		{`"char"`, 18, -1}, {"varbit", 1562, -1},
		{"pg_catalog.varchar((12))", 1043, 16}, {"pg_catalog.varchar('12')", 1043, 16},
	} {
		t.Run(tc.input, func(t *testing.T) {
			literal := regObjectLiteral(tc.input)
			q := "SELECT to_regtype(" + literal + ")::oid::bigint AS oid,to_regtypemod(" + literal + ") AS modifier"
			regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, q, []string{"oid", "modifier"}, []string{"INT8", "INT4"}, [][]any{{tc.oid, tc.mod}}))
			for _, formal := range []string{"$1", "$1::text"} {
				q = "SELECT to_regtype(" + formal + ")::oid::bigint AS oid,to_regtypemod(" + formal + ") AS modifier"
				s := regLookupRelTypeSelect(b, q, []string{"oid", "modifier"}, []string{"INT8", "INT4"}, [][]any{{tc.oid, tc.mod}}, tc.input)
				regObjectBoth(t, p, b, c, s)
				held := regLookupHeld(t, p, c, q)
				regObjectRunStep(t, p, b, held, s)
				held.Statement.Close()
			}
		})
	}
	regObjectBoth(t, p, b, c, fixture.Select(`SELECT format_type(to_regtype('varchar(32)'),to_regtypemod('varchar(32)')) AS canonical`, []string{"canonical"}, []string{"TEXT"}, [][]any{{"character varying(32)"}}))
}

func TestReferenceRegLookupRelTypeTypmods(t *testing.T) {
	regLookupScenario(t, []string{"rl_mod"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regLookupRelTypeTypmodChecks(t, p, b, c)
		regObjectExec(t, p, b, c, "CREATE SCHEMA rl_mod", "CREATE DOMAIN rl_mod.d AS varchar(12)")
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regtype('rl_mod.d')::oid='rl_mod.d'::regtype::oid AS identity,to_regtypemod('rl_mod.d') AS modifier,to_regtypemod('rl_mod.d[]') AS array_modifier`, []string{"identity", "modifier", "array_modifier"}, []string{"BOOL", "INT4", "INT4"}, [][]any{{true, int64(-1), int64(-1)}}))
		for _, name := range []string{"to_regtype", "to_regtypemod"} {
			t.Run(name+"/domain_modifier", func(t *testing.T) {
				q := "SELECT " + name + "($1)"
				s := regLookupError(q, "42601", `type modifier is not allowed for type "rl_mod.d"`, "rl_mod.d(3)")
				regObjectBoth(t, p, b, c, s)
			})
		}
	})
}
