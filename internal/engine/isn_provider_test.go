package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func isnUnitStage(t *testing.T, e *Engine, op *session.Operation, name, schema string) *CatalogStage {
	t.Helper()
	creation := &parser.CreateExtension{Name: parser.Identifier{Text: name}}
	if schema != "" {
		creation.Options = []parser.ExtensionOption{{Name: "schema", Value: parser.Identifier{Text: schema}}}
	}
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(&StatementScope{op: op}, creation)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage
}

func isnUnitInstalled(t *testing.T) (*Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, isnUnitStage(t, e, op, "isn", ""))
	ext := e.catalog.State.MustExtension(e.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
	return e, ext, e.catalog.State.MustType(ext.Members[0])
}

func TestIsnProviderIdentity(t *testing.T) {
	e, ext, base := isnUnitInstalled(t)
	view := e.CatalogRead()
	if roles, err := extension.InstalledTypes(view, ext); err != nil || len(roles) != 2 || roles[0].Name != "ean13" || roles[0].Type != base || roles[1].Name != "isbn" || !roles[1].Type.SQLType.IsIsbn() {
		t.Fatal("installed ownership roles", roles, err)
	}
	if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(view, ext); err != nil {
		t.Fatal(err)
	}
	if ext.Version != "1.3" || len(ext.Members) != 6 || base.Extension != ext.Address() || !base.SQLType.IsEan13() || base.Owner != ext.Owner {
		t.Fatal("installed identity", ext, base)
	}
	array, ok := view.LookupArray(base.SQLType)
	if !ok || array.Concrete() || array.Element() != base.SQLType || array.ID() == base.SQLType.ID() {
		t.Fatal("lookup-only reciprocal array")
	}
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())}
	if stored.DependPinned(view, nil, base.Address()) || stored.DependPinned(view, nil, arrayAddress) {
		t.Fatal("installed base or automatic array classified as bootstrap-pinned")
	}
	if !stored.DependPinned(view, nil, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1007}) {
		t.Fatal("builtin int4 array lost bootstrap pinning")
	}
	for i, address := range ext.Members {
		member := view.Get(address)
		if member == nil || member.Namespace() != ext.TargetNamespace || catalog.ExtensionMembership(member) != ext.Address() {
			t.Fatal("membership", address)
		}
		owner, _, _ := access.DescriptorACL(member)
		if owner != ext.Owner {
			t.Fatal("owner", address)
		}
		if i == 0 || i == 3 {
			if err := (stored.TypeProvider{}).Validate(view, member); err != nil {
				t.Fatal(err)
			}
		} else if err := (extension.CatalogProvider{ids.ProcedureClassID}).Validate(view, member); err != nil {
			t.Fatal(err)
		}
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, engineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	members := map[ids.ObjectAddress]bool{}
	arrayInternal := false
	for _, edge := range edges {
		if edge.Referenced == ext.Address() && edge.DepType == 'e' {
			members[edge.Dependent] = true
		}
		if edge.Dependent == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())}) && edge.Referenced == base.Address() && edge.DepType == 'i' {
			arrayInternal = true
		}
	}
	if len(members) != 6 || !arrayInternal {
		t.Fatal("dependency ownership", members, arrayInternal)
	}
	for _, address := range ext.Members {
		if !members[address] {
			t.Fatal("missing extension edge", address)
		}
	}
	metadata, err := extension.OwnerMetadata(view, e.roles.PublishedRead(&e.catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	// Metadata is a complete SQL comment line; the decoder consumes the
	// lexer-provided comment body, exactly as the Restore pipeline does.
	var records []extension.OwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION isn;\n"+metadata, parser.Options{
		Context: context.Background(), StandardConformingStrings: true,
		LineComment: func(comment string, _ parser.Position) error {
			record, found, err := extension.DecodeOwners(comment)
			if err == nil && found {
				records = append(records, record)
			}
			return err
		},
	})
	if err != nil || len(records) != 1 {
		t.Fatal("owner metadata comments", records, err)
	}
	record := records[0]
	if record.Name != "isn" || record.Schema != "public" || record.Version != "1.3" || record.Owner != "postgres" || len(record.Members) != 6 {
		t.Fatal("owner metadata", record)
	}
	for _, member := range record.Members {
		if member.Owner != "postgres" {
			t.Fatal("member owner", member)
		}
	}
	if record.Members[0].Kind != "type" || record.Members[0].Name != "ean13" || record.Members[0].Signature != "" {
		t.Fatal("canonical type ownership role", record.Members[0])
	}
	if record.Members[2].Kind != "procedure" || record.Members[2].Name != "ean13_out" || record.Members[2].Signature != "ean13" {
		t.Fatal("symbolic output ownership signature", record.Members[2])
	}
	candidate := catalog.CloneState(e.catalog.State)
	changed, err := extension.ImportOwners(context.Background(), e.catalog.Read(candidate), e.Roles(), records)
	if err != nil || changed {
		t.Fatal("unchanged owner metadata import", changed, err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA isn_identity_dst", "ALTER TYPE public.ean13 RENAME TO moved", "ALTER TYPE public.moved SET SCHEMA isn_identity_dst"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	current := e.CatalogRead()
	moved := current.MustType(base.Address())
	if roles, err := extension.InstalledTypes(current, ext); err != nil || len(roles) != 2 || roles[0].Name != "ean13" || roles[0].Type != moved {
		t.Fatal("renamed ownership roles", roles, err)
	}
	if role, err := extension.MemberTypeRole(current, ext, moved); err != nil || role != "ean13" {
		t.Fatal("renamed canonical member role", role, err)
	}
	movedMetadata, err := extension.OwnerMetadata(current, e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	movedRecord, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(movedMetadata, "--")))
	if err != nil || !found || movedRecord.Members[0].Name != "ean13" || movedRecord.Members[0].Schema != "isn_identity_dst" {
		t.Fatal("renamed canonical ownership identity", movedRecord, found, err)
	}
	candidate = catalog.CloneState(current.State())
	if changed, err := extension.ImportOwners(context.Background(), e.catalog.Read(candidate), e.Roles(), []extension.OwnerRecord{movedRecord}); err != nil || changed {
		t.Fatal("renamed unchanged owner import", changed, err)
	}
	for _, address := range ext.Members[1:] {
		if address.ClassID != ids.ProcedureClassID { continue }
		if current.MustProcedure(address) != view.MustProcedure(address) {
			t.Fatal("base identity edit replaced independent I/O authority")
		}
	}
}

func TestIsnProviderImport(t *testing.T) {
	for _, family := range []string{"ean13", "isbn"} {
	t.Run(family, func(t *testing.T) {
		source, installed, _ := isnUnitInstalled(t)
		slot, input, output, constructor := 0, "1234567890120!", "123-456789012-8!", sqltype.Ean13
		if family == "isbn" { slot, input, output, constructor = 3, "9780123456780!", "0-12-345678-9!", sqltype.Isbn }
		base := source.CatalogRead().MustType(installed.Members[slot])
		op, done := roleOperation(t, source, "postgres")
		defer done()
		for _, q := range []string{"CREATE SCHEMA isn_import_dst", "ALTER TYPE public."+family+" RENAME TO moved", "ALTER TYPE public.moved SET SCHEMA isn_import_dst"} {
			domainUnitPublish(t, source, domainUnitStage(t, source, op, q))
		}
		base = source.CatalogRead().MustType(base.Address())
		value, err := base.SQLType.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		target := New()
		owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
		namespace := intarrayUnitTemporaryNamespace(t, target, owner)
		installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
		extensionUnitPublish(t, target, installer, isnUnitStage(t, target, installer, "unaccent", namespace.Name()))
		unaccent := target.catalog.State.Get(target.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		closeInstaller()
		if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		ext := target.catalog.State.MustExtension(target.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
		imported := target.catalog.State.MustType(ext.Members[slot])
		if imported.SQLType == base.SQLType || imported.SQLType.ID() == base.SQLType.ID() {
			t.Fatal("setup failed to exercise a genuine retained-TEMP OID collision")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(target.CatalogRead(), ext); err != nil {
			t.Fatal(err)
		}
		if roles, err := extension.InstalledTypes(target.CatalogRead(), ext); err != nil || len(roles) != 2 || roles[slot/3].Name != family || roles[slot/3].Type != imported || imported.Name() != "moved" || imported.SQLType.SQLName() != "isn_import_dst.moved" {
			t.Fatal("imported mutable identity lost canonical role", roles, err)
		}
		if target.catalog.State.Get(namespace.Address()) != namespace || target.catalog.State.Get(unaccent.Address()) != unaccent {
			t.Fatal("import changed live TEMP provider authority")
		}
		mapped, err := typedef.ReownDomainDatum(value, imported.SQLType, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: imported.SQLType}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, datum := range []sqltype.Value{value, sqltype.Null(base.SQLType)} {
			mapping := map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: imported.SQLType}
			mapped, err := typedef.ReownDomainDatum(datum, imported.SQLType, nil, mapping, nil)
			if err != nil || mapped.Type() != imported.SQLType || mapped.IsNull() != datum.IsNull() {
				t.Fatal("authorized scalar import", mapped, err)
			}
			foreign, err := constructor(imported.SQLType.ID(), family, "public."+family)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := typedef.ReownDomainDatum(datum, foreign, nil, mapping, nil); err == nil {
				t.Fatal("import authorized a foreign destination pointer")
			}
			if _, err := typedef.ReownDomainDatum(datum, imported.SQLType, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{foreign: imported.SQLType}, nil); err == nil {
				t.Fatal("import authorized a foreign source pointer")
			}
		}
		if _, err := value.Retag(imported.SQLType); err == nil {
			t.Fatal("ordinary Retag authorized cross-OID import")
		}
		if text, err := imported.SQLType.Output(context.Background(), mapped); err != nil || text != output {
			t.Fatal("imported codec", text, err)
		}
		if err := source.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		if text, err := base.SQLType.Output(context.Background(), value); err != nil || text != output {
			t.Fatal("retained codec", text, err)
		}
		closeOwner()
		assertTemporaryNamespacePair(t, target, namespace.Temporary, namespace.Address())
		if target.catalog.State.Get(namespace.Address()) != namespace || target.catalog.State.Get(unaccent.Address()) != nil {
			t.Fatal("original TEMP provider failed retirement after import")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(target.CatalogRead(), ext); err != nil {
			t.Fatal("persistent imported provider damaged by TEMP retirement", err)
		}
	})
	}
}
