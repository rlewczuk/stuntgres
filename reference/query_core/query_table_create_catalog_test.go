package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateCatalogTypes(t *testing.T) {
	p := fixture.NewScenario(t)
	var dump []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE TABLE qc_anchor(i INT)", 0))
			// Arrays enter through the existing declared-domain-array source owner;
			// this does not add array constructors, array casts or subscripting.
			run(fixture.Exec("CREATE DOMAIN qc_array_domain AS regclass", 0))
			run(fixture.Exec("CREATE TABLE qc_array_input(a qc_array_domain[])", 0))
			run(fixture.Exec("INSERT INTO qc_array_input VALUES('[0:1]={qc_anchor,NULL}')", 1))
			run(fixture.Exec("CREATE TABLE qc_catalog_source(i INT)", 0))
			producer := `SELECT c.oid AS o,c.relname AS n,c.relkind AS k,'qc_anchor'::regclass AS r,s.a FROM pg_class c CROSS JOIN qc_array_input s WHERE c.relname='qc_catalog_source'`
			run(fixture.Prepared(fixture.Exec("CREATE TABLE qc_catalog_copy AS "+producer, 1)))
			run(fixture.Exec("CREATE TABLE qc_catalog_empty AS "+producer+" WITH NO DATA", 0))
			check := func() {
				run(fixture.Select("SELECT n,k,r,o>0 AS positive,r='qc_anchor'::regclass AS same FROM qc_catalog_copy", []string{"n", "k", "r", "positive", "same"}, []string{"NAME", "CHAR", "REGCLASS", "BOOL", "BOOL"}, [][]any{{"qc_catalog_source", "r", "qc_anchor", true, true}}))
				run(fixture.Prepared(fixture.Select("SELECT o,n,k,r FROM qc_catalog_empty", []string{"o", "n", "k", "r"}, []string{"OID", "NAME", "CHAR", "REGCLASS"}, nil)))
				run(fixture.Select("SELECT a::text AS a FROM qc_catalog_copy", []string{"a"}, []string{"TEXT"}, [][]any{{"[0:1]={qc_anchor,NULL}"}}))
				arrayType := "_QC_ARRAY_DOMAIN"
				if b.PG {
					arrayType = ""
				}
				run(fixture.ArrayReferenceSelect("SELECT a FROM qc_catalog_empty", []string{"a"}, []string{arrayType}, []string{"_REGCLASS"}, nil, b.PG))
			}
			check()
			run(fixture.Exec("DROP TABLE qc_catalog_source", 0))
			run(fixture.Exec("DROP TABLE qc_array_input", 0))
			check()
			if !b.PG {
				// Explicit native controls: no PostgreSQL Dump/Reset/Restore API is invented.
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				dump = bytes.Clone(saved.Bytes())
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.QueryTableCreateAbsent(t, run, "qc_catalog_copy")
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				check()
			} else {
				if dump == nil {
					t.Fatal("actual native-emitted dump is required")
				}
				run(fixture.Exec("DROP TABLE qc_catalog_copy,qc_catalog_empty,qc_anchor", 0))
				run(fixture.Exec("DROP DOMAIN qc_array_domain", 0))
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PostgreSQL rejected native-emitted catalog type dump", err)
				}
				check()
			}
		})
	}
}
