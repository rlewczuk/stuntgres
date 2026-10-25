package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func loUnitInstalled(t *testing.T) (*core.Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "lo", ""))
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "lo"}))
	return e, ext, view.MustType(ext.Members[0])
}

func TestLoProviderIdentity(t *testing.T) {
	e, ext, domain := loUnitInstalled(t)
	view := e.CatalogRead()
	base, _ := view.LookupType(sqltype.Oid)
	array := view.MustType(ext.Members[1])
	if ext.Version != "1.2" || len(ext.Members) != 2 || !domain.SQLType.IsDomain() || !domain.SQLType.Concrete() || domain.SQLType.DomainBase() != base.Usage() || domain.SQLType.HasDomainConstraints() || domain.Owner != ext.Owner || domain.Extension != ext.Address() || domain.Array != array.SQLType.ID() || array.SQLType.Element() != domain.SQLType || array.SQLType.Concrete() || array.Owner != domain.Owner || array.Namespace() != domain.Namespace() || array.Extension != ext.Address() {
		t.Fatal("lo concrete domain/lookup companion recipe", ext, domain, array)
	}
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view, ext); err != nil {
		t.Fatal(err)
	}
	for _, a := range ext.Members {
		if err := (stored.TypeProvider{}).Validate(view, view.Get(a)); err != nil {
			t.Fatal(err)
		}
	}
	deps, err := catalog.ExtensionPrerequisites(view, ext)
	if err != nil || len(deps) != 0 {
		t.Fatal("lo must have no prerequisites", deps, err)
	}
	control := extension.StockLo.Control()
	if control.Version != "1.2" || !control.Superuser || !control.Trusted || !control.Relocatable || len(control.Requires) != 0 || control.Comment == nil || *control.Comment != "Large Object maintenance" {
		t.Fatal("lo control", control)
	}
	*control.Comment = "mutated"
	control.Requires = append(control.Requires, "foreign")
	if *extension.StockLo.Control().Comment != "Large Object maintenance" || len(extension.StockLo.Control().Requires) != 0 {
		t.Fatal("shared control storage")
	}
	versions := extension.StockLo.Versions()
	if !reflect.DeepEqual(versions.Bases, []string{"1.2"}) || len(versions.Edges) != 0 || extension.ValidateActions(extension.StockLo) != nil {
		t.Fatal("lo version inventory", versions)
	}
	versions.Bases[0] = "mutated"
	if extension.StockLo.Versions().Bases[0] != "1.2" {
		t.Fatal("shared version storage")
	}
	roles, err := extension.InstalledTypes(view, ext)
	if err != nil || len(roles) != 1 || roles[0].Name != "lo" || roles[0].Type != domain {
		t.Fatal("installed domain role", roles, err)
	}
	for i, member := range []*catalog.Type{domain, array} {
		role, err := extension.MemberTypeRole(view, ext, member)
		if err != nil || role != []string{"lo", "lo[]"}[i] {
			t.Fatal("member role", role, err)
		}
	}
}

func TestLoProviderConflicts(t *testing.T) {
	t.Run("collision", func(t *testing.T) {
		e, ext, _ := loUnitInstalled(t)
		op, done := roleOperation(t, e, "postgres")
		defer done()
		before := e.Catalog.State
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, op.Session)
		copy := *ext
		err = extension.StockLo.Stage(&extensionHost{Engine: e, Op: op, Auth: auth, Stage: stage}, &copy, ext.Owner)
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Message != `type "lo" already exists` || diagnostic.Context != "SQL statement \"CREATE DOMAIN lo AS pg_catalog.oid\"\nextension script file \"lo--1.1.sql\", near line 10" || e.Catalog.State != before {
			t.Fatal("genuine collision must publish nothing", err)
		}
	})
	for _, frontier := range []ids.OID{0, ^ids.OID(0) - 1} {
		t.Run("allocation_"+map[bool]string{true: "empty", false: "companion"}[frontier == 0], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			before := e.Catalog.State
			e.Mu.Lock()
			e.Catalog.InstallOIDFrontierLocked(frontier)
			stage, err := StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "lo"}})
			e.Mu.Unlock()
			if stage != nil {
				e.DiscardCatalogStage(stage)
			}
			if err == nil || stage != nil || e.Catalog.State != before {
				t.Fatal("exhausted installation published a prefix", stage, err)
			}
			if e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "lo"}) != (ids.ObjectAddress{}) || e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "lo"}) != (ids.ObjectAddress{}) {
				t.Fatal("failed installation leaked names")
			}
		})
	}
}

func TestLoManifestAdmission(t *testing.T) {
	e, ext, domain := loUnitInstalled(t)
	view := e.CatalogRead()
	array := view.MustType(ext.Members[1])
	for _, tc := range []struct {
		name   string
		mutate func(*catalog.Extension, *catalog.Type, *catalog.Type)
	}{
		{"version", func(x *catalog.Extension, d, a *catalog.Type) { x.Version = "1.1" }},
		{"missing", func(x *catalog.Extension, d, a *catalog.Type) { x.Members = x.Members[:1] }},
		{"extra", func(x *catalog.Extension, d, a *catalog.Type) { x.Members = append(x.Members, a.Address()) }},
		{"duplicate", func(x *catalog.Extension, d, a *catalog.Type) { x.Members[1] = x.Members[0] }},
		{"class", func(x *catalog.Extension, d, a *catalog.Type) { x.Members[1].ClassID = ids.ProcedureClassID }},
		{"subobject", func(x *catalog.Extension, d, a *catalog.Type) { x.Members[1].SubID = 1 }},
		{"base_generation", func(x *catalog.Extension, d, a *catalog.Type) {
			base := *d.SQLType.DomainBase().Type
			var err error
			d.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), "public.lo", base.Usage())
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"checks", func(x *catalog.Extension, d, a *catalog.Type) {
			var err error
			d.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), "public.lo", d.SQLType.DomainBase(), true)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"not_null", func(x *catalog.Extension, d, a *catalog.Type) {
			d.DomainNotNull = &catalog.DomainNotNullConstraint{OID: 900000, Name: "bad"}
		}},
		{"default", func(x *catalog.Extension, d, a *catalog.Type) { d.DomainDefault = &catalog.ColumnDefault{} }},
		{"dimensions", func(x *catalog.Extension, d, a *catalog.Type) { d.Dimensions = 1 }},
		{"array", func(x *catalog.Extension, d, a *catalog.Type) { d.Array = 0 }},
		{"array_generation", func(x *catalog.Extension, d, a *catalog.Type) {
			other := *d.SQLType
			a.SQLType = sqltype.LookupArray(a.SQLType.ID(), a.Name(), &other)
		}},
		{"owner", func(x *catalog.Extension, d, a *catalog.Type) { a.Owner = authorization.PrincipalRef{} }},
		{"namespace", func(x *catalog.Extension, d, a *catalog.Type) {
			a.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		}},
		{"membership", func(x *catalog.Extension, d, a *catalog.Type) { d.Extension = ids.ObjectAddress{} }},
		{"domain_revision", func(x *catalog.Extension, d, a *catalog.Type) { d.ObjectRevision = 0 }},
		{"array_revision", func(x *catalog.Extension, d, a *catalog.Type) { a.ObjectRevision = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			x, d, a := *ext, *domain, *array
			x.Members = append([]ids.ObjectAddress(nil), ext.Members...)
			tc.mutate(&x, &d, &a)
			state.SetDescriptor(ext.Address(), &x)
			state.SetDescriptor(domain.Address(), &d)
			state.SetDescriptor(array.Address(), &a)
			if err := extension.StockLo.ValidateExtension(e.Catalog.Read(state), &x); err == nil {
				t.Fatal("corrupt lo manifest admitted")
			}
			if e.Catalog.State != view.State() || view.MustType(domain.Address()) != domain || view.MustType(array.Address()) != array {
				t.Fatal("manifest validation mutated source")
			}
		})
	}
	// A loose same-named domain is ordinary user authority, not a stock recipe.
	state := catalog.CloneState(view.State())
	loose := *domain
	loose.Extension = ids.ObjectAddress{}
	state.SetDescriptor(domain.Address(), &loose)
	if err := (stored.TypeProvider{}).Validate(e.Catalog.Read(state), &loose); err != nil {
		t.Fatal("lo spelling claimed unrelated domain", err)
	}
	if err := extension.StockLo.ValidateExtension(view, nil); err == nil {
		t.Fatal("nil manifest admitted")
	}
	foreign := *extension.StockLo
	if err := foreign.ValidateExtension(view, ext); err == nil {
		t.Fatal("foreign provider admitted")
	}
	t.Run("unlisted_member", func(t *testing.T) {
		e.Mu.Lock()
		oid, err := e.Catalog.AllocateOIDLocked()
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		typ, err := sqltype.Domain(sqltype.ID(oid), "foreign_lo", "public.foreign_lo", domain.SQLType.DomainBase())
		if err != nil {
			t.Fatal(err)
		}
		extra := &catalog.Type{SQLType: typ, ObjectNamespace: domain.Namespace(), ObjectRevision: domain.Revision(), Owner: domain.Owner, Extension: ext.Address()}
		state := catalog.CloneState(view.State())
		state.SetDescriptor(extra.Address(), extra)
		state.BindName(catalog.DescriptorNameKey(extra), extra.Address())
		if err := (stored.TypeProvider{}).Validate(e.Catalog.Read(state), extra); err == nil {
			t.Fatal("unlisted extension type admitted")
		}
	})
	t.Run("mutable_spelling", func(t *testing.T) {
		d, a := *domain, *array
		var err error
		d.SQLType, err = domain.SQLType.RebindDomain(domain.SQLType.DomainBase(), "renamed", "public.renamed")
		if err != nil {
			t.Fatal(err)
		}
		a.SQLType = sqltype.LookupArray(array.SQLType.ID(), "other_companion", d.SQLType)
		state := catalog.CloneState(view.State())
		for _, old := range []*catalog.Type{domain, array} {
			state.UnbindName(catalog.DescriptorNameKey(old))
		}
		for _, next := range []*catalog.Type{&d, &a} {
			state.SetDescriptor(next.Address(), next)
			state.BindName(catalog.DescriptorNameKey(next), next.Address())
		}
		next := e.Catalog.Read(state)
		if err := extension.StockLo.ValidateExtension(next, ext); err != nil {
			t.Fatal("manifest identity depended on spelling", err)
		}
		for i, member := range []*catalog.Type{&d, &a} {
			role, err := extension.MemberTypeRole(next, ext, member)
			if err != nil || role != []string{"lo", "lo[]"}[i] {
				t.Fatal("renamed symbolic role", role, err)
			}
		}
	})
}

func TestLoOwnerMetadata(t *testing.T) {
	e, ext, domain := loUnitInstalled(t)
	view := e.CatalogRead()
	metadata, err := extension.OwnerMetadata(view, e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	want := extension.OwnerRecord{Name: "lo", Schema: "public", Version: "1.2", Owner: "postgres", Members: []extension.MemberOwner{
		{Slot: 0, Kind: "type", Schema: "public", Name: "lo", Owner: "postgres"},
		{Slot: 1, Kind: "type", Schema: "public", Name: "lo[]", Owner: "postgres"},
	}}
	if err != nil || !found || !reflect.DeepEqual(record, want) {
		t.Fatal("lo symbolic owner record", record, err)
	}
	encode := func(r extension.OwnerRecord) string {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*extension.OwnerRecord)
	}{
		{"version", func(r *extension.OwnerRecord) { r.Version = "1.1" }},
		{"missing", func(r *extension.OwnerRecord) { r.Members = r.Members[:1] }},
		{"extra", func(r *extension.OwnerRecord) { r.Members = append(r.Members, r.Members[0]) }},
		{"duplicate", func(r *extension.OwnerRecord) { r.Members[1] = r.Members[0] }},
		{"role", func(r *extension.OwnerRecord) { r.Members[1].Name = "_lo" }},
		{"slot_role", func(r *extension.OwnerRecord) { r.Members[0].Slot, r.Members[1].Slot = 1, 0 }},
		{"signature", func(r *extension.OwnerRecord) { r.Members[0].Signature = "26" }},
		{"foreign_kind", func(r *extension.OwnerRecord) { r.Members[0].Kind = "procedure" }},
		{"owner", func(r *extension.OwnerRecord) { r.Members[1].Owner = "pg_signal_backend" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := record
			bad.Members = append([]extension.MemberOwner(nil), record.Members...)
			tc.mutate(&bad)
			if _, found, err := extension.DecodeOwners(encode(bad)); !found || err == nil {
				t.Fatal("corrupt owner record decoded", bad)
			}
			admission := e.Catalog.Read(catalog.CloneState(view.State()))
			if _, err := extension.ImportOwners(context.Background(), admission, e.Roles(), []extension.OwnerRecord{bad}); err == nil {
				t.Fatal("invalid owner record bypassed decoder", bad)
			}
			if e.Catalog.State != view.State() {
				t.Fatal("invalid private owner import published")
			}
		})
	}
	roles := e.Roles()
	target, ok := roles.Lookup("pg_signal_backend")
	if !ok {
		t.Fatal("missing released owner fixture")
	}
	for i := range record.Members {
		record.Members[i].Owner = target.Name
	}
	record.Members[0], record.Members[1] = record.Members[1], record.Members[0]
	record, found, err = extension.DecodeOwners(encode(record))
	if err != nil || !found {
		t.Fatal("valid reverse slot order rejected", err)
	}
	state := catalog.CloneState(view.State())
	next := e.Catalog.Read(state)
	changed, err := extension.ImportOwners(context.Background(), next, roles, []extension.OwnerRecord{record})
	if err != nil || !changed {
		t.Fatal("lo owner import", changed, err)
	}
	if record.Members[0].Slot != 1 {
		t.Fatal("import mutated caller record order")
	}
	for _, address := range ext.Members {
		mapped := next.MustType(address)
		original := view.MustType(address)
		if mapped == original || mapped.SQLType != original.SQLType || mapped.Owner != target.Ref || !mapped.ACLSyntheticDefault || !authorization.MatchesDefaultBaseline(mapped.ACL, target.Ref, authorization.DefaultTypes, false) {
			t.Fatal("lo owner/immutable ACL remap", mapped)
		}
		if err := (stored.TypeProvider{}).Validate(next, mapped); err != nil {
			t.Fatal(err)
		}
	}
	if domain.Owner != ext.Owner || view.MustType(ext.Members[1]).Owner != ext.Owner || e.Catalog.State != view.State() {
		t.Fatal("owner import mutated source")
	}
	t.Run("installer_member_owner_split", func(t *testing.T) {
		state := catalog.CloneState(view.State())
		installed := *ext
		installed.Owner = target.Ref
		state.SetDescriptor(ext.Address(), &installed)
		candidate := e.Catalog.Read(state)
		metadata, err := extension.OwnerMetadata(candidate, e.Roles(), &installed)
		if err != nil {
			t.Fatal(err)
		}
		r, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
		if err != nil || !found || r.Owner != target.Name || r.Members[0].Owner != "postgres" || r.Members[1].Owner != "postgres" {
			t.Fatal("installer/member owner split lost", r, err)
		}
		if _, err := extension.ImportOwners(context.Background(), candidate, e.Roles(), []extension.OwnerRecord{r}); err != nil {
			t.Fatal("split owner record import", err)
		}
	})
	t.Run("companion_collision", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		e.Mu.Lock()
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		arrayOID, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		revision, err := e.Catalog.RevisionLocked()
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		base, _ := e.CatalogRead().LookupType(sqltype.Oid)
		shadow, err := sqltype.Domain(sqltype.ID(oid), "_lo", "public._lo", base.Usage())
		if err != nil {
			t.Fatal(err)
		}
		array := sqltype.LookupArray(sqltype.ID(arrayOID), "__lo", shadow)
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		owner := op.RoleState().CurrentUser
		for _, d := range []*catalog.Type{
			{SQLType: shadow, Array: array.ID(), ObjectNamespace: ns, ObjectRevision: revision, Owner: owner},
			{SQLType: array, ObjectNamespace: ns, ObjectRevision: revision, Owner: owner},
		} {
			if err := stage.Create(d); err != nil {
				t.Fatal(err)
			}
		}
		extensionUnitPublish(t, e, op, stage)
		extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "lo", ""))
		view := e.CatalogRead()
		ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "lo"}))
		companion := view.MustType(ext.Members[1])
		if companion.Name() == "_lo" {
			t.Fatal("companion collision fixture did not displace name")
		}
		metadata, err := extension.OwnerMetadata(view, e.Roles(), ext)
		if err != nil {
			t.Fatal(err)
		}
		r, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
		if err != nil || !found || r.Members[1].Name != "lo[]" {
			t.Fatal("companion role derived from spelling", r, err)
		}
		candidate := e.Catalog.Read(catalog.CloneState(view.State()))
		if _, err := extension.ImportOwners(context.Background(), candidate, e.Roles(), []extension.OwnerRecord{r}); err != nil {
			t.Fatal("actual collided companion import", err)
		}
	})
	t.Run("same_name_role_replacement", func(t *testing.T) {
		target := New()
		admin, done := roleOperation(t, target, "postgres")
		defer done()
		original := roleAttributeCreate(t, target, admin, "lo_import_owner", true, parser.RoleOptions{})
		staged := target.RestoreStage()
		replay, replayDone := roleOperation(t, staged, "postgres")
		defer replayDone()
		extensionUnitPublish(t, staged, replay, cubeUnitStage(t, staged, replay, "lo", ""))
		r := want
		r.Members = append([]extension.MemberOwner(nil), want.Members...)
		for i := range r.Members {
			r.Members[i].Owner = original.Name
		}
		if err := ImportExtensionOwners(staged, context.Background(), replay, []extension.OwnerRecord{r}); err != nil {
			t.Fatal(err)
		}
		roleAttributeApply(t, target, admin, &parser.DropRole{Names: []string{original.Name}})
		replacement := roleAttributeCreate(t, target, admin, original.Name, true, parser.RoleOptions{})
		if replacement.Ref == original.Ref {
			t.Fatal("role identity reused")
		}
		before := target.Catalog.State
		if _, err := target.ReplaceCatalogInOperation(context.Background(), staged, staged.CatalogRead(), admin); err == nil || target.Catalog.State != before {
			t.Fatal("lo member owner recreation escaped replacement fence", err)
		}
	})
}
