package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func cubeUnitStage(t *testing.T, e *Engine, op *session.Operation, name, schema string) *CatalogStage {
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

func cubeUnitInstalled(t *testing.T) (*Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "cube", ""))
	ext := e.catalog.state.MustExtension(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	return e, ext, e.catalog.state.MustType(ext.Members[0])
}

func TestCubeProviderIdentity(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	view := e.CatalogRead()
	if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(view, ext); err != nil {
		t.Fatal(err)
	}
	if ext.Version != "1.5" || len(ext.Members) != 3 || base.Extension != ext.Address() || !base.SQLType.IsCube() || base.Owner != ext.Owner {
		t.Fatal("installed identity", ext, base)
	}
	array, ok := view.LookupArray(base.SQLType)
	if !ok || array.Concrete() || array.Element() != base.SQLType || array.ID() == base.SQLType.ID() {
		t.Fatal("lookup-only reciprocal array")
	}
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())}
	if DependPinned(view, nil, base.Address()) || DependPinned(view, nil, arrayAddress) {
		t.Fatal("installed base or automatic array classified as bootstrap-pinned")
	}
	if !DependPinned(view, nil, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1007}) {
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
		if i == 0 {
			if err := (typeProvider{}).Validate(view, member); err != nil {
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
	if len(members) != 3 || !arrayInternal {
		t.Fatal("dependency ownership", members, arrayInternal)
	}
	for _, address := range ext.Members {
		if !members[address] {
			t.Fatal("missing extension edge", address)
		}
	}
	metadata, err := extension.OwnerMetadata(view, e.roles.read(e.catalog.owner(), view, e.roles.published), ext)
	if err != nil {
		t.Fatal(err)
	}
	// Metadata is a complete SQL comment line; the decoder consumes the
	// lexer-provided comment body, exactly as the Restore pipeline does.
	var records []extension.OwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION cube;\n"+metadata, parser.Options{
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
	if record.Name != "cube" || record.Schema != "public" || record.Version != "1.5" || record.Owner != "postgres" || len(record.Members) != 3 {
		t.Fatal("owner metadata", record)
	}
	for _, member := range record.Members {
		if member.Owner != "postgres" {
			t.Fatal("member owner", member)
		}
	}
}

func TestCubeProviderConflicts(t *testing.T) {
	t.Run("staged_codec_conflict", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		before := e.catalog.state
		e.mu.Lock()
		err := func() error {
			auth, err := e.authorizationLocked(op)
			if err != nil {
				return err
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			stage.authorizeLocked(auth, op.Session)
			stage.extensionNamespace = namespace
			extOID, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			procOID, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			revision, err := e.catalog.revisionLocked()
			if err != nil {
				return err
			}
			owner := auth.State.CurrentUser
			ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: extOID}, ObjectRevision: revision, ObjectName: "cube", Version: "1.5", TargetNamespace: namespace, Owner: owner}
			if err := stage.Create(ext); err != nil {
				return err
			}
			// A genuine preceding staged procedure occupies the input signature.
			// The provider has already staged its scalar and array when it fails.
			conflict := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procOID}, ObjectNamespace: namespace, ObjectRevision: revision, ObjectName: "cube_in", Signature: "2275", Owner: owner}
			if err := stage.Create(conflict); err != nil {
				return err
			}
			return extension.StockCube.Stage(&extensionHost{engine: e, auth: auth, stage: stage}, ext, owner)
		}()
		e.mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.catalog.state != before {
			t.Fatal("staged codec collision published a prefix", err)
		}
	})
	t.Run("published_type_conflict", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		e.mu.Lock()
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		arrayOID, err := e.catalog.allocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		memberID, err := e.catalog.allocateEnumMemberLocked()
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		revision, err := e.catalog.revisionLocked()
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		typ, err := sqltype.EnumWithMembers(sqltype.ID(oid), "cube", "public.cube", []sqltype.EnumMember{{ID: memberID, Label: "occupied", SortOrder: 1}})
		if err != nil {
			t.Fatal(err)
		}
		array, err := sqltype.Array(sqltype.ID(arrayOID), "_cube", typ)
		if err != nil {
			t.Fatal(err)
		}
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		owner := op.RoleState().CurrentUser
		for _, d := range []*catalog.Type{
			{SQLType: typ, ObjectNamespace: namespace, Owner: owner, ObjectRevision: revision, Array: array.ID()},
			{SQLType: array, ObjectNamespace: namespace, Owner: owner, ObjectRevision: revision},
		} {
			if err := stage.Create(d); err != nil {
				t.Fatal(err)
			}
		}
		extensionUnitPublish(t, e, op, stage)
		before := e.catalog.state
		e.mu.Lock()
		_, err = e.stageExtensionLocked(&StatementScope{op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "cube"}})
		e.mu.Unlock()
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "cube" already exists`, Context: extension.CubeTypeContext}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.catalog.state != before {
			t.Fatal("type conflict was not atomic/exact", diagnostic, err)
		}
	})
	t.Run("manifest_validation_and_nonmutation", func(t *testing.T) {
		e, ext, base := cubeUnitInstalled(t)
		original := slices.Clone(ext.Members)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		for _, corrupt := range []func(*catalog.State, *catalog.Extension){
			func(_ *catalog.State, d *catalog.Extension) { d.Members[1] = d.Members[0] },
			func(_ *catalog.State, d *catalog.Extension) { d.Version = "1.2" },
			func(state *catalog.State, d *catalog.Extension) { state.RemoveDescriptor(d.Members[1]) },
			func(state *catalog.State, d *catalog.Extension) {
				proc := *state.MustProcedure(d.Members[1])
				proc.Implementation = extension.StockIntarray
				state.SetDescriptor(proc.Address(), &proc)
			},
			func(state *catalog.State, d *catalog.Extension) {
				proc := *state.MustProcedure(d.Members[1])
				proc.ObjectNamespace = ids.ObjectAddress{}
				state.SetDescriptor(proc.Address(), &proc)
			},
			func(state *catalog.State, d *catalog.Extension) {
				array := *state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
				array.Owner = authorization.PrincipalRef{}
				state.SetDescriptor(array.Address(), &array)
			},
			func(state *catalog.State, d *catalog.Extension) {
				proc := *state.MustProcedure(d.Members[1])
				proc.Result++
				state.SetDescriptor(proc.Address(), &proc)
			},
			func(state *catalog.State, d *catalog.Extension) {
				proc := *state.MustProcedure(d.Members[2])
				proc.Signature = "25"
				state.SetDescriptor(proc.Address(), &proc)
			},
			func(state *catalog.State, _ *catalog.Extension) {
				address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)}
				array := *state.MustType(address)
				array.SQLType = sqltype.LookupArray(array.SQLType.ID(), array.Name(), text)
				state.SetDescriptor(address, &array)
			},
		} {
			candidate := catalog.CloneState(e.catalog.state)
			bad := *ext
			bad.Members = slices.Clone(ext.Members)
			candidate.SetDescriptor(bad.Address(), &bad)
			corrupt(candidate, &bad)
			if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(e.catalog.read(candidate), &bad); err == nil {
				t.Fatal("invalid manifest accepted")
			}
			if !slices.Equal(original, ext.Members) {
				t.Fatal("negative validation mutated original manifest")
			}
		}
		mapped := extension.RemapDescriptor(ext, map[ids.ObjectAddress]ids.ObjectAddress{ext.Members[1]: {ClassID: ids.ProcedureClassID, ObjectID: 65000}}).(*catalog.Extension)
		if mapped.Members[1] == ext.Members[1] || !slices.Equal(original, ext.Members) {
			t.Fatal("remapping aliased source membership")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(e.CatalogRead(), ext); err != nil {
			t.Fatal("original manifest damaged", err)
		}
	})
}

func TestCubeProviderControls(t *testing.T) {
	t.Run("real_import_remapping_and_retained_codec", func(t *testing.T) {
		source, _, base := cubeUnitInstalled(t)
		value, err := base.SQLType.Input(context.Background(), "(3,1),(1,4)", -1)
		if err != nil {
			t.Fatal(err)
		}
		target := New()
		owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
		namespace := intarrayUnitTemporaryNamespace(t, target, owner)
		installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
		extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
		unaccent := target.catalog.state.Get(target.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		closeInstaller()
		if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		ext := target.catalog.state.MustExtension(target.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
		imported := target.catalog.state.MustType(ext.Members[0])
		if imported.SQLType == base.SQLType || imported.SQLType.ID() == base.SQLType.ID() {
			t.Fatal("setup failed to exercise a genuine retained-TEMP OID collision")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(target.CatalogRead(), ext); err != nil {
			t.Fatal(err)
		}
		if target.catalog.state.Get(namespace.Address()) != namespace || target.catalog.state.Get(unaccent.Address()) != unaccent {
			t.Fatal("import changed live TEMP provider authority")
		}
		mapped, err := typedef.ReownDomainDatum(value, imported.SQLType, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: imported.SQLType}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := value.Retag(imported.SQLType); err == nil {
			t.Fatal("ordinary Retag authorized cross-OID import")
		}
		if text, err := imported.SQLType.Output(context.Background(), mapped); err != nil || text != "(3, 1),(1, 4)" {
			t.Fatal("imported codec", text, err)
		}
		if err := source.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		if text, err := base.SQLType.Output(context.Background(), value); err != nil || text != "(3, 1),(1, 4)" {
			t.Fatal("retained codec", text, err)
		}
		closeOwner()
		assertTemporaryNamespacePair(t, target, namespace.Temporary, namespace.Address())
		if target.catalog.state.Get(namespace.Address()) != namespace || target.catalog.state.Get(unaccent.Address()) != nil {
			t.Fatal("original TEMP provider failed retirement after import")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(target.CatalogRead(), ext); err != nil {
			t.Fatal("persistent imported provider damaged by TEMP retirement", err)
		}
	})
}
