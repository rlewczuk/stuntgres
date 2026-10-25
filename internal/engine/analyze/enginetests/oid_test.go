package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestOIDDescriptors(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE o(v OID NOT NULL,n OID)")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, q := range []string{"SELECT v,v::oid,v::int8,n,NULL::oid FROM o", "SELECT v,v::oid,v::int8,n,NULL::oid FROM o WHERE v='0'"} {
		d, err := e.Describe(op, enginetest.Statement(t, q))
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range d.Results {
			want := sqltype.Oid
			if i == 2 {
				want = sqltype.Int8
			}
			if c.Usage.Type.ID() != want || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 {
				t.Fatal(i, c)
			}
		}
		if d.Results[0].OriginTable == 0 || d.Results[1].OriginTable != d.Results[0].OriginTable || d.Results[1].OriginAttribute != d.Results[0].OriginAttribute || d.Results[2].OriginTable != 0 || d.Results[2].NullabilityKnown || d.Results[4].OriginTable != 0 {
			t.Fatal(d.Results)
		}
		if !d.Results[0].NullabilityKnown || d.Results[0].Nullable || !d.Results[3].Nullable {
			t.Fatal(d.Results)
		}
	}
	d, err := e.Describe(op, enginetest.Statement(t, "INSERT INTO o VALUES($1,$1)"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Parameters.Types) != 1 || d.Parameters.Types[0].Type.ID() != 26 {
		t.Fatal(d.Parameters)
	}
	d, err = e.Describe(op, enginetest.Statement(t, "SELECT v IS DISTINCT FROM (-1)::int4,(-1)::int4 IS NOT DISTINCT FROM v,v IS DISTINCT FROM $1,$1 IS NOT DISTINCT FROM v FROM o"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Parameters.Types) != 1 || d.Parameters.Types[0].Type.ID() != 26 {
		t.Fatal(d.Parameters)
	}
	for _, c := range d.Results {
		if c.Usage.Type.ID() != 16 || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal(c)
		}
	}
}
