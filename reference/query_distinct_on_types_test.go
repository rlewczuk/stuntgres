package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceQueryDistinctOnTypedKeys(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
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
			run(referenceExec("CREATE TABLE "+table+"(v "+tc.typ+")", 0))
			run(referenceExec("INSERT INTO "+table+" VALUES"+tc.values, int64(1+strings.Count(tc.values, "),("))))
			// Hidden key equality is required even though the visible payload is
			// constant. No array ORDER capability is requested by this witness.
			run(referenceExec("CREATE VIEW "+table+"_v AS SELECT DISTINCT ON(v) 17 AS payload FROM "+table, 0))
			queryDistinctBoth(run, referenceSelect("SELECT count(*) AS n FROM "+table+"_v", []string{"n"}, []string{"INT8"}, [][]any{{tc.count}}))
			queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(v) 17 AS payload FROM "+table+" WHERE FALSE", []string{"payload"}, []string{"INT4"}, [][]any{}))
		}
		run(referenceExec("CREATE DOMAIN on_amount AS NUMERIC(7,2)", 0))
		run(referenceExec("CREATE TYPE on_mood AS ENUM('z','a')", 0))
		run(referenceExec("CREATE DOMAIN on_mood_domain AS on_mood", 0))
		run(referenceExec("CREATE DOMAIN on_element AS INT", 0))
		run(referenceExec("CREATE DOMAIN on_array AS INT[]", 0))
		run(referenceExec("CREATE DOMAIN on_nested AS on_array[]", 0))
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
			run(referenceExec("CREATE TABLE "+table+"(v "+tc.typ+")", 0))
			run(referenceExec("INSERT INTO "+table+" VALUES"+tc.values, 5))
			run(referenceExec("CREATE VIEW "+table+"_v AS SELECT DISTINCT ON(v) 17 AS payload FROM "+table, 0))
			queryDistinctBoth(run, referenceSelect("SELECT count(*) AS n FROM "+table+"_v", []string{"n"}, []string{"INT8"}, [][]any{{tc.count}}))
		}
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(v) v::text AS mood FROM on_domain_1 ORDER BY v", []string{"mood"}, []string{"TEXT"}, [][]any{{"z"}, {"a"}, {nil}}))
		run(referenceExec("CREATE TABLE enum_flags(id INT)", 0))
		run(referenceExec("INSERT INTO enum_flags VALUES(1),(1),(2),(NULL),(NULL)", 5))
		key := "CASE id WHEN 1 THEN '{z,NULL}'::on_mood_domain[] WHEN 2 THEN '{a,NULL}'::on_mood_domain[] ELSE NULL::on_mood_domain[] END"
		run(referenceExec("CREATE VIEW on_enum_arrays AS SELECT DISTINCT ON("+key+") 17 AS payload FROM enum_flags", 0))
		queryDistinctBoth(run, referenceSelect("SELECT count(*) AS n FROM on_enum_arrays", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		// JSON payload has no equality provider, but ON only needs integer k.
		run(referenceExec("CREATE TABLE json_payload(id INT,k INT,j JSON)", 0))
		run(referenceExec("INSERT INTO json_payload VALUES(1,1,'{\"old\":1}'),(2,1,'{\"new\":2}'),(3,NULL,NULL)", 3))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(k) id,j FROM json_payload ORDER BY k,id DESC", []string{"id", "j"}, []string{"INT4", "JSON"}, [][]any{{int64(2), `{"new":2}`}, {int64(3), nil}}))
	})
}

func TestReferenceQueryDistinctOnTreeKeys(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE tree_source(a INT DEFAULT 1,b INT DEFAULT 2)", 0))
		run(referenceExec("CREATE TABLE repeats(id INT)", 0))
		run(referenceExec("INSERT INTO repeats VALUES(1),(2)", 2))
		run(referenceExec("CREATE VIEW on_tree AS SELECT DISTINCT ON(d.adbin) pg_get_expr(d.adbin,0) AS definition FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid CROSS JOIN repeats WHERE c.relname='tree_source'", 0))
		s := referenceSelect("SELECT definition FROM on_tree ORDER BY definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"1"}, {"2"}})
		queryDistinctBoth(run, s)
		queryDistinctOnHeld(t, p, b, c, s)
		if !b.pg {
			queryDistinctOnNativeReplay(t, p, s)
		}
	})
}
