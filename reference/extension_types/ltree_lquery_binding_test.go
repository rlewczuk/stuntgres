package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeLqueryBinding(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "lq_binding")
	ltreeLqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { ltreeLqueryRun(t, p, b, c, s) }
		for _, q := range []string{"CREATE SCHEMA lq_binding", "CREATE EXTENSION ltree SCHEMA lq_binding", "SET search_path=public,lq_binding", "CREATE DOMAIN lq_path AS ltree"} { run(fixture.Exec(q, 0)) }
		for _, expression := range []string{
			"'Top.A.Child'::ltree ~ 'Top.*{1,2}.Child'::lquery",
			"'Top.*{1,2}.Child'::lquery ~ 'Top.A.Child'::ltree",
			"'Top.A.Child'::lq_path ~ 'Top.*{1,2}.Child'::lquery",
			"'Top.A.Child'::ltree OPERATOR(lq_binding.~) 'Top.*{1,2}.Child'::lquery",
			"'Top.*{1,2}.Child'::lquery OPERATOR(lq_binding.~) 'Top.A.Child'::ltree",
			"lq_binding.ltq_regex('Top.A.Child'::ltree,'Top.*{1,2}.Child'::lquery)",
			"lq_binding.ltq_rregex('Top.*{1,2}.Child'::lquery,'Top.A.Child'::ltree)",
		} {
			s := ltreeArrayResult(b, "SELECT "+expression+" AS v", true)
			run(s); run(fixture.Prepared(s))
		}
		// Unknown paths cannot choose between the scalar and array ltree overloads.
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT 'Top.A.Child' ~ 'Top.*{1,2}.Child'::lquery AS v", "unknown ~ lquery", "22"},
			{"SELECT 'Top.*{1,2}.Child'::lquery ~ 'Top.A.Child' AS v", "lquery ~ unknown", "35"},
		} {
			want := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: "+tc.signature)
			want.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
			want.Position = tc.position
			for _, prepared := range []bool{false, true} {
				ltreeError(t, p, b, c, tc.q, prepared, true, true, want)
			}
		}
		// Explicit type spelling does not override search-path visibility in errors.
		for _, visibility := range []struct{ path, queryType, pathType string }{
			{"public,lq_binding", "lquery", "ltree"},
			{"public", "lq_binding.lquery", "lq_binding.ltree"},
		} {
			run(fixture.Exec("SET search_path="+visibility.path, 0))
			for _, tc := range []struct{ q, code, message, hint string }{
				{"SELECT 'a' OPERATOR(lq_binding.~) 'a'::lq_binding.lquery", "42725", "operator is not unique: unknown lq_binding.~ "+visibility.queryType, "Could not choose a best candidate operator. You might need to add explicit type casts."},
				{"SELECT 'a'::lq_binding.ltree OPERATOR(lq_binding.~) 'a'::text", "42883", "operator does not exist: "+visibility.pathType+" lq_binding.~ text", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			} {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Hint = tc.hint
				want.Position = lquerySQLPosition(tc.q, "OPERATOR")
				for _, prepared := range []bool{false, true} {
					ltreeError(t, p, b, c, tc.q, prepared, true, true, want)
				}
			}
		}
		run(fixture.Exec("SET search_path=public,lq_binding", 0))
		stmt, err := c.PrepareContext(p.Ctx, ltreeLqueryHeldSQL)
		if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:c, Statement:stmt, SQL:ltreeLqueryHeldSQL}
		ltreeLqueryHeldChecks(t, p, b, held)
		for _, args := range [][]any{{[]byte("Top.A.Child"), []byte("Top.*{1,2}.Child")}, {"Top.A.Child", "Top.*{1,2}.Child"}} {
			run(ltreeLqueryPair(b, ltreeLqueryHeldSQL, true, args...))
			run(fixture.Prepared(ltreeLqueryPair(b, ltreeLqueryHeldSQL, true, args...)))
		}
		byteNilWant := any(true); if b.PG { byteNilWant=nil } // TEXT importer client distinction.
		for _, tc := range []struct{ path, query, want any }{{nil,"*",nil},{"a",nil,nil},{nil,nil,nil},{"","*",true},{[]byte(nil),"*",byteNilWant}} {
			ltreeLqueryRun(t,p,b,held,ltreeLqueryPair(b,ltreeLqueryHeldSQL,tc.want,tc.path,tc.query))
		}
		for _, q := range []string{"CREATE TABLE lq_empty(path ltree)", "CREATE TABLE lq_null(path ltree)", "INSERT INTO lq_null VALUES (NULL)"} {
			count := int64(0); if q=="INSERT INTO lq_null VALUES (NULL)" { count=1 }; run(fixture.Exec(q,count))
		}
		for _, tc := range []struct{ table string; rows [][]any }{{"lq_empty",[][]any{}},{"lq_null",[][]any{{nil,nil}}}} {
			s := fixture.Select("SELECT path ~ '*'::lquery AS forward,'*'::lquery ~ path AS reverse FROM "+tc.table, []string{"forward","reverse"}, []string{"BOOL","BOOL"}, tc.rows)
			s.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},nil)}
			run(s); run(fixture.Prepared(s))
		}
		path, query := []byte("Top.A.Child"), []byte("Top.*{1,2}.Child")
		rows, err := stmt.QueryContext(p.Ctx,path,query)
		if err != nil { t.Fatal(err) }; defer rows.Close()
		metadata := ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},nil)
		for i:=range path {path[i]='x'}; for i:=range query {query[i]='x'}
		if !rows.Next(){t.Fatalf("owned result missing: %v",rows.Err())}
		var forward,reverse bool
		if err:=rows.Scan(&forward,&reverse);err!=nil{t.Fatal(err)}
		if !forward||!reverse{t.Fatal("caller bytes changed result")}
		if rows.Next()||rows.Err()!=nil{t.Fatalf("owned result tail: %v",rows.Err())}; rows.Close()
		ltreeArrayColumnMetadata(t,b,metadata,[]string{"BOOL","BOOL"},nil)
		for _,q:=range []string{"ALTER FUNCTION lq_binding.ltq_regex(lq_binding.ltree,lq_binding.lquery) RENAME TO renamed_match", "ALTER FUNCTION lq_binding.ltq_rregex(lq_binding.lquery,lq_binding.ltree) RENAME TO renamed_reverse"}{run(fixture.Exec(q,0))}
		ltreeLqueryHeldChecks(t,p,b,held)
		run(ltreeArrayResult(b,"SELECT lq_binding.renamed_match('a'::ltree,'a'::lquery) AS v",true))
		run(ltreeArrayResult(b,"SELECT lq_binding.renamed_reverse('a'::lquery,'a'::ltree) AS v",true))
	})
}
