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
	"stuntgres/internal/engine/catalog"
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
	hidden := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90010}, ObjectName: "hidden", ObjectRevision: 1, Owner: e.authorizationDomain.Principal(10)}
	collision := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90011}, ObjectName: "__stuntgres_conversion_2", ObjectRevision: 1, Owner: e.authorizationDomain.Principal(10)}
	first, second := conversionUnitDefinition(e, 90000, "a", true), conversionUnitDefinition(e, 90001, "b", true)
	plain := conversionUnitDefinition(e, 90002, "plain", false)
	seed := *view.MustConversion(ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: 4519})
	seed.ObjectName, seed.ObjectNamespace, seed.ObjectRevision = "seed_moved", hidden.Address(), 2
	seedCollision := conversionUnitDefinition(e, 90003, "__stuntgres_conversion_seed_4519", false)
	seedCollision.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	stage := e.BeginCatalogStage(view)
	for _, d := range []catalog.Descriptor{hidden, collision, first, second, plain, seedCollision} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := stage.Replace(&seed); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	namespaces := make(map[string]bool)
	for _, descriptor := range e.CatalogRead().Descriptors() {
		if namespace, ok := descriptor.(*catalog.Namespace); ok {
			namespaces[namespace.Name()] = true
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
	for _, descriptor := range current.Descriptors() {
		if namespace, ok := descriptor.(*catalog.Namespace); ok {
			if !namespaces[namespace.Name()] {
				t.Fatal("unexpected replay namespace survived", namespace.Name())
			}
			delete(namespaces, namespace.Name())
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
		namespace := current.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: expected.namespace})
		address := current.NameAddress(ids.NameKey{ClassID: ids.ConversionClassID, Namespace: namespace, Name: expected.name})
		restored, ok := current.Conversion(address)
		if !ok || restored.Source != 6 || restored.Destination != 8 || restored.Handler != first.Handler || restored.DefaultConversion != expected.def || restored.Owner != e.authorizationDomain.Principal(10) {
			t.Fatal("lossless replay conversion metadata", expected, restored)
		}
	}
	count := 0
	for _, descriptor := range current.Descriptors() {
		if d, ok := descriptor.(*catalog.Conversion); ok && d.Namespace().ObjectID == ids.PublicNamespaceOID && d.Source == 6 && d.Destination == 8 && d.DefaultConversion {
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
	if target.CatalogRead().State() != before.State() {
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
	if target.CatalogRead().State() != before.State() || target.CatalogRead().Get(keep.Address()) != keep {
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
	d.Owner = source.Roles().LookupRef("conversion_import_owner")
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
	candidate := catalog.CloneState(source.CatalogRead().State())
	before := target.CatalogRead()
	target.mu.Lock()
	remapped, err := target.remapPreparedCatalog(candidate, sourceRoles, captured, expected)
	target.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	owner := remapped.MustConversion(d.Address()).Owner
	if owner != expected[d.Owner] || target.Roles().ValidatePrincipal(owner) != nil || source.Roles().ValidatePrincipal(owner) == nil || target.Roles().ValidatePrincipal(d.Owner) == nil {
		t.Fatal("actual conversion owner remap", owner, d.Owner)
	}
	// Publish a genuine successor principal under the reused name. Captured
	// import authority must not retarget the old identity to that successor.
	if _, err := target.executeRole(targetOp, &parser.DropRole{Names: []string{"conversion_import_owner"}}, 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Execute(targetOp, creation, 0, nil); err != nil {
		t.Fatal(err)
	}
	candidate = catalog.CloneState(source.CatalogRead().State())
	target.mu.Lock()
	_, err = target.remapPreparedCatalog(candidate, sourceRoles, captured, expected)
	target.mu.Unlock()
	if err == nil {
		t.Fatal("dropped/recreated conversion owner adopted by name")
	}
	if target.CatalogRead().State() != before.State() {
		t.Fatal("failed principal fence replaced target catalog")
	}
	bad := catalog.CloneState(source.CatalogRead().State())
	invalid := *d
	invalid.Handler = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90099}
	bad.SetDescriptor(d.Address(), &invalid)
	if err := (catalog.ConversionProvider{}).Validate(source.catalog.read(bad), &invalid); err == nil {
		t.Fatal("unavailable imported handler admitted")
	}
}
