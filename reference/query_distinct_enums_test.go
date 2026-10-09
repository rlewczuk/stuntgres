package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryDistinctEnums(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { enumReferenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, q := range []string{
			"CREATE TYPE distinct_mood AS ENUM ('z','a','q')",
			"CREATE DOMAIN distinct_wrapped AS distinct_mood",
			"CREATE TABLE enum_keys(id INT,v distinct_mood,w distinct_wrapped)",
		} {
			run(referenceExec(q, 0))
		}
		check := func(source, column string, want [][]any) {
			s := referenceSelect("SELECT DISTINCT "+column+" AS v FROM "+source+" ORDER BY v", []string{"v"}, []string{""}, want)
			s.checkMetadata = enumReferenceMetadata(b.pg, true)
			queryDistinctBoth(run, s)
		}
		for _, column := range []string{"v", "w"} {
			check("enum_keys", column, [][]any{})
		}
		run(referenceExec("INSERT INTO enum_keys VALUES(1,NULL,NULL),(2,NULL,NULL)", 2))
		for _, column := range []string{"v", "w"} {
			check("enum_keys", column, [][]any{{nil}})
		}
		run(referenceExec("DELETE FROM enum_keys", 2))
		run(referenceExec("INSERT INTO enum_keys VALUES(1,'z','z'),(2,'z','z'),(3,'a','a'),(4,'q','q'),(5,'q','q'),(6,NULL,NULL),(7,NULL,NULL)", 7))
		want := [][]any{{"z"}, {"a"}, {"q"}, {nil}}
		for _, column := range []string{"v", "w"} {
			check("enum_keys", column, want)
		}
		q := "SELECT DISTINCT v FROM enum_keys WHERE v=$1 ORDER BY v"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, arg := range []any{"z", "a", nil, "q", "z"} {
			var expected [][]any
			if arg != nil {
				expected = [][]any{{arg}}
			}
			s := referenceSelect(q, []string{"v"}, []string{""}, expected, arg)
			s.checkMetadata = enumReferenceMetadata(b.pg, true)
			enumReferenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
		}
		domainQuery := "SELECT DISTINCT w AS v FROM enum_keys WHERE id>=$1 ORDER BY v"
		domainStmt, err := c.PrepareContext(p.ctx, domainQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer domainStmt.Close()
		for _, sample := range []struct {
			minimum int
			rows    [][]any
		}{
			{1, want}, {3, [][]any{{"a"}, {"q"}, {nil}}}, {6, [][]any{{nil}}}, {8, [][]any{}}, {1, want},
		} {
			s := referenceSelect(domainQuery, []string{"v"}, []string{""}, sample.rows, sample.minimum)
			s.checkMetadata = enumReferenceMetadata(b.pg, true)
			enumReferenceRunStep(t, p.ctx, referenceHeldStatement{c, domainStmt, domainQuery}, b.recorder, s)
		}
		run(referenceExec("CREATE VIEW enum_dv AS SELECT DISTINCT v FROM enum_keys", 0))
		run(referenceExec("CREATE VIEW enum_domain_dv AS SELECT DISTINCT w AS v FROM enum_keys", 0))
		for _, view := range []string{"enum_dv", "enum_domain_dv"} {
			s := referenceSelect("SELECT v FROM "+view+" ORDER BY v", []string{"v"}, []string{""}, want)
			s.checkMetadata = enumReferenceMetadata(b.pg, true)
			queryDistinctBoth(run, s)
			if !b.pg {
				queryDistinctNativeReplay(t, p, s, "CREATE VIEW invalid_distinct AS SELECT DISTINCT id FROM enum_keys ORDER BY v;\n", "v;")
			}
		}
	})
}

func TestReferenceQueryDistinctEnumArrays(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		// Stored enum arrays remain excluded natively. Existing annotation
		// codecs still admit projected arrays with domain-over-enum elements.
		for _, q := range []string{
			"CREATE TYPE distinct_mood AS ENUM ('z','a','q')",
			"CREATE DOMAIN distinct_wrapped AS distinct_mood",
			"CREATE DOMAIN distinct_whole AS distinct_wrapped[]",
			"CREATE TABLE enum_array_keys(id INT)",
		} {
			run(referenceExec(q, 0))
		}
		storage := "CREATE TABLE enum_array_storage(v distinct_wrapped[],w distinct_whole)"
		if b.pg {
			run(referenceExec(storage, 0))
		} else {
			run(referenceImplicitError(storage, referenceDiagnostic{Code: "0A000", Message: "unsupported column type", Position: queryDistinctPosition(storage, "distinct_wrapped")}))
		}
		projection := func(typ string) string {
			return "CASE id WHEN 1 THEN '{z,NULL}'::" + typ + " WHEN 2 THEN '[0:1]={z,NULL}'::" + typ + " WHEN 3 THEN '{a,NULL}'::" + typ + " WHEN 4 THEN '{}'::" + typ + " ELSE NULL::" + typ + " END"
		}
		types := []string{"_DISTINCT_WRAPPED"}
		if b.pg {
			types = []string{""}
		}
		check := func(typ string, want [][]any) {
			s := arrayReferenceSelect("SELECT DISTINCT "+projection(typ)+" AS v FROM enum_array_keys", []string{"v"}, types, []string{"_ENUM"}, want, b.pg)
			s.bag = true
			queryDistinctBoth(run, s)
		}
		for _, typ := range []string{"distinct_wrapped[]", "distinct_whole"} {
			check(typ, [][]any{})
		}
		run(referenceExec("INSERT INTO enum_array_keys VALUES(NULL),(NULL)", 2))
		for _, typ := range []string{"distinct_wrapped[]", "distinct_whole"} {
			check(typ, [][]any{{nil}})
		}
		run(referenceExec("DELETE FROM enum_array_keys", 2))
		run(referenceExec("INSERT INTO enum_array_keys VALUES(1),(1),(2),(3),(4),(4),(NULL),(NULL)", 8))
		want := [][]any{{"{z,NULL}"}, {"[0:1]={z,NULL}"}, {"{a,NULL}"}, {"{}"}, {nil}}
		for _, typ := range []string{"distinct_wrapped[]", "distinct_whole"} {
			check(typ, want)
		}
		run(referenceExec("CREATE VIEW enum_array_dv AS SELECT DISTINCT "+projection("distinct_whole")+" AS v FROM enum_array_keys", 0))
		s := arrayReferenceSelect("SELECT v FROM enum_array_dv", []string{"v"}, types, []string{"_ENUM"}, want, b.pg)
		s.bag = true
		queryDistinctBoth(run, s)
		// Do not add ordinary array ordering: the held filter only changes the
		// demanded source bag while recursive keys remain unordered.
		q := "SELECT DISTINCT " + projection("distinct_wrapped[]") + " AS v FROM enum_array_keys WHERE $1"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, include := range []bool{true, false, true} {
			expected := want
			if !include {
				expected = [][]any{}
			}
			s := arrayReferenceSelect(q, []string{"v"}, types, []string{"_ENUM"}, expected, b.pg, include)
			s.bag = true
			referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
		}
		if !b.pg {
			queryDistinctNativeReplay(t, p, s, "CREATE VIEW invalid_distinct AS SELECT DISTINCT id FROM enum_array_keys ORDER BY id+1;\n", "id+1;")
		}
	})
}
