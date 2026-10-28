package core_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestObjectACLTypeProvenance(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, definition := range []string{
		"CREATE DOMAIN public.acl_provenance_domain AS integer",
		"CREATE TYPE public.acl_provenance_enum AS ENUM ('one')",
		"CREATE TYPE public.acl_provenance_composite AS (a integer)",
	} {
		enginetest.RoleExecute(t, e, op, definition)
	}
	view := e.CatalogRead()
	for _, name := range []string{"acl_provenance_domain", "acl_provenance_enum", "acl_provenance_composite"} {
		d := domainUnitType(t, view, name)
		for _, address := range []ids.ObjectAddress{d.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)}} {
			typ := view.MustType(address)
			acl, err := access.RawObjectACL(view, address)
			if !typ.ACLSyntheticDefault || !typedef.DefaultDomainACL(typ) || err != nil || acl.Explicit {
				t.Fatal("constructor lost raw absence", name, address, acl, err)
			}
		}
	}
	// Ordinary relation row and associated array constructors also materialize
	// effective USAGE without materializing a raw ACL.
	stage := e.BeginCatalogStage(view)
	e.Mu.Lock()
	integer, _ := view.LookupType(sqltype.Int4)
	relation, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "acl_provenance_row", op.RoleState().CurrentUser, []catalog.Column{{Name: "a", Usage: integer.Usage()}}, nil)
	if err == nil {
		relation.TableMethod = 2
		err = e.StageRowTypeLocked(stage, relation)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(relation); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view = e.CatalogRead()
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
	for _, address := range []ids.ObjectAddress{row.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}} {
		if d := view.MustType(address); !d.ACLSyntheticDefault {
			t.Fatal("relation row/array has explicit raw ACL", address)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.ACLItemArray} {
		d := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		if d.ACLSyntheticDefault || d.ACL.Explicit {
			t.Fatal("builtin raw absence does not need synthetic baseline", id)
		}
	}
	t.Run("extension_constructor", func(t *testing.T) {
		extensionEngine, _, base := intarrayUnitInstalled(t)
		catalog := extensionEngine.CatalogRead()
		for _, address := range []ids.ObjectAddress{base.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)}} {
			d := catalog.MustType(address)
			if !d.ACLSyntheticDefault || !typedef.DefaultDomainACL(d) {
				t.Fatal("extension baseline provenance", address)
			}
		}
	})
	t.Run("creation_default_normalization", func(t *testing.T) {
		f := newDefaultUnitFixture(t)
		revoke := defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"PUBLIC"})
		revoke.Revoke = true
		if err := f.apply(revoke); err != nil {
			t.Fatal(err)
		}
		stage := f.e.BeginCatalogStage(f.e.CatalogRead())
		acl, synthetic := stage.CreationTypeACL(f.owner, f.schema)
		if synthetic || !acl.Explicit || len(acl.Entries) != 1 || acl.Entries[0].Grantee.Role != f.owner || acl.Entries[0].Privileges != authorization.UsagePrivilege {
			t.Fatal("custom creation default lost raw ACL", acl, synthetic)
		}
		f.e.DiscardCatalogStage(stage)
		revoke.Revoke = false
		if err := f.apply(revoke); err != nil {
			t.Fatal(err)
		}
		stage = f.e.BeginCatalogStage(f.e.CatalogRead())
		defer f.e.DiscardCatalogStage(stage)
		acl, synthetic = stage.CreationTypeACL(f.owner, f.schema)
		if !synthetic || !authorization.MatchesDefaultBaseline(acl, f.owner, authorization.DefaultTypes, false) {
			t.Fatal("baseline-equal creation was not normalized", acl, synthetic)
		}
	})
	d := domainUnitType(t, view, "acl_provenance_domain")
	ownerStage := e.BeginCatalogStage(view)
	target := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	if err := role.StageOwnership(ownerStage, d, target); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, ownerStage)
	view = e.CatalogRead()
	d = domainUnitType(t, view, "acl_provenance_domain")
	if d.Owner != target || !d.ACLSyntheticDefault || !typedef.DefaultDomainACL(d) {
		t.Fatal("owner remap lost raw absence", d)
	}
	other := engine.New()
	remapped, _, err := core.EngineRemapPreparedCatalog(other, catalog.CloneState(view.State()), e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), other.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), nil)
	if err != nil {
		t.Fatal("principal remap", err)
	}
	imported := remapped.MustType(d.Address())
	if !imported.ACLSyntheticDefault || !typedef.DefaultDomainACL(imported) || imported.Owner == d.Owner {
		t.Fatal("foreign principal remap lost provenance", imported)
	}
	// Equal effective payloads need not have equal raw provenance. Publication
	// must accept an explicit baseline but serialization must not discard it.
	explicit := *d
	explicit.ACLSyntheticDefault = false
	explicit.ObjectRevision++
	change := e.BeginCatalogStage(view)
	if err := change.Replace(&explicit); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, change)
	acl, err := access.RawObjectACL(e.CatalogRead(), explicit.Address())
	if err != nil || !reflect.DeepEqual(acl, explicit.ACL) || !acl.Explicit || typedef.DefaultDomainACL(&explicit) {
		t.Fatal("explicit baseline collapsed to NULL", acl, err)
	}
	var output bytes.Buffer
	if err := engine.Dump(context.Background(), e, &output); err != nil || output.Len() == 0 {
		t.Fatal("Dump failed to preserve explicit-baseline provenance", output.String(), err)
	}
	invalid := explicit
	invalid.ACLSyntheticDefault = true
	invalid.ACL = authorization.ACLValue{Explicit: true}
	if err := (stored.TypeProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
		t.Fatal("accepted synthetic provenance for a nonbaseline ACL")
	}
}

// SetObjectACL is a private publication seam, distinct from SQL GRANT. The SQL
// definitions below only construct real descriptors for that seam.
func TestObjectACLTypeSetterProvenance(t *testing.T) {
	for _, kind := range []struct {
		name       string
		definition string
	}{
		{"domain", "CREATE DOMAIN public.acl_setter_type AS integer"},
		{"enum", "CREATE TYPE public.acl_setter_type AS ENUM ('one')"},
		{"composite", "CREATE TYPE public.acl_setter_type AS (a integer)"},
	} {
		t.Run(kind.name, func(t *testing.T) {
			for _, replacement := range []string{"baseline", "empty", "owner_only"} {
				t.Run(replacement, func(t *testing.T) {
					e := engine.New()
					op, done := enginetest.RoleOperation(t, e, "postgres")
					defer done()
					enginetest.RoleExecute(t, e, op, kind.definition)
					oldView := e.CatalogRead()
					old := domainUnitType(t, oldView, "acl_setter_type")
					oldACL := authorization.ACLValue{
						Explicit: old.ACL.Explicit,
						Entries:  append([]authorization.ACLEntry(nil), old.ACL.Entries...),
					}
					if !old.ACLSyntheticDefault || !typedef.DefaultDomainACL(old) {
						t.Fatal("fixture lacks constructor-synthetic baseline", old)
					}
					if acl, err := access.RawObjectACL(oldView, old.Address()); err != nil || acl.Explicit || len(acl.Entries) != 0 {
						t.Fatal("fixture raw ACL is not absent", acl, err)
					}
					if err := (stored.TypeProvider{}).Validate(oldView, old); err != nil {
						t.Fatal("invalid constructor descriptor", err)
					}

					// Specify the payload independently of the constructor's ACL.
					want := authorization.ACLValue{Explicit: true}
					if replacement != "empty" {
						want.Entries = []authorization.ACLEntry{
							objectACLUnitEntry(old.Owner, old.Owner, authorization.UsagePrivilege, 0),
						}
					}
					if replacement == "baseline" {
						want.Entries = append(want.Entries, authorization.ACLEntry{
							Grantor: old.Owner, Grantee: authorization.ACLGrantee{Public: true},
							Privileges: authorization.UsagePrivilege,
						})
					}
					ref := e.Catalog.Domain.Ref(oldView.Epoch(), old.Address())
					if err := enginetest.SetObjectACL(e, context.Background(), ref, want); err != nil {
						t.Fatal("SetObjectACL", err)
					}
					currentView := e.CatalogRead()
					current := domainUnitType(t, currentView, "acl_setter_type")
					if current == old || currentView.State() == oldView.State() {
						t.Error("setter did not publish a new immutable catalog descriptor")
					}
					if !reflect.DeepEqual(current.ACL, want) {
						t.Error("setter published the wrong ACL", current.ACL, want)
					}
					if current.ACLSyntheticDefault {
						t.Error("genuine replacement retained constructor provenance")
					}
					if acl, err := access.RawObjectACL(currentView, current.Address()); err != nil || !reflect.DeepEqual(acl, want) {
						t.Error("genuine explicit ACL is not raw-visible", acl, want, err)
					}
					if err := (stored.TypeProvider{}).Validate(currentView, current); err != nil {
						t.Error("setter published an invalid type descriptor", err)
					}
					if typedef.DefaultDomainACL(current) {
						t.Error("genuine explicit ACL is considered regenerable")
					}

					sink := new(concurrentUnitSink)
					if err := engine.Dump(context.Background(), e, sink); err != nil || sink.calls == 0 || sink.bytes == 0 {
						t.Error("Dump failed to serialize explicit ACL", err, sink)
					}
					release := make(chan struct{})
					close(release)
					writer := &commentGateWriter{entered: make(chan struct{}), release: release}
					if err := engine.Dump(context.Background(), e, writer); err != nil || !writer.first || writer.Len() == 0 {
						t.Error("Dump StringWriter failed", err, writer.first, writer.String())
					}

					// Retained readers must still see the original constructor
					// state after both publication and attempted serialization.
					retained := domainUnitType(t, oldView, "acl_setter_type")
					if retained != old || !retained.ACLSyntheticDefault || !reflect.DeepEqual(retained.ACL, oldACL) || !typedef.DefaultDomainACL(retained) {
						t.Error("setter or Dump mutated the old catalog descriptor", retained)
					}
					if acl, err := access.RawObjectACL(oldView, old.Address()); err != nil || acl.Explicit || len(acl.Entries) != 0 {
						t.Error("old snapshot lost raw absence", acl, err)
					}
					if err := (stored.TypeProvider{}).Validate(oldView, retained); err != nil {
						t.Error("old catalog descriptor became invalid", err)
					}
					if err := engine.Restore(context.Background(), e, bytes.NewReader(writer.Bytes())); err != nil {
						t.Fatal(err)
					}
					replayed := domainUnitType(t, e.CatalogRead(), "acl_setter_type")
					if replayed.ACLSyntheticDefault || replayed.ACL.Explicit != want.Explicit || !slices.Equal(replayed.ACL.Entries, want.Entries) {
						t.Fatal("setter raw provenance lost in replay", replayed.ACL, want)
					}
				})
			}
		})
	}
}

func intarrayUnitInstalled(t *testing.T) (*core.Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION intarray")
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"}))
	return e, ext, e.Catalog.State.MustType(ext.Members[0])
}

type commentGateWriter struct {
	bytes.Buffer
	entered, release chan struct{}
	first            bool
}

func (w *commentGateWriter) Write(p []byte) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return w.Buffer.Write(p)
}

// Dump uses io.WriteString, so intercept the promoted Buffer fast path too.
func (w *commentGateWriter) WriteString(s string) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return w.Buffer.WriteString(s)
}
