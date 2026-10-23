package engine

import (
	"bytes"
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"testing"
)

func TestRangeTypeGeneratedConstructorDumpAuthority(t *testing.T) {
	for _, alter := range []string{"ALTER FUNCTION public.authority_span(integer,integer) OWNER TO pg_monitor", "REVOKE EXECUTE ON FUNCTION public.authority_span(integer,integer) FROM PUBLIC"} {
		t.Run(alter, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE TYPE authority_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=authority_many)", "CREATE TABLE authority_saved(k integer,r authority_span,m authority_many)", "INSERT INTO authority_saved VALUES(7,'[1,3]','{[1,3],[8,9]}')"} {
				roleExecute(t, e, op, q)
			}
			var baseline bytes.Buffer
			if err := e.Dump(context.Background(), &baseline); err != nil {
				t.Fatal("unaltered authority baseline", err)
			}
			roleExecute(t, e, op, alter)
			before := e.catalog.state
			found := false
			view := e.CatalogRead()
			for d := range catalog.DefinitionProceduresNamedAt(view, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "authority_span") {
				if d.RangeConstructor != nil && d.RangeConstructor.Form == catalog.RangeConstructorTwo {
					found = true
					if err := (stored.ProcedureProvider{}).Validate(view, d); err != nil {
						t.Fatal("ordinary altered authority must remain a genuine valid constructor", err)
					}
				}
			}
			if !found {
				t.Fatal("missing genuine two-bound constructor")
			}
			var rejected bytes.Buffer
			if err := e.Dump(context.Background(), &rejected); err == nil || err.Error() != "stuntgres: dump cannot serialize altered generated constructor authority" || rejected.Len() != 0 {
				t.Fatal("authority must reject before output", err, rejected.String())
			}
			if e.catalog.state != before {
				t.Fatal("collection mutated source authority")
			}
			rows := tuples(roleExecute(t, e, op, "SELECT k,r::text,m::text FROM authority_saved"))
			if len(rows) != 1 || rows[0][0] != int64(7) || rows[0][1] != "[1,3]" || rows[0][2] != "{[1,3],[8,9]}" {
				t.Fatal("failed collection changed stored values", rows)
			}
		})
	}
}
