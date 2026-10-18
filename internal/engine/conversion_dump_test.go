package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestConversionDumpLegalDuplicateDefaultsAndSeedCollisions(t *testing.T) {
	ctx := context.Background()
	e := New()
	var canonical bytes.Buffer
	if err := e.Dump(ctx, &canonical); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	hidden := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: 90010}, name: "hidden", revision: 1, owner: e.authorizationDomain.Principal(10)}
	collision := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: 90011}, name: "__stuntgres_conversion_2", revision: 1, owner: e.authorizationDomain.Principal(10)}
	first, second := conversionUnitDefinition(e, 90000, "a", true), conversionUnitDefinition(e, 90001, "b", true)
	plain := conversionUnitDefinition(e, 90002, "plain", false)
	seed := *view.state.descriptors[ids.ObjectAddress{ClassID: ConversionClassID, ObjectID: 4519}].(*conversionDescriptor)
	seed.name, seed.namespace, seed.revision = "seed_moved", hidden.address, 2
	seedCollision := conversionUnitDefinition(e, 90003, "__stuntgres_conversion_seed_4519", false)
	seedCollision.namespace = ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
	stage := e.BeginCatalogStage(view)
	for _, d := range []Descriptor{hidden, collision, first, second, plain, seedCollision} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := stage.Replace(&seed); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	namespaces := make(map[string]bool)
	for _, descriptor := range e.CatalogRead().state.descriptors {
		if namespace, ok := descriptor.(*namespaceDescriptor); ok {
			namespaces[namespace.name] = true
		}
	}
	var dump bytes.Buffer
	if err := e.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "$libdir") || strings.Contains(dump.String(), "pg_conversion.dat") {
		t.Fatal("provider payload serialized")
	}
	if err := e.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	var reset bytes.Buffer
	if err := e.Dump(ctx, &reset); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical.Bytes(), reset.Bytes()) {
		t.Fatal("canonical seed-only dump changed")
	}
	if err := e.Restore(ctx, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("actual native duplicate-default replay", err, dump.String())
	}
	current := e.CatalogRead()
	for _, descriptor := range current.state.descriptors {
		if namespace, ok := descriptor.(*namespaceDescriptor); ok {
			if !namespaces[namespace.name] {
				t.Fatal("unexpected replay namespace survived", namespace.name)
			}
			delete(namespaces, namespace.name)
		}
	}
	if len(namespaces) != 0 {
		t.Fatal("existing namespace collision lost", namespaces)
	}
	for _, expected := range []struct {
		namespace, name string
		def             bool
	}{
		{"public", "a", true}, {"public", "b", true}, {"public", "plain", false},
		{"pg_catalog", "__stuntgres_conversion_seed_4519", false}, {"hidden", "seed_moved", true},
	} {
		namespace := current.state.names[ids.NameKey{ClassID: NamespaceClassID, Name: expected.namespace}]
		address := current.state.names[ids.NameKey{ClassID: ConversionClassID, Namespace: namespace, Name: expected.name}]
		restored, ok := current.state.descriptors[address].(*conversionDescriptor)
		if !ok || restored.source != 6 || restored.destination != 8 || restored.handler != first.handler || restored.defaultConversion != expected.def || restored.owner != e.authorizationDomain.Principal(10) {
			t.Fatal("lossless replay conversion metadata", expected, restored)
		}
	}
	count := 0
	for _, descriptor := range current.state.descriptors {
		if d, ok := descriptor.(*conversionDescriptor); ok && d.Namespace().ObjectID == PublicNamespaceOID && d.source == 6 && d.destination == 8 && d.defaultConversion {
			count++
		}
	}
	if count != 2 {
		t.Fatal("legal duplicate default state lost", count)
	}
	var redump bytes.Buffer
	if err := e.Dump(ctx, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("helper identity allocation changed deterministic dump", dump.String(), redump.String())
	}
}

type conversionBlockedReader struct {
	input            *bytes.Reader
	entered, release chan struct{}
	once             sync.Once
}

func (r *conversionBlockedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.input.Read(p)
}

func TestConversionDumpBlockedRestoreCancellation(t *testing.T) {
	source := New()
	stage := source.BeginCatalogStage(source.CatalogRead())
	definition := conversionUnitDefinition(source, 90000, "imported", true)
	if err := stage.Create(definition); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, source, stage)
	var dump bytes.Buffer
	if err := source.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	target := New()
	stage = target.BeginCatalogStage(target.CatalogRead())
	keep := conversionUnitDefinition(target, 90000, "keep", false)
	if err := stage.Create(keep); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, target, stage)
	before := target.CatalogRead()
	reader := &conversionBlockedReader{input: bytes.NewReader(dump.Bytes()), entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- target.Restore(ctx, reader) }()
	joined, released := false, false
	defer func() {
		cancel()
		if !released {
			close(reader.release)
		}
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("restore cancellation worker did not join")
			}
		}
	}()
	select {
	case <-reader.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual restore reader never admitted")
	}
	if target.CatalogRead().state != before.state {
		t.Fatal("blocked reader replaced target catalog")
	}
	cancel()
	close(reader.release)
	released = true
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal("admitted restore cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admitted restore cancellation did not join")
	}
	if target.CatalogRead().state != before.state || target.CatalogRead().state.descriptors[keep.Address()] != keep {
		t.Fatal("canceled import replaced sentinel conversion")
	}
}

func TestConversionRemapCapturedPrincipalFence(t *testing.T) {
	source, target := New(), New()
	sourceOp, _ := conversionUnitAuth(t, source)
	targetOp, _ := conversionUnitAuth(t, target)
	creation := &parser.CreateRole{Name: parser.Identifier{Text: "conversion_import_owner"}, DefaultLogin: true}
	if _, err := source.Execute(sourceOp, creation, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Execute(targetOp, creation, 0, nil); err != nil {
		t.Fatal(err)
	}
	d := conversionUnitDefinition(source, 90000, "named_owner", false)
	d.owner = source.Roles().LookupRef("conversion_import_owner")
	stage := source.BeginCatalogStage(source.CatalogRead())
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, source, stage)
	sourceRoles, captured := source.Roles(), target.Roles()
	expected := make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	for ref, role := range sourceRoles.RoleEntries() {
		actual, ok := captured.Lookup(role.Name)
		if !ok {
			t.Fatal("missing ordinary builtin principal", role.Name)
		}
		expected[ref] = actual.Ref
	}
	candidate := cloneCatalog(source.CatalogRead().state)
	before := target.CatalogRead()
	target.mu.Lock()
	remapped, err := target.remapPreparedCatalog(candidate, sourceRoles, captured, expected)
	target.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	owner := remapped.descriptors[d.Address()].(*conversionDescriptor).owner
	if owner != expected[d.owner] || target.Roles().ValidatePrincipal(owner) != nil || source.Roles().ValidatePrincipal(owner) == nil || target.Roles().ValidatePrincipal(d.owner) == nil {
		t.Fatal("actual conversion owner remap", owner, d.owner)
	}
	// Publish a genuine successor principal under the reused name. Captured
	// import authority must not retarget the old identity to that successor.
	if _, err := target.executeRole(targetOp, &parser.DropRole{Names: []string{"conversion_import_owner"}}, 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Execute(targetOp, creation, 0, nil); err != nil {
		t.Fatal(err)
	}
	candidate = cloneCatalog(source.CatalogRead().state)
	target.mu.Lock()
	_, err = target.remapPreparedCatalog(candidate, sourceRoles, captured, expected)
	target.mu.Unlock()
	if err == nil {
		t.Fatal("dropped/recreated conversion owner adopted by name")
	}
	if target.CatalogRead().state != before.state {
		t.Fatal("failed principal fence replaced target catalog")
	}
	bad := cloneCatalog(source.CatalogRead().state)
	invalid := *d
	invalid.handler = ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: 90099}
	bad.descriptors[d.Address()] = &invalid
	if err := (conversionProvider{}).Validate(CatalogRead{source, bad}, &invalid); err == nil {
		t.Fatal("unavailable imported handler admitted")
	}
}
