package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapAttrsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"attrs_reader"})
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public,pg_catalog", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers TO off", 0))
		definitions := func() {
			for _, arity := range []int{2, 3} {
				oid := attrsDefinitionOID(t, p, c, attrsDefinitionIdentity(arity, false))
				heapBoth(t, p, b, c, fixture.Select(attrsDefinitionSQL, []string{"definition"}, []string{"TEXT"}, [][]any{{attrsDefinitionText(arity, false)}}, oid))
			}
		}
		definitions()
		attrsStore(t, p, b, c)
		for _, s := range attrsConsumers() {
			heapBoth(t, p, b, c, s)
		}
		attrsSplitRoutes(t, p, b, c, attrsBytes("01020304"), 1, 2, "10000000", attrsArray("01020304", "NULL"))
		heapBoth(t, p, b, c, heapError(attrsSplit5, "XX001", "t_bits string must not be NULL", []byte{}, 1, 2, nil))
		heapBoth(t, p, b, c, attrsRows(attrsSource2, nil, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE attrs_text(v text)", 0))
		q := "SELECT heap_part.tuple_data_split('attrs_text'::regclass::oid,$1,0,1,NULL,true) AS attrs"
		heapBoth(t, p, b, c, attrsValue(q, attrsArray("616263"), attrsBytes("320000000300000000616263")))
		attrsAuthority(t, p, b, c)
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		definitions()
		for _, s := range attrsConsumers() {
			heapBoth(t, p, b, c, s)
		}
		heapBoth(t, p, b, c, attrsValue(q, attrsArray("616263"), attrsBytes("320000000300000000616263")))
	})
}
