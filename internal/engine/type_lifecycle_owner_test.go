package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestTypeLifecycleOwnerImportACL(t *testing.T) {
	for _, tc := range []struct{ extension, typ string }{{"cube", "cube"}, {"intarray", "query_int"}, {"citext", "citext"}, {"earthdistance", "earth"}} {
		t.Run(tc.extension, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			if tc.extension == "earthdistance" {
				domainUnitPublish(t, e, cubeUnitStage(t, e, op, "cube", ""))
			}
			domainUnitPublish(t, e, cubeUnitStage(t, e, op, tc.extension, ""))
			for _, q := range []string{"CREATE SCHEMA moved", "ALTER TYPE public." + tc.typ + " RENAME TO retained", "ALTER TYPE public.retained SET SCHEMA moved"} {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
			}
			view := e.CatalogRead()
			installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: tc.extension}))
			base := view.MustType(installed.Members[0])
			array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
			roles := e.roles.PublishedRead(&e.catalog.Owner, view)
			target, ok := roles.Lookup("pg_signal_backend")
			if !ok {
				t.Fatal("missing released role fixture")
			}
			metadata, err := extension.OwnerMetadata(view, roles, installed)
			if err != nil {
				t.Fatal(err)
			}
			record, found, err := extension.DecodeOwners(strings.TrimPrefix(metadata, "-- "))
			if err != nil || !found {
				t.Fatal(record, err)
			}
			for i := range record.Members {
				if record.Members[i].Kind == "type" {
					record.Members[i].Owner = target.Name
				}
			}
			candidate := catalog.CloneState(view.State())
			next := e.catalog.Read(candidate)
			changed, err := extension.ImportOwners(context.Background(), next, roles, []extension.OwnerRecord{record})
			if err != nil || !changed {
				t.Fatal("owner import", changed, err)
			}
			for _, old := range []*catalog.Type{base, array} {
				mapped := next.MustType(old.Address())
				if mapped == old || mapped.Owner != target.Ref || mapped.SQLType != old.SQLType || !mapped.ACLSyntheticDefault || !authorization.MatchesDefaultBaseline(mapped.ACL, target.Ref, authorization.DefaultTypes, false) {
					t.Fatal("owner/codec/raw-NULL baseline fidelity lost", mapped)
				}
				if err := e.catalog.Owner.Provider(ids.TypeClassID).Validate(next, mapped); err != nil {
					t.Fatal("strict imported type validation", err)
				}
			}
			for _, address := range installed.Members {
				if old, ok := view.Procedure(address); ok {
					mapped, exists := next.Procedure(address)
					if !exists || mapped.Owner != old.Owner || !authorization.EqualACL(mapped.ACL, old.ACL) {
						t.Fatal("type owner import changed an unrelated procedure owner/ACL")
					}
				}
			}
			// A malformed synthetic ACL must not be laundered into a fresh
			// baseline, have its marker cleared, or reach published state.
			corrupt := catalog.CloneState(view.State())
			bad := *base
			bad.ACL = authorization.ACLValue{Explicit: true}
			corrupt.SetDescriptor(base.Address(), &bad)
			invalid := e.catalog.Read(corrupt)
			_, err = extension.ImportOwners(context.Background(), invalid, roles, []extension.OwnerRecord{record})
			if err == nil {
				err = e.catalog.Owner.Provider(ids.TypeClassID).Validate(invalid, invalid.MustType(base.Address()))
			}
			if err == nil {
				t.Fatal("invalid synthetic ACL accepted after ownership import")
			}
			for _, old := range []*catalog.Type{base, array} {
				if !old.ACLSyntheticDefault || !authorization.MatchesDefaultBaseline(old.ACL, old.Owner, authorization.DefaultTypes, false) {
					t.Fatal("retained source ACL mutated")
				}
			}
			if e.CatalogRead().State() != view.State() {
				t.Fatal("private owner import published catalog state")
			}
		})
	}
}
