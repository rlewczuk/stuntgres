package control

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func replayProvider(schema, name, destinationSchema, destinationName string) providerReplay {
	return providerReplay{original: providerKey{schema, name, "23"}, current: providerKey{destinationSchema, destinationName, "23"}, arguments: "integer"}
}

func TestProviderReplayVacatedDestination(t *testing.T) {
	a := replayProvider("public", "a", "public", "b")
	b := replayProvider("public", "b", "other", "b")
	statements, err := planProviderReplay([]providerReplay{a, b})
	want := []string{
		"ALTER FUNCTION \"public\".\"b\"(integer) SET SCHEMA \"other\";\n",
		"ALTER FUNCTION \"public\".\"a\"(integer) RENAME TO \"b\";\n",
	}
	if err != nil || !slices.Equal(statements, want) {
		t.Fatalf("replay=%q, %v; want %q", statements, err, want)
	}
	reversed, err := planProviderReplay([]providerReplay{b, a})
	if err != nil || !slices.Equal(statements, reversed) {
		t.Fatalf("input order changed replay: %q, %v", reversed, err)
	}
}

func TestProviderReplayCycleAndOwnerLast(t *testing.T) {
	encrypt := replayProvider("public", "encrypt", "public", "decrypt")
	decrypt := replayProvider("public", "decrypt", "public", "encrypt")
	decrypt.ownerChanged = true
	decrypt.owner = "recipient"
	reserved := replayProvider("public", "__stuntgres_routine_1", "public", "__stuntgres_routine_1")
	got, err := planProviderReplay([]providerReplay{encrypt, reserved, decrypt})
	want := []string{
		"ALTER FUNCTION \"public\".\"decrypt\"(integer) RENAME TO \"__stuntgres_routine_2\";\n",
		"ALTER FUNCTION \"public\".\"encrypt\"(integer) RENAME TO \"decrypt\";\n",
		"ALTER FUNCTION \"public\".\"__stuntgres_routine_2\"(integer) RENAME TO \"encrypt\";\n",
		"ALTER FUNCTION \"public\".\"encrypt\"(integer) OWNER TO \"recipient\";\n",
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("replay=%q, %v; want %q", got, err, want)
	}
}

func TestProviderReplayMoveBeforeRename(t *testing.T) {
	mover := replayProvider("source", "old", "target", "new")
	stationary := replayProvider("source", "new", "source", "new")
	got, err := planProviderReplay([]providerReplay{mover, stationary})
	want := []string{
		"ALTER FUNCTION \"source\".\"old\"(integer) SET SCHEMA \"target\";\n",
		"ALTER FUNCTION \"target\".\"old\"(integer) RENAME TO \"new\";\n",
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("replay=%q, %v; want %q", got, err, want)
	}
}

func TestProviderReplayRejectsCorruptKeys(t *testing.T) {
	a := replayProvider("public", "a", "public", "b")
	b := replayProvider("public", "b", "public", "b")
	duplicateSource := replayProvider("public", "a", "public", "c")
	for _, providers := range [][]providerReplay{{a, a}, {a, b}, {a, duplicateSource}} {
		if _, err := planProviderReplay(providers); err == nil {
			t.Fatal("corrupt provider keys accepted")
		}
	}
}

func TestProviderReplayUnchangedAndOwnerOnly(t *testing.T) {
	p := replayProvider("pg_catalog", "abs", "pg_catalog", "abs")
	got, err := planProviderReplay([]providerReplay{p})
	if err != nil || len(got) != 0 {
		t.Fatalf("unchanged replay=%q, %v", got, err)
	}
	p.ownerChanged = true
	p.owner = "quoted\"recipient"
	got, err = planProviderReplay([]providerReplay{p})
	if err != nil || len(got) != 1 || got[0] != "ALTER FUNCTION \"pg_catalog\".\"abs\"(integer) OWNER TO \"quoted\"\"recipient\";\n" {
		t.Fatalf("owner replay=%q, %v", got, err)
	}
	// Overloads occupy different keys and cannot force unnecessary temporary SQL.
	other := p
	other.ownerChanged = false
	other.original.signature = catalog.ProcedureSignature("20")
	other.current = other.original
	got, err = planProviderReplay([]providerReplay{other, p})
	if err != nil || len(got) != 1 {
		t.Fatalf("overload replay=%q, %v", got, err)
	}
}

func TestProviderReplayEmissionPrerequisites(t *testing.T) {
	var out bytes.Buffer
	ctx := context.Background()
	s := &dumpScript{Capture: &Capture{
		Views:       []OwnedStatement{{"postgres", "CREATE EXTENSION dependency;\n"}, {"postgres", "CREATE EXTENSION dependent;\n"}},
		ViewIndices: map[ids.OID]int{10: 0, 20: 1},
	}, eventOrder: []dump.Event{{OID: 10}, {OID: 20}, {OID: 30}},
		extensionMetadata:  map[ids.OID]string{10: "-- dependency owners\n", 20: "-- dependent owners\n"},
		providerStatements: []string{"ALTER FUNCTION public.digest(text,text) RENAME TO digest_new;\n"}}
	w := &dumpWriter{ctx: ctx, dst: &out}
	if err := emitDumpProviderReplay(ctx, s, w); err != nil {
		t.Fatal(err)
	}
	if err := emitDumpDeclaration(ctx, s, w, dump.Event{OID: 10}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	dependency := strings.Index(text, "CREATE EXTENSION dependency;")
	dependent := strings.Index(text, "CREATE EXTENSION dependent;")
	alter := strings.Index(text, "ALTER FUNCTION")
	owners := strings.Index(text, "-- dependency owners")
	if dependency < 0 || dependent <= dependency || alter <= dependent || owners <= alter || strings.Count(text, "CREATE EXTENSION dependency;") != 1 {
		t.Fatalf("wrong provider prerequisite/metadata ordering: %s", text)
	}
	// No provider changes means this section writes no bytes at all.
	s.providerStatements = nil
	out.Reset()
	if err := emitDumpProviderReplay(ctx, s, w); err != nil || out.Len() != 0 {
		t.Fatalf("unchanged stream affected: %q, %v", out.String(), err)
	}
}

func TestProviderReplayRejectsExplicitACL(t *testing.T) {
	p := &catalog.Procedure{}
	p.ACL.Explicit = true
	c := &dumpCollection{Capture: &Capture{}}
	err := c.collectProvider(p)
	if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit routine ACL" {
		t.Fatalf("explicit ACL capture=%v", err)
	}
	if len(c.Providers) != 0 {
		t.Fatal("rejected provider entered replay")
	}
}

func TestProviderReplayBlockedIntermediateKeys(t *testing.T) {
	mover := replayProvider("source", "old", "target", "new")
	sourceBlocker := replayProvider("source", "new", "source", "new")
	targetBlocker := replayProvider("target", "old", "target", "old")
	got, err := planProviderReplay([]providerReplay{targetBlocker, mover, sourceBlocker})
	want := []string{
		"ALTER FUNCTION \"source\".\"old\"(integer) RENAME TO \"__stuntgres_routine_1\";\n",
		"ALTER FUNCTION \"source\".\"__stuntgres_routine_1\"(integer) SET SCHEMA \"target\";\n",
		"ALTER FUNCTION \"target\".\"__stuntgres_routine_1\"(integer) RENAME TO \"new\";\n",
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("blocked intermediate replay=%q, %v; want %q", got, err, want)
	}
}

func TestProviderReplayRemapKeepsVacatedNames(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	var original *catalog.Procedure
	for _, seed := range routine.BuiltinProcedures(source.Bootstrap) {
		if seed.Name() == "abs" && seed.Signature == "23" {
			p := seed
			original = &p
			break
		}
	}
	if original == nil {
		t.Fatal("released abs(integer) missing")
	}
	source.State.SetDescriptor(original.Address(), original)
	source.State.BindName(catalog.DescriptorNameKey(original), original.Address())
	source.State.SetProcedureNames(catalog.RebuildProcedureNames(source.State.Descriptors()))
	next := catalog.CloneState(source.State)
	renamed := *original
	renamed.ObjectName = "current_abs"
	renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	next.UnbindName(catalog.DescriptorNameKey(original))
	next.SetDescriptor(renamed.Address(), &renamed)
	next.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
	edit := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
	edit.Replace(original, &renamed)
	next.SetProcedureNames(edit.Index)
	movedNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 940000}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{renamed.Namespace(): movedNamespace}
	if err := RemapReplacementIdentities(source.CatalogView, next, nil, mapping, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !next.HasProviderProcedureOverride("abs") || !next.HasProviderProcedureOverride("current_abs") {
		t.Fatal("remap reopened released-name registry fallback")
	}
	selected := next.ProceduresNamed(movedNamespace, "current_abs")
	if len(selected) != 1 || selected[0] != renamed.Address() {
		t.Fatal("remapped provider is not discoverable", selected)
	}
	if len(next.ProceduresNamed(original.Namespace(), "abs")) != 0 {
		t.Fatal("vacated provider spelling was resurrected")
	}
	if source.State.HasProviderProcedureOverride("abs") {
		t.Fatal("replacement changed retained source discovery")
	}
}

func TestProviderReplayInternalIdentityArguments(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	var internal *catalog.Type
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == 2281 {
			internal = &catalog.Type{SQLType: typ, ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PGCatalogNamespaceOID), ObjectRevision: 1, Owner: source.Bootstrap}
			break
		}
	}
	if internal == nil {
		t.Fatal("released internal type missing")
	}
	source.State.SetDescriptor(internal.Address(), internal)
	source.State.BindName(catalog.DescriptorNameKey(internal), internal.Address())
	var handler *catalog.Procedure
	for _, seed := range routine.NativeAccessMethodProcedures(source.Bootstrap) {
		if seed.Name() == "bthandler" {
			copy := seed
			handler = &copy
			break
		}
	}
	if handler == nil {
		t.Fatal("native bthandler declaration missing")
	}
	recipient, ok := source.DomainRoles.Lookup("pg_monitor")
	if !ok {
		t.Fatal("predefined owner fixture missing")
	}
	handler.Owner = recipient.Ref
	source.State.SetDescriptor(handler.Address(), handler)
	source.State.BindName(catalog.DescriptorNameKey(handler), handler.Address())
	collection := &dumpCollection{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}, source: source}
	if err := collectProcedureDump(context.Background(), collection, handler.Address(), handler); err != nil {
		t.Fatal(err)
	}
	statements, err := planProviderReplay(collection.Providers)
	want := []string{"ALTER FUNCTION \"pg_catalog\".\"bthandler\"(pg_catalog.internal) OWNER TO \"pg_monitor\";\n"}
	if err != nil || !slices.Equal(statements, want) {
		t.Fatalf("internal identity replay=%q, %v; want %q", statements, err, want)
	}
	// Identity spelling is symbolic, but cannot fall back to an unverified
	// numeric type OID when its catalog descriptor is absent.
	missing := catalog.CloneState(source.State)
	missing.RemoveDescriptor(internal.Address())
	missingSource := source
	missingSource.State = missing
	missingSource.CatalogView = source.CatalogView.WithState(missing)
	broken := &dumpCollection{Capture: &Capture{CatalogView: missingSource.CatalogView, DomainRoles: source.DomainRoles}, source: missingSource}
	if err := collectProcedureDump(context.Background(), broken, handler.Address(), handler); err == nil || err.Error() != "stuntgres: missing provider argument type" {
		t.Fatalf("missing internal identity accepted: %v", err)
	}
	if len(broken.Providers) != 0 {
		t.Fatal("missing identity entered replay")
	}
}
