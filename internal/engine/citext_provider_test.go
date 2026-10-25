package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func citextUnitStage(t *testing.T, e *core.Engine, op *session.Operation, name, schema string) *core.CatalogStage {
	t.Helper()
	creation := &parser.CreateExtension{Name: parser.Identifier{Text: name}}
	if schema != "" {
		creation.Options = []parser.ExtensionOption{{Name: "schema", Value: parser.Identifier{Text: schema}}}
	}
	e.Mu.Lock()
	stage, err := StageExtensionLocked(e, &core.StatementScope{Op: op}, creation)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage
}

func citextUnitInstalled(t *testing.T) (*core.Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, citextUnitStage(t, e, op, "citext", ""))
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "citext"}))
	return e, ext, e.Catalog.State.MustType(ext.Members[0])
}

func TestCitextProviderIdentity(t *testing.T) {
	e, ext, base := citextUnitInstalled(t)
	view := e.CatalogRead()
	if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(view, ext); err != nil {
		t.Fatal(err)
	}
	if ext.Version != "1.8" || len(ext.Members) != extension.CitextMemberCount || base.Extension != ext.Address() || !base.SQLType.IsCitext() || base.Owner != ext.Owner {
		t.Fatal("installed identity", ext, base)
	}
	facts, err := pgcatalog.TypeFacts(context.Background(), view, base, sqltype.OutputEnvironment{})
	if err != nil || facts.Input != uint32(ext.Members[1].ObjectID) || facts.Output != uint32(ext.Members[2].ObjectID) {
		t.Fatal("installed type IO projection", facts, err)
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
		if i == 0 {
			if err := (stored.TypeProvider{}).Validate(view, member); err != nil {
				t.Fatal(err)
			}
		} else if err := (extension.CatalogProvider{address.ClassID}).Validate(view, member); err != nil {
			t.Fatal(err)
		}
	}
	for i, signature := range []catalog.ProcedureSignature{"1042", "16", "869"} {
		procedure, ok := view.Procedure(ext.Members[extension.CitextConversionProcedureStart+i])
		if !ok || procedure.Name() != "citext" || procedure.Signature != signature || procedure.Result != ids.OID(base.SQLType.ID()) || !procedure.Strict || procedure.Volatility != expr.Immutable {
			t.Fatal("released citext conversion declaration", procedure)
		}
		// REL_18_6 citext--1.4.sql uses internal symbols, not C-library
		// module names, with invoker security and unnamed arguments.
		display := catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: [3]string{"rtrim1", "booltext", "network_show"}[i]}
		if procedure.Display != display {
			t.Fatal("released citext conversion display declaration", procedure.Display)
		}
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, core.EngineCatalogRuntime{})
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
	if len(members) != extension.CitextMemberCount || !arrayInternal {
		t.Fatal("dependency ownership", members, arrayInternal)
	}
	for _, address := range ext.Members {
		if !members[address] {
			t.Fatal("missing extension edge", address)
		}
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	// Metadata is a complete SQL comment line; the decoder consumes the
	// lexer-provided comment body, exactly as the Restore pipeline does.
	var records []extension.OwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION citext;\n"+metadata, parser.Options{
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
	if record.Name != "citext" || record.Schema != "public" || record.Version != "1.8" || record.Owner != "postgres" || len(record.Members) != extension.CitextMemberCount {
		t.Fatal("owner metadata", record)
	}
	for _, member := range record.Members {
		if member.Owner != "postgres" {
			t.Fatal("member owner", member)
		}
	}
}

func TestCitextProviderConflicts(t *testing.T) {
	t.Run("staged_codec_conflict", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		before := e.Catalog.State
		e.Mu.Lock()
		err := func() error {
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				return err
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			stage.AuthorizeLocked(auth, op.Session)
			extOID, err := e.Catalog.AllocateOIDLocked()
			if err != nil {
				return err
			}
			procOID, err := e.Catalog.AllocateOIDLocked()
			if err != nil {
				return err
			}
			revision, err := e.Catalog.RevisionLocked()
			if err != nil {
				return err
			}
			owner := auth.State.CurrentUser
			ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: extOID}, ObjectRevision: revision, ObjectName: "citext", Version: "1.8", TargetNamespace: namespace, Owner: owner}
			if err := stage.Create(ext); err != nil {
				return err
			}
			// A genuine preceding staged procedure occupies the input signature.
			// The provider has already staged its scalar and array when it fails.
			conflict := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procOID}, ObjectNamespace: namespace, ObjectRevision: revision, ObjectName: "citextin", Signature: "2275", Owner: owner}
			if err := stage.Create(conflict); err != nil {
				return err
			}
			return extension.StockCitext.Stage(&extensionHost{Engine: e, Auth: auth, Stage: stage}, ext, owner)
		}()
		e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
			t.Fatal("staged codec collision published a prefix", err)
		}
	})
	t.Run("published_type_conflict", func(t *testing.T) {
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
		memberID, err := e.Catalog.AllocateEnumMemberLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		revision, err := e.Catalog.RevisionLocked()
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		typ, err := sqltype.EnumWithMembers(sqltype.ID(oid), "citext", "public.citext", []sqltype.EnumMember{{ID: memberID, Label: "occupied", SortOrder: 1}})
		if err != nil {
			t.Fatal(err)
		}
		array, err := sqltype.Array(sqltype.ID(arrayOID), "_citext", typ)
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
		before := e.Catalog.State
		e.Mu.Lock()
		_, err = StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "citext"}})
		e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "citext" already exists`, Context: extension.CitextTypeContext}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.Catalog.State != before {
			t.Fatal("type conflict was not atomic/exact", diagnostic, err)
		}
	})
	t.Run("manifest_validation_and_nonmutation", func(t *testing.T) {
		e, ext, base := citextUnitInstalled(t)
		original := slices.Clone(ext.Members)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		for _, corrupt := range []func(*catalog.State, *catalog.Extension){
			func(state *catalog.State, _ *catalog.Extension) {
				forged := *base
				foreign, err := sqltype.QueryInt(base.SQLType.ID(), "citext", base.SQLType.SQLName())
				if err != nil {
					t.Fatal(err)
				}
				forged.SQLType = foreign
				state.SetDescriptor(forged.Address(), &forged)
			},
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
			candidate := catalog.CloneState(e.Catalog.State)
			bad := *ext
			bad.Members = slices.Clone(ext.Members)
			candidate.SetDescriptor(bad.Address(), &bad)
			corrupt(candidate, &bad)
			if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(e.Catalog.Read(candidate), &bad); err == nil {
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

func TestCitextProviderImport(t *testing.T) {
	t.Run("real_import_remapping_and_retained_codec", func(t *testing.T) {
		source, _, base := citextUnitInstalled(t)
		value, err := base.SQLType.Input(context.Background(), "  MiXeD e\u0301 🙂  ", -1)
		if err != nil {
			t.Fatal(err)
		}
		target := New()
		owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
		namespace := intarrayUnitTemporaryNamespace(t, target, owner)
		installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
		extensionUnitPublish(t, target, installer, citextUnitStage(t, target, installer, "unaccent", namespace.Name()))
		unaccent := target.Catalog.State.Get(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		closeInstaller()
		if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		ext := target.Catalog.State.MustExtension(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "citext"}))
		imported := target.Catalog.State.MustType(ext.Members[0])
		if imported.SQLType == base.SQLType || imported.SQLType.ID() == base.SQLType.ID() {
			t.Fatal("setup failed to exercise a genuine retained-TEMP OID collision")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(target.CatalogRead(), ext); err != nil {
			t.Fatal(err)
		}
		if target.Catalog.State.Get(namespace.Address()) != namespace || target.Catalog.State.Get(unaccent.Address()) != unaccent {
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
			foreign, err := sqltype.Citext(imported.SQLType.ID(), "citext", "public.citext")
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
		if text, err := imported.SQLType.Output(context.Background(), mapped); err != nil || text != "  MiXeD e\u0301 🙂  " {
			t.Fatal("imported codec", text, err)
		}
		if err := Reset(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		if text, err := base.SQLType.Output(context.Background(), value); err != nil || text != "  MiXeD e\u0301 🙂  " {
			t.Fatal("retained codec", text, err)
		}
		closeOwner()
		assertTemporaryNamespacePair(t, target, namespace.Temporary, namespace.Address())
		if target.Catalog.State.Get(namespace.Address()) != namespace || target.Catalog.State.Get(unaccent.Address()) != nil {
			t.Fatal("original TEMP provider failed retirement after import")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(target.CatalogRead(), ext); err != nil {
			t.Fatal("persistent imported provider damaged by TEMP retirement", err)
		}
	})
}
