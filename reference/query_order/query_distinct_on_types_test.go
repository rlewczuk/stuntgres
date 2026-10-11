package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctOnTypedKeys(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for i, tc := range []struct {
			typ, values string
			count       int64
		}{
			{"NUMERIC", "(1.0),(1.00),(2.0),(NULL),(NULL)", 3},
			{"DOUBLE PRECISION", "('0'),('-0'),('NaN'),('NaN'),('1'),(NULL),(NULL)", 4},
			{"REAL", "('0'),('-0'),('NaN'),('NaN'),('1'),(NULL),(NULL)", 4},
			{"CHAR(4)", "('a'),('a '),('a  '),('A'),(NULL),(NULL)", 3},
			{"TEXT", "('a'),('a'),('a '),('A'),(NULL),(NULL)", 4},
			{"JSONB", `('{"a":1,"b":2}'),('{"b":2,"a":1.00}'),('{"a":2}'),(NULL),(NULL)`, 3},
			{"INT[]", "('{1,2}'),('{1,2}'),('[0:1]={1,2}'),('{{1,2}}'),('{}'),('{NULL}'),(NULL),(NULL)", 6},
			{"NUMERIC[]", "('{1.0,NULL}'),('{1.00,NULL}'),('{2,NULL}'),(NULL),(NULL)", 3},
			{"CHAR(3)[]", "('{a,NULL}'),('{\"a \",NULL}'),('{A,NULL}'),(NULL),(NULL)", 3},
			{"DOUBLE PRECISION[]", "('{0,NaN}'),('{-0,NaN}'),('{1,NaN}'),(NULL),(NULL)", 3},
		} {
			table := fmt.Sprintf("on_typed_%d", i)
			run(fixture.Exec("CREATE TABLE "+table+"(v "+tc.typ+")", 0))
			run(fixture.Exec("INSERT INTO "+table+" VALUES"+tc.values, int64(1+strings.Count(tc.values, "),("))))
			// Hidden key equality is required even though the visible payload is
			// constant. No array ORDER capability is requested by this witness.
			run(fixture.Exec("CREATE VIEW "+table+"_v AS SELECT DISTINCT ON(v) 17 AS payload FROM "+table, 0))
			fixture.QueryDistinctBoth(run, fixture.Select("SELECT count(*) AS n FROM "+table+"_v", []string{"n"}, []string{"INT8"}, [][]any{{tc.count}}))
			fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(v) 17 AS payload FROM "+table+" WHERE FALSE", []string{"payload"}, []string{"INT4"}, [][]any{}))
		}
		run(fixture.Exec("CREATE DOMAIN on_amount AS NUMERIC(7,2)", 0))
		run(fixture.Exec("CREATE TYPE on_mood AS ENUM('z','a')", 0))
		run(fixture.Exec("CREATE DOMAIN on_mood_domain AS on_mood", 0))
		run(fixture.Exec("CREATE DOMAIN on_element AS INT", 0))
		run(fixture.Exec("CREATE DOMAIN on_array AS INT[]", 0))
		run(fixture.Exec("CREATE DOMAIN on_nested AS on_array[]", 0))
		for i, tc := range []struct {
			typ, values string
			count       int64
		}{
			{"on_amount", "(1),(1),(2),(NULL),(NULL)", 3},
			{"on_mood_domain", "('z'),('z'),('a'),(NULL),(NULL)", 3},
			{"on_element[]", "('{1,NULL}'),('{1,NULL}'),('{}'),(NULL),(NULL)", 3},
			{"on_array", "('{1,NULL}'),('{1,NULL}'),('{}'),(NULL),(NULL)", 3},
			{"on_nested", `('{"{1,NULL}",NULL}'),('{"{1,NULL}",NULL}'),('{}'),(NULL),(NULL)`, 3},
		} {
			table := fmt.Sprintf("on_domain_%d", i)
			run(fixture.Exec("CREATE TABLE "+table+"(v "+tc.typ+")", 0))
			run(fixture.Exec("INSERT INTO "+table+" VALUES"+tc.values, 5))
			run(fixture.Exec("CREATE VIEW "+table+"_v AS SELECT DISTINCT ON(v) 17 AS payload FROM "+table, 0))
			fixture.QueryDistinctBoth(run, fixture.Select("SELECT count(*) AS n FROM "+table+"_v", []string{"n"}, []string{"INT8"}, [][]any{{tc.count}}))
		}
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(v) v::text AS mood FROM on_domain_1 ORDER BY v", []string{"mood"}, []string{"TEXT"}, [][]any{{"z"}, {"a"}, {nil}}))
		run(fixture.Exec("CREATE TABLE enum_flags(id INT)", 0))
		run(fixture.Exec("INSERT INTO enum_flags VALUES(1),(1),(2),(NULL),(NULL)", 5))
		key := "CASE id WHEN 1 THEN '{z,NULL}'::on_mood_domain[] WHEN 2 THEN '{a,NULL}'::on_mood_domain[] ELSE NULL::on_mood_domain[] END"
		run(fixture.Exec("CREATE VIEW on_enum_arrays AS SELECT DISTINCT ON("+key+") 17 AS payload FROM enum_flags", 0))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT count(*) AS n FROM on_enum_arrays", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		// JSON payload has no equality provider, but ON only needs integer k.
		run(fixture.Exec("CREATE TABLE json_payload(id INT,k INT,j JSON)", 0))
		run(fixture.Exec("INSERT INTO json_payload VALUES(1,1,'{\"old\":1}'),(2,1,'{\"new\":2}'),(3,NULL,NULL)", 3))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(k) id,j FROM json_payload ORDER BY k,id DESC", []string{"id", "j"}, []string{"INT4", "JSON"}, [][]any{{int64(2), `{"new":2}`}, {int64(3), nil}}))
	})
}

func TestReferenceQueryDistinctOnTreeKeys(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE tree_source(a INT DEFAULT 1,b INT DEFAULT 2)", 0))
		run(fixture.Exec("CREATE TABLE repeats(id INT)", 0))
		run(fixture.Exec("INSERT INTO repeats VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE VIEW on_tree AS SELECT DISTINCT ON(d.adbin) pg_get_expr(d.adbin,0) AS definition FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid CROSS JOIN repeats WHERE c.relname='tree_source'", 0))
		s := fixture.Select("SELECT definition FROM on_tree ORDER BY definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"1"}, {"2"}})
		fixture.QueryDistinctBoth(run, s)
		queryDistinctOnHeld(t, p, b, c, s)
		if !b.PG {
			queryDistinctOnNativeReplay(t, p, s)
		}
	})
}
