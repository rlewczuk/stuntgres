package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregateInitialConditionNodes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, tc := range []struct{ name, literal, external string }{
				{"integer_zero", "000", "0"},
				{"quoted_zero", "'000'", "000"},
				{"positive_integer", "+2", "2"},
				{"negative_integer", "-2", "-2"},
				{"decimal", "1.00", "1.00"},
				{"positive_decimal", "+1.00", "1.00"},
				{"exponent", "1e+02", "1e+02"},
				{"negative_decimal", "-1.00", "-1.00"},
				{"minimum_numeric", "-2147483648", "-2147483648"},
				{"name", "seed", "seed"},
				{"qualified_name", "seed.value", "seed.value"},
				{"type_name", "int4", "int4"},
				{"operator", "+", "+"},
				{"star_operator", "*", "*"},
				{"quoted_name", "\"Seed\"", "Seed"},
				{"type_alias", "integer", "pg_catalog.int4"},
				{"array_name", "seed.value[][]", "seed.value[]"},
				{"type_modifier", "varchar(3)", "pg_catalog.varchar"},
				{"setof_type", "SETOF integer", "pg_catalog.int4"},
				{"none", "NONE", "none"},
				{"reserved_keyword", "SELECT", "select"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					name := "ca_init_" + tc.name
					createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE "+name+"(text) (SFUNC=pg_catalog.textcat,STYPE=text,INITCOND = "+tc.literal+")", 0))
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT "+name+"(x) AS v FROM(VALUES('unused'::text)) s(x) WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{tc.external}}))
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.agginitval FROM (SELECT oid FROM pg_proc WHERE proname='"+name+"') p JOIN pg_aggregate a ON p.oid=a.aggfnoid", []string{"agginitval"}, []string{"TEXT"}, [][]any{{tc.external}}))
				})
			}
		})
	}
}

func TestReferenceCreateAggregateKeywordNames(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TABLE aggregate(aggregate integer)", 0),
			fixture.Exec("INSERT INTO aggregate VALUES(1),(2),(3)", 3),
			fixture.Exec("CREATE FUNCTION aggregate(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_keyword_sum(integer) (SFUNC=aggregate,STYPE=integer,INITCOND='0')", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT aggregate(aggregate,10) AS v FROM aggregate ORDER BY aggregate", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}, {int64(13)}}))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_keyword_sum(aggregate) AS v FROM aggregate", int64(6)))
	})
}
