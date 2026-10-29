package enginetests_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestRangeTypeGeneratedConstructorDumpAuthority(t *testing.T) {
	for _, alter := range []string{"ALTER FUNCTION public.authority_span(integer,integer) OWNER TO pg_monitor", "REVOKE EXECUTE ON FUNCTION public.authority_span(integer,integer) FROM PUBLIC"} {
		t.Run(alter, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE TYPE authority_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=authority_many)", "CREATE TABLE authority_saved(k integer,r authority_span,m authority_many)", "INSERT INTO authority_saved VALUES(7,'[1,3]','{[1,3],[8,9]}')"} {
				enginetest.RoleExecute(t, e, op, q)
			}
			var baseline bytes.Buffer
			if err := engine.Dump(context.Background(), e, &baseline); err != nil {
				t.Fatal("unaltered authority baseline", err)
			}
			enginetest.RoleExecute(t, e, op, alter)
			before := e.Catalog.State
			found := false
			view := e.CatalogRead()
			for d := range catalog.DefinitionProceduresNamedAt(view, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "authority_span") {
				if d.RangeConstructor != nil && d.RangeConstructor.Form == catalog.RangeConstructorTwo {
					found = true
					if err := (stored.ProcedureProvider{}).Validate(view, d); err != nil {
						t.Fatal("ordinary altered authority must remain a genuine valid constructor", err)
					}
					forged := *d
					forged.Signature = "25"
					if err := (stored.ProcedureProvider{}).Validate(view, &forged); err == nil {
						t.Fatal("current owner or ACL admitted forged constructor signature")
					}
				}
			}
			if !found {
				t.Fatal("missing genuine two-bound constructor")
			}
			var script bytes.Buffer
			if err := engine.Dump(context.Background(), e, &script); err != nil {
				t.Fatal("current constructor authority Dump", err)
			}
			if e.Catalog.State != before {
				t.Fatal("collection mutated source authority")
			}
			queries := []string{
				"SELECT k,r::text,m::text FROM authority_saved",
				"SELECT public.authority_span(1,3)::text",
				"SELECT proowner::regrole::text,proacl::text FROM pg_proc WHERE oid='public.authority_span(integer,integer)'::regprocedure",
				"SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='public.authority_span(integer,integer)'::regprocedure AND refclassid='pg_type'::regclass AND refobjid='public.authority_span'::regtype AND deptype='i'",
			}
			// Capture before either replacement; never derive the independent
			// engine's expected authority from an already restored engine.
			var want []any
			for _, query := range queries {
				want = append(want, enginetest.Tuples(enginetest.RoleExecute(t, e, op, query)))
			}
			for _, target := range []*core.Engine{e, engine.New()} {
				if err := engine.Restore(context.Background(), target, bytes.NewReader(script.Bytes())); err != nil {
					t.Fatal("current constructor authority Restore", err)
				}
				read, closeRead := enginetest.RoleOperation(t, target, "postgres")
				for i, query := range queries {
					if got := enginetest.Tuples(enginetest.RoleExecute(t, target, read, query)); !reflect.DeepEqual(got, want[i]) {
						t.Fatal("restored authority or behavior changed", query, got, want[i])
					}
				}
				closeRead()
				current := target.CatalogRead()
				for d := range catalog.DefinitionProceduresNamedAt(current, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "authority_span") {
					if d.RangeConstructor != nil && d.RangeConstructor.Form == catalog.RangeConstructorTwo {
						if err := (stored.ProcedureProvider{}).Validate(current, d); err != nil {
							t.Fatal("restored TypeForm/current metadata", err)
						}
					}
				}
			}
		})
	}
}
