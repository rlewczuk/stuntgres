package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLValuesExpansion(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		owner := aclValuesOwner(b.PG)
		for _, tc := range []struct {
			name, expr string
			rows       [][]any
		}{
			{"duplicates", "ARRAY[makeaclitem(10,10,'SELECT,UPDATE',true),makeaclitem(10,10,'SELECT,UPDATE',true)]", append(aclValuesRows(b.PG, 10, 10, "SELECT,UPDATE", true), aclValuesRows(b.PG, 10, 10, "SELECT,UPDATE", true)...)},
			{"no_rights", fixture.ArrayReferenceQuote("{"+owner+"=/"+owner+"}") + "::aclitem[]", nil},
			{"lower_bound", fixture.ArrayReferenceQuote("[-2:-1]={"+owner+"=r/"+owner+",=w*/"+owner+"}") + "::aclitem[]", append(aclValuesRows(b.PG, 10, 10, "SELECT", false), aclValuesRows(b.PG, 10, 0, "UPDATE", true)...)},
			{"null", "NULL::aclitem[]", nil},
			{"empty_default", "acldefault('c',10)", nil},
			{"empty_cast", "acldefault('c',10)::aclitem[]", nil},
			{"empty_recipe", "pg_catalog.acldefault('c'::\"char\",0::oid)", nil},
			{"empty_concat_rank1_rank0", "array_cat(acldefault('c',0),'{}'::aclitem[])", nil},
			{"empty_concat_rank0_rank1", "array_cat('{}'::aclitem[],acldefault('c',0))", nil},
			{"empty_concat_rank1_rank1", "array_cat(acldefault('c',0),acldefault('c',0))", nil},
			{"zero", "ARRAY[makeaclitem(0,0,'SELECT',true)]", aclValuesRows(b.PG, 0, 0, "SELECT", true)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := aclValuesExplode("SELECT * FROM aclexplode("+tc.expr+")", tc.rows)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		run(fixture.Select("SELECT aclexplode(ARRAY[makeaclitem(0,0,'SELECT',true)]) AS a", []string{"a"}, []string{"RECORD"}, [][]any{{"(0,0,SELECT,t)"}}))
		run(fixture.Select("SELECT g,r,p,o,n FROM aclexplode(ARRAY[makeaclitem(0,0,'SELECT',true)]) WITH ORDINALITY AS x(g,r,p,o,n)", []string{"g", "r", "p", "o", "n"}, []string{"OID", "OID", "TEXT", "BOOL", "INT8"}, [][]any{{aclValuesOID(b.PG, 0), aclValuesOID(b.PG, 0), "SELECT", true, int64(1)}}))
		run(fixture.Select("SELECT x.privilege_type,x.ordinality,s FROM ROWS FROM (aclexplode(acldefault('c',0)),generate_series(1,2)) WITH ORDINALITY AS x(grantor,grantee,privilege_type,is_grantable,s,ordinality) ORDER BY s", []string{"privilege_type", "ordinality", "s"}, []string{"TEXT", "INT8", "INT4"}, [][]any{{nil, int64(1), int64(1)}, {nil, int64(2), int64(2)}}))
		run(fixture.Select("SELECT n,x.privilege_type,x.ordinality FROM generate_series(1,2) AS s(n), LATERAL aclexplode(ARRAY[makeaclitem(n::oid,0,'SELECT',false)]) WITH ORDINALITY AS x ORDER BY n", []string{"n", "privilege_type", "ordinality"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "SELECT", int64(1)}, {int64(2), "SELECT", int64(1)}}))
		run(fixture.Exec("CREATE TABLE acl_values_empty(a aclitem[])", 0))
		run(fixture.Exec("INSERT INTO acl_values_empty VALUES (acldefault('c',0))", 1))
		run(aclValuesExplode("SELECT x.* FROM acl_values_empty e, LATERAL aclexplode(e.a) x", nil))
		run(aclValuesExplode("SELECT * FROM aclexplode((SELECT a FROM acl_values_empty))", nil))
		run(fixture.Select("SELECT acldefault('c',0) @> makeaclitem(0,0,'SELECT',false) AS a", []string{"a"}, []string{"BOOL"}, [][]any{{false}}))
		// Text transport intentionally loses the one-dimensional empty shape.
		var text string
		if err := c.QueryRowContext(p.Ctx, "SELECT a FROM acl_values_empty").Scan(&text); err != nil {
			t.Fatal(err)
		}
		if text != "{}" {
			t.Fatalf("empty ACL text=%q want {}", text)
		}
		d := aclitemDiagnostic("22023", "ACL arrays must be one-dimensional")
		s := fixture.ErrorDiagnostic("SELECT * FROM aclexplode($1::aclitem[])", d, text)
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Prepared(s))
	})
}
