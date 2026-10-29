package control

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// Bind a real released class and its actual executable support. No ordering,
// constructor callback, provider capability or inspection API is manufactured.
func rangeReplayCallbackSource(t *testing.T) (Source, *catalog.Type, *catalog.Type) {
	t.Helper()
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	catalog.InstallBuiltinClasses(source.State, source.Bootstrap)
	catalog.InstallBuiltinFamilies(source.State, source.Bootstrap)
	catalog.InstallBuiltinAccessMethods(source.State)
	catalog.InstallBuiltinProcedures(source.State, routine.BuiltinProcedures(source.Bootstrap))
	integer, _ := source.CatalogView.LookupType(sqltype.Int4)
	class, ok, err := source.CatalogView.DefaultIndexClass(403, integer)
	if err != nil || !ok {
		t.Fatal("missing released int4 class", err)
	}
	class, err = routine.BindClassSupport(source.CatalogView, class)
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := routine.BindIndexOrdering(source.CatalogView, integer.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	boolean, _ := source.CatalogView.LookupType(sqltype.Bool)
	rangeType, multiType, err := sqltype.NewRangeFamily(78000, "same_name", "public.same_name", 78001, "same_name", "pg_catalog.same_name", sqltype.RangeDefinition{Subtype: integer.Usage(), Class: class, Ordering: ordering}, boolean)
	if err != nil {
		t.Fatal(err)
	}
	var parents []*catalog.Type
	for i, scalar := range []*sqltype.Descriptor{rangeType, multiType} {
		namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		if i == 1 {
			namespace.ObjectID = ids.PGCatalogNamespaceOID
		}
		array, err := sqltype.Array(sqltype.ID(78002+i), "_same_name", scalar)
		if err != nil {
			t.Fatal(err)
		}
		// Match real range creation: the multirange parent and generated
		// arrays retain synthetic type defaults, independently of mutable
		// constructor owners and routine ACLs.
		parent := &catalog.Type{SQLType: scalar, ObjectNamespace: namespace, ObjectRevision: 1, Owner: source.Bootstrap, ACL: authorization.DefaultBaseline(source.Bootstrap, authorization.DefaultTypes, false), ACLSyntheticDefault: true, Array: array.ID()}
		companion := &catalog.Type{SQLType: array, ObjectNamespace: namespace, ObjectRevision: 1, Owner: source.Bootstrap, ACL: authorization.DefaultBaseline(source.Bootstrap, authorization.DefaultTypes, false), ACLSyntheticDefault: true}
		for _, descriptor := range []*catalog.Type{parent, companion} {
			source.State.SetDescriptor(descriptor.Address(), descriptor)
			source.State.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
		}
		parents = append(parents, parent)
	}
	for i, form := range []catalog.RangeConstructorForm{catalog.RangeConstructorTwo, catalog.RangeConstructorThree, catalog.MultirangeConstructorZero, catalog.MultirangeConstructorOne, catalog.MultirangeConstructorMany} {
		typ := rangeType
		if form >= catalog.MultirangeConstructorZero {
			typ = multiType
		}
		provider := &catalog.RangeConstructor{Type: typ, Form: form}
		signature, err := provider.Signature(source.CatalogView)
		if err != nil {
			t.Fatal(err)
		}
		procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(78100 + i)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "retained_constructor", ObjectRevision: 1, Signature: signature, Result: ids.OID(typ.ID()), RoutineKind: catalog.FunctionRoutine, Owner: source.Bootstrap, Volatility: expr.Immutable, Strict: form >= catalog.MultirangeConstructorZero, RangeConstructor: provider, Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: provider.Source()}}
		if i == 0 {
			procedure.Owner = source.DomainRoles.LookupRef("pg_read_all_stats")
			procedure.ACL = authorization.ACLValue{Explicit: true}
			procedure.Parallel, procedure.Cost = 'r', 9.25
		}
		source.State.SetDescriptor(procedure.Address(), procedure)
		source.State.BindName(catalog.DescriptorNameKey(procedure), procedure.Address())
		if err := catalog.ValidateRangeConstructor(source.CatalogView, procedure); err != nil {
			t.Fatal(err)
		}
	}
	return source, parents[0], parents[1]
}

func TestTypeReplayCallbackRangeCreation(t *testing.T) {
	source, root, multi := rangeReplayCallbackSource(t)
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}, installationTypeReplay: newInstallationTypeReplay(source.CatalogView, context.Background()), typeReplayEpilogues: map[ids.OID]string{}, rangeCreations: map[ids.OID]*rangeReplayCreation{}}
	collection := &dumpCollection{Capture: script.Capture, source: source}
	for _, procedure := range script.installationTypeReplay.constructors[root.SQLType.ID()] {
		if err := collectProcedureDump(context.Background(), collection, procedure.Address(), procedure); err != nil {
			t.Fatal("genuine constructor authority rejected", err)
		}
	}
	declarations, err := typedef.MixedDumpStatements(context.Background(), Presenter{}, source.CatalogView, source.DomainRoles, []ids.OID{root.Address().ObjectID}, map[ids.OID]*catalog.Type{root.Address().ObjectID: root}, nil, script.installationTypeReplay.names, script.beforeTypeCreate, script.ordinaryTypeArrayReplay)
	if err != nil {
		t.Fatal(err)
	}
	creation := script.rangeCreations[root.Address().ObjectID]
	if creation.coordinates.Root.Namespace == root.Namespace() || creation.coordinates.Root.Name == creation.coordinates.Companion.Name {
		t.Fatal("equal public labels were not isolated into distinct creation coordinates")
	}
	statement := declarations[root.Address().ObjectID].SQL
	if !strings.Contains(statement, "CREATE TYPE "+creation.coordinates.RootSQL+" AS RANGE") || !strings.Contains(statement, "MULTIRANGE_TYPE_NAME = "+creation.coordinates.CompanionSQL) || !strings.Contains(statement, "SUBTYPE = INTEGER") || !strings.Contains(statement, `SUBTYPE_OPCLASS = "pg_catalog"."int4_ops"`) {
		t.Fatal("creation coordinates replaced actual prerequisite identities", statement)
	}
	if prefix := strings.Join(script.installationStatements, ""); strings.Contains(prefix, "CREATE SCHEMA "+catalog.QuoteIdentifier(creation.schema)) {
		t.Fatal("range work schema must not shift earlier numeric alias endpoints", prefix)
	}
	epilogue := script.typeReplayEpilogues[root.Address().ObjectID]
	if !strings.Contains(epilogue, "SET SCHEMA \"public\"") || !strings.Contains(epilogue, "SET SCHEMA \"pg_catalog\"") || !strings.Contains(epilogue, "RENAME TO \"retained_constructor\"") || !strings.Contains(epilogue, "OWNER TO \"pg_read_all_stats\"") || !strings.HasSuffix(epilogue, "DROP SCHEMA "+catalog.QuoteIdentifier(creation.schema)+";\n") {
		t.Fatal("separate parents, mutable constructors, authority or retirement lost", epilogue)
	}
	if strings.Contains(epilogue, "CREATE FUNCTION") || len(collection.OwnedRoutines) != 0 || len(collection.Providers) != 5 {
		t.Fatal("generated constructors acquired a standalone declaration or lost their metadata records")
	}
	for _, provider := range collection.Providers {
		if provider.readyAfter != root.Address().ObjectID || provider.original != provider.current || provider.ownerChanged || provider.owner != "" {
			t.Fatal("constructor metadata acquired an early event, identity mover or second owner", provider)
		}
	}
	metadata, err := planProviderReplay(collection.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 || !strings.HasSuffix(metadata[0], " PARALLEL RESTRICTED;\n") || !strings.HasSuffix(metadata[1], " COST 9.25;\n") {
		t.Fatal("constructor current estimates lost", metadata)
	}
	for _, statement := range metadata {
		if !strings.HasPrefix(statement, `ALTER FUNCTION "public"."retained_constructor"(`) || strings.Contains(statement, "OWNER TO") || strings.Contains(statement, "RENAME TO") || strings.Contains(statement, "SET SCHEMA") {
			t.Fatal("constructor estimate event must be ALTER-only at final identity", statement)
		}
	}
	script.providerEventStatements = map[ids.OID][]string{root.Address().ObjectID: metadata}
	for _, parent := range []*catalog.Type{root, multi} {
		if script.installationTypeReplay.names.Names[parent.SQLType.ID()] != catalog.DescriptorNameKey(parent) || script.installationTypeReplay.names.Names[parent.Array].Namespace != parent.Namespace() || source.State.Get(parent.Address()) != parent {
			t.Fatal("final model diverged or immutable source changed")
		}
	}
	// Exercise the nonbootstrap emission protocol without changing the source
	// family: the owner pair must use actual creation coordinates, immediately
	// before the privileged final-identity epilogue.
	declaration := declarations[root.Address().ObjectID]
	declaration.Owner = "pg_read_all_stats"
	script.domainNodes = map[ids.OID]*catalog.Type{root.Address().ObjectID: root}
	var output bytes.Buffer
	writer := &dumpWriter{ctx: context.Background(), dst: &output}
	if err := emitDumpStatement(script, writer, root.Address().ObjectID, declaration); err != nil {
		t.Fatal(err)
	}
	paired := declaration.SQL + "ALTER TYPE " + creation.coordinates.RootSQL + " OWNER TO pg_read_all_stats;\n"
	if !strings.Contains(output.String(), paired+epilogue) || writer.currentOwner != "postgres" {
		t.Fatal("fresh owner pair was interrupted, used final coordinates or changed the normalization actor", output.String(), writer.currentOwner)
	}
	schemaSQL := "CREATE SCHEMA " + catalog.QuoteIdentifier(creation.schema) + ";\n"
	if !strings.HasPrefix(output.String(), "SET SESSION AUTHORIZATION 'postgres';\n"+schemaSQL) || strings.Count(output.String(), schemaSQL) != 1 || strings.Index(output.String(), schemaSQL) > strings.Index(output.String(), "SET SESSION AUTHORIZATION 'pg_read_all_stats';\n") {
		t.Fatal("private schema must precede the exact owner pair at the range declaration", output.String())
	}
	if err := emitDumpProviderEvent(script, writer, root.Address().ObjectID); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.HasSuffix(text, strings.Join(metadata, "")) || strings.Index(text, "DROP SCHEMA "+catalog.QuoteIdentifier(creation.schema)) > strings.Index(text, " PARALLEL RESTRICTED") {
		t.Fatal("constructor estimates preceded final identity/current-owner completion", text)
	}
}

func TestTypeReplayCallbackRelationBoundary(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	integer, _ := source.CatalogView.LookupType(sqltype.Int4)
	scalar, err := sqltype.Composite(79000, "row_owner", "public.row_owner", 79001, []sqltype.CompositeMember{{Attribute: 1, Name: "i", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(79001, "___row_owner", scalar)
	if err != nil {
		t.Fatal(err)
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 79002}, ObjectNamespace: namespace, ObjectName: "row_owner", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.TableRelation, RowType: scalar.ID(), Columns: []catalog.Column{{Name: "i", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1}, Index: map[string]int{"i": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	root := &catalog.Type{SQLType: scalar, ObjectNamespace: namespace, ObjectRevision: 1, Owner: source.Bootstrap, Array: array.ID(), OwningRelation: relation.Address()}
	for _, descriptor := range []catalog.Descriptor{relation, root, &catalog.Type{SQLType: array, ObjectNamespace: namespace, ObjectRevision: 1, Owner: source.Bootstrap}} {
		source.State.SetDescriptor(descriptor.Address(), descriptor)
		source.State.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	}
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView}, installationTypeReplay: newInstallationTypeReplay(source.CatalogView, context.Background()), typeReplayEpilogues: map[ids.OID]string{}}
	declarations, err := typedef.MixedDumpStatements(context.Background(), Presenter{}, source.CatalogView, source.DomainRoles, []ids.OID{relation.Address().ObjectID}, nil, nil, script.installationTypeReplay.names, script.beforeTypeCreate, script.ordinaryTypeArrayReplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(declarations) != 0 {
		t.Fatal("suffix-only declaration would replace real table CREATE", declarations)
	}
	epilogue := script.typeReplayEpilogues[relation.Address().ObjectID]
	if !strings.Contains(epilogue, "ALTER TABLE \"public\".\"row_owner\" SET SCHEMA") || strings.Contains(epilogue, "ALTER TYPE ") || script.installationTypeReplay.names.Names[array.ID()] != catalog.DescriptorNameKey(source.CatalogView.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())})) {
		t.Fatal("protected rowtype bypass or final scar mismatch", epilogue)
	}
	var output bytes.Buffer
	writer := &dumpWriter{ctx: context.Background(), dst: &output, currentOwner: "pg_read_all_stats"}
	if err := script.emitTypeReplayEpilogue(writer, relation.Address().ObjectID); err != nil {
		t.Fatal(err)
	}
	if writer.currentOwner != "pg_read_all_stats" || !strings.HasPrefix(output.String(), "SET SESSION AUTHORIZATION 'postgres';\n") || !strings.HasSuffix(output.String(), "SET SESSION AUTHORIZATION 'pg_read_all_stats';\n") {
		t.Fatal("normalization changed emitting authority", output.String())
	}
}
