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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func cubeUnitStage(t *testing.T, e *core.Engine, op *session.Operation, name, schema string) *core.CatalogStage {
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

func cubeUnitInstalled(t *testing.T) (*core.Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "cube", ""))
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	return e, ext, e.Catalog.State.MustType(ext.Members[0])
}

func TestCubeProviderIdentity(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	view := e.CatalogRead()
	if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(view, ext); err != nil {
		t.Fatal(err)
	}
	if ext.Version != "1.5" || len(ext.Members) != 48 || base.Extension != ext.Address() || !base.SQLType.IsCube() || base.Owner != ext.Owner {
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
	for _, address := range ext.Members {
		member := view.Get(address)
		if member == nil || member.Namespace() != ext.TargetNamespace || catalog.ExtensionMembership(member) != ext.Address() {
			t.Fatal("membership", address)
		}
		owner, _, _ := access.DescriptorACL(member)
		if owner != ext.Owner {
			t.Fatal("owner", address)
		}
		var err error
		switch member.Address().ClassID {
		case ids.TypeClassID:
			err = (stored.TypeProvider{}).Validate(view, member)
		case ids.ProcedureClassID:
			err = (extension.CatalogProvider{ids.ProcedureClassID}).Validate(view, member)
		case ids.OperatorClassID:
			err = (routine.OperatorProvider{}).Validate(view, member)
		case ids.OperatorClassCatalogID:
			err = (catalog.OperatorClassProvider{}).Validate(view, member)
		case ids.OperatorFamilyClassID:
			err = (catalog.OperatorFamilyProvider{Class: ids.OperatorFamilyClassID}).Validate(view, member)
		default:
			t.Fatal("unexpected cube member", address)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	cubeID := string(extension.CubeSignature(base.SQLType.ID()))
	for i, expected := range []struct {
		name, signature string
		result          ids.OID
	}{
		{"cube_in", "2275", ids.OID(base.SQLType.ID())}, {"cube_out", cubeID, 2275},
		{"cube", "701", ids.OID(base.SQLType.ID())}, {"cube", "701,701", ids.OID(base.SQLType.ID())},
		{"cube", "1022", ids.OID(base.SQLType.ID())}, {"cube", "1022,1022", ids.OID(base.SQLType.ID())},
		{"cube", cubeID + ",701", ids.OID(base.SQLType.ID())}, {"cube", cubeID + ",701,701", ids.OID(base.SQLType.ID())},
		{"cube_dim", cubeID, 23}, {"cube_is_point", cubeID, 16},
		{"cube_ll_coord", cubeID + ",23", 701}, {"cube_ur_coord", cubeID + ",23", 701},
		{"cube_coord", cubeID + ",23", 701}, {"cube_coord_llur", cubeID + ",23", 701},
		{"cube_subset", cubeID + ",1007", ids.OID(base.SQLType.ID())},
		{"cube_union", cubeID + "," + cubeID, ids.OID(base.SQLType.ID())},
		{"cube_inter", cubeID + "," + cubeID, ids.OID(base.SQLType.ID())},
		{"cube_enlarge", cubeID + ",701,23", ids.OID(base.SQLType.ID())},
	} {
		p, ok := view.Procedure(view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: expected.name, Signature: expected.signature}))
		if !ok || p.Name() != expected.name || string(p.Signature) != expected.signature || p.Result != expected.result {
			t.Fatal("selected manifest signature/result", i, p, expected)
		}
	}
	facts, err := pgcatalog.TypeFacts(context.Background(), view, base, sqltype.OutputEnvironment{})
	if err != nil || facts.Input != uint32(ext.Members[1].ObjectID) || facts.Output != uint32(ext.Members[2].ObjectID) || facts.Receive != 0 || facts.Send != 0 {
		t.Fatal("selected manifest corrupted codec projection", facts, err)
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
	if len(members) != 48 || !arrayInternal {
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
	if record.Name != "cube" || record.Schema != "public" || record.Version != "1.5" || record.Owner != "postgres" || len(record.Members) != 48 {
		t.Fatal("owner metadata", record)
	}
	for _, member := range record.Members {
		if member.Owner != "postgres" {
			t.Fatal("member owner", member)
		}
	}
	wantSignatures := map[string]bool{
		"cube_in(2275)": true, "cube_out(cube)": true, "cube": true,
		"cube(701)": true, "cube(701,701)": true, "cube(1022)": true, "cube(1022,1022)": true,
		"cube(cube,701)": true, "cube(cube,701,701)": true,
		"cube_dim(cube)": true, "cube_is_point(cube)": true,
		"cube_ll_coord(cube,23)": true, "cube_ur_coord(cube,23)": true,
		"cube_coord(cube,23)": true, "cube_coord_llur(cube,23)": true,
		"cube_subset(cube,1007)": true, "cube_union(cube,cube)": true,
		"cube_inter(cube,cube)": true, "cube_enlarge(cube,701,23)": true,
		"->": true, "~>": true,
		"cube_overlap(cube,cube)": true, "cube_contains(cube,cube)": true, "cube_contained(cube,cube)": true,
		"cube_distance(cube,cube)": true, "distance_taxicab(cube,cube)": true, "distance_chebyshev(cube,cube)": true,
		"&&": true, "@>": true, "<@": true, "<->": true, "<#>": true, "<=>": true,
		"cube_eq(cube,cube)": true, "cube_ne(cube,cube)": true, "cube_lt(cube,cube)": true,
		"cube_le(cube,cube)": true, "cube_gt(cube,cube)": true, "cube_ge(cube,cube)": true, "cube_cmp(cube,cube)": true,
		"=": true, "<>": true, "<": true, "<=": true, ">": true, ">=": true,
		"operator_class:cube_ops(btree)": true, "operator_family:cube_ops(btree)": true,
	}
	for _, member := range record.Members {
		key := member.Name
		if member.Kind == "procedure" {
			key += "(" + member.Signature + ")"
		} else if member.Kind == "operator_class" || member.Kind == "operator_family" {
			key = member.Kind + ":" + key + "(" + member.Signature + ")"
		}
		if !wantSignatures[key] {
			t.Fatal("nonportable or duplicate cube ownership signature", member)
		}
		delete(wantSignatures, key)
	}
	if len(wantSignatures) != 0 {
		t.Fatal("missing cube ownership signatures", wantSignatures)
	}
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	if _, err := extension.ImportOwners(context.Background(), view.WithState(catalog.CloneState(view.State())), roles, records); err != nil {
		t.Fatal("canonical cube ownership import", err)
	}
	badRecord := record
	badRecord.Members = slices.Clone(record.Members)
	for i, member := range badRecord.Members {
		if member.Name == "cube_out" {
			badRecord.Members[i].Signature = cubeID
		}
	}
	if _, err := extension.ImportOwners(context.Background(), view.WithState(catalog.CloneState(view.State())), roles, []extension.OwnerRecord{badRecord}); err == nil {
		t.Fatal("raw dynamic cube OID accepted as portable ownership identity")
	}
}

func TestCubeProviderConflicts(t *testing.T) {
	cubeComparisonProviderConflicts(t)
	for _, collision := range []struct{ label, name, signature string }{
		{"staged_codec_conflict", "cube_in", "2275"},
		{"staged_constructor_conflict", "cube", "701"},
	} {
		t.Run(collision.label, func(t *testing.T) {
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
				ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: extOID}, ObjectRevision: revision, ObjectName: "cube", Version: "1.5", TargetNamespace: namespace, Owner: owner}
				if err := stage.Create(ext); err != nil {
					return err
				}
				// A genuine preceding staged procedure occupies a selected signature.
				// Earlier staged type/codec members must never escape the collision.
				conflict := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procOID}, ObjectNamespace: namespace, ObjectRevision: revision, ObjectName: collision.name, Signature: catalog.ProcedureSignature(collision.signature), Owner: owner}
				if err := stage.Create(conflict); err != nil {
					return err
				}
				return extension.StockCube.Stage(&extensionHost{Engine: e, Auth: auth, Stage: stage}, ext, owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
				t.Fatal("staged codec collision published a prefix", err)
			}
		})
	}
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
		before := e.Catalog.State
		e.Mu.Lock()
		_, err = StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "cube"}})
		e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "cube" already exists`, Context: extension.CubeTypeContext}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.Catalog.State != before {
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
	t.Run("selected_signature_validation", func(t *testing.T) {
		e, ext, _ := cubeUnitInstalled(t)
		for _, address := range ext.Members[3:] {
			if address.ClassID != ids.ProcedureClassID {
				continue
			}
			for _, corrupt := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Signature += ",701" },
				func(p *catalog.Procedure) { p.Result = 25 },
				func(p *catalog.Procedure) { p.Strict = false },
			} {
				state := catalog.CloneState(e.Catalog.State)
				proc := *state.MustProcedure(address)
				corrupt(&proc)
				state.SetDescriptor(address, &proc)
				if err := extension.StockCube.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
					t.Fatal("invalid selected procedure accepted", proc)
				}
			}
		}
	})
}

func TestCubeProviderControls(t *testing.T) {
	t.Run("comparison_manifest_remap", cubeComparisonProviderRemap)
	t.Run("comparison_expression_remap", cubeComparisonExpressionRemap)
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
		unaccent := target.Catalog.State.Get(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		closeInstaller()
		if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		ext := target.Catalog.State.MustExtension(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
		imported := target.Catalog.State.MustType(ext.Members[0])
		if imported.SQLType == base.SQLType || imported.SQLType.ID() == base.SQLType.ID() {
			t.Fatal("setup failed to exercise a genuine retained-TEMP OID collision")
		}
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(target.CatalogRead(), ext); err != nil {
			t.Fatal(err)
		}
		sourceExt := source.Catalog.State.MustExtension(source.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
		for i, oldAddress := range sourceExt.Members[1:] {
			if oldAddress.ClassID != ids.ProcedureClassID {
				continue
			}
			old := source.Catalog.State.MustProcedure(oldAddress)
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{base.Address(): imported.Address(), oldAddress: ext.Members[i+1]}
			mapped := extension.RemapDescriptor(old, mapping).(*catalog.Procedure)
			actual := target.Catalog.State.MustProcedure(ext.Members[i+1])
			if mapped.Signature != actual.Signature || mapped.Result != actual.Result || mapped == old {
				t.Fatal("constructor formals/results did not remint", old, actual)
			}
		}
		if target.Catalog.State.Get(namespace.Address()) != namespace || target.Catalog.State.Get(unaccent.Address()) != unaccent {
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
		if err := Reset(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		if text, err := base.SQLType.Output(context.Background(), value); err != nil || text != "(3, 1),(1, 4)" {
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
