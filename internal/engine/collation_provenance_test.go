package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCollationRetainedProviderProvenance(t *testing.T) {
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.retained_provider FROM pg_catalog.pg_unicode_fast`)
	sessionSQL(t, owner, `CREATE TEMP TABLE retained_provider_local(v TEXT COLLATE pg_temp.retained_provider DEFAULT ('ß' COLLATE pg_temp.retained_provider),g TEXT GENERATED ALWAYS AS (upper(v COLLATE pg_temp.retained_provider)) STORED,CHECK(v COLLATE pg_temp.retained_provider > ''))`)
	sessionSQL(t, peer, `CREATE COLLATION pg_temp.foreign_provider FROM pg_catalog.pg_c_utf8`)
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.expired_provider FROM pg_catalog.pg_unicode_fast`)
	before := e.CatalogRead()
	consumer := before.state.descriptors[before.state.names[NameKey{ClassID: RelationClassID, Namespace: owner.temporaryNamespace, Name: "retained_provider_local"}]].(*relationDescriptor)
	address := before.state.names[NameKey{ClassID: CollationClassID, Namespace: owner.temporaryNamespace, Name: "retained_provider"}]
	provider := before.state.descriptors[address].(*collationDescriptor)
	capability, err := provider.usage()
	if err != nil {
		t.Fatal(err)
	}
	ref := ObjectRef{owner: e.owner, Epoch: before.Epoch(), Address: address}
	expiredAddress := before.state.names[NameKey{ClassID: CollationClassID, Namespace: owner.temporaryNamespace, Name: "expired_provider"}]
	expiredCapability, err := before.state.descriptors[expiredAddress].(*collationDescriptor).usage()
	if err != nil {
		t.Fatal(err)
	}
	expiredRef := ObjectRef{owner: e.owner, Epoch: before.Epoch(), Address: expiredAddress}
	sessionSQL(t, owner, `DROP COLLATION pg_temp.expired_provider`)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	if view.Epoch() == before.Epoch() || view.state.descriptors[consumer.Address()] != consumer {
		t.Fatal("fixture did not retain exact TEMP descriptor across replacement")
	}
	if !storedCollationReferenceValid(view, consumer, ref, capability) {
		t.Fatal("lawful physical-owner retained provider rejected")
	}
	seedAddress := ObjectAddress{ClassID: CollationClassID, ObjectID: 100}
	seedCapability, err := view.state.descriptors[seedAddress].(*collationDescriptor).usage()
	if err != nil {
		t.Fatal(err)
	}
	seedRef := ObjectRef{owner: e.owner, Epoch: before.Epoch(), Address: seedAddress}
	if !storedCollationReferenceValid(view, consumer, seedRef, seedCapability) {
		t.Fatal("lawful retained actual pinned seed rejected")
	}
	foreignAddress := before.state.names[NameKey{ClassID: CollationClassID, Namespace: peer.temporaryNamespace, Name: "foreign_provider"}]
	foreignCapability, err := before.state.descriptors[foreignAddress].(*collationDescriptor).usage()
	if err != nil {
		t.Fatal(err)
	}
	incompatible, err := sqltype.RegisteredCollation(uint32(address.ObjectID), 950)
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, owner, `CREATE COLLATION permanent_provider FROM pg_catalog.pg_unicode_fast`)
	sessionSQL(t, owner, `CREATE TABLE permanent_consumer(v INT)`)
	view = e.CatalogRead()
	permanent := view.state.descriptors[view.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "permanent_consumer"}]].(*relationDescriptor)
	permanentAddress := view.state.names[NameKey{ClassID: CollationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "permanent_provider"}]
	permanentCapability, err := view.state.descriptors[permanentAddress].(*collationDescriptor).usage()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		consumer   *relationDescriptor
		ref        ObjectRef
		capability sqltype.CollationRef
	}{
		{"foreign engine", consumer, ObjectRef{owner: New().owner, Epoch: ref.Epoch, Address: address}, capability},
		{"zero epoch", consumer, ObjectRef{owner: e.owner, Address: address}, capability},
		{"future epoch", consumer, ObjectRef{owner: e.owner, Epoch: view.Epoch() + 1, Address: address}, capability},
		{"missing identity", consumer, ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: ObjectAddress{ClassID: CollationClassID, ObjectID: address.ObjectID + 100000}}, capability},
		{"wrong class", consumer, ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: consumer.Address()}, capability},
		{"wrong subid", consumer, ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: ObjectAddress{ClassID: CollationClassID, ObjectID: address.ObjectID, SubID: 1}}, capability},
		{"other physical owner", consumer, ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: foreignAddress}, foreignCapability},
		{"persistent consumer", permanent, ref, capability},
		{"persistent dynamic target", consumer, ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: permanentAddress}, permanentCapability},
		{"incompatible capability", consumer, ref, incompatible},
		{"invalid capability", consumer, ref, sqltype.CollationRef{ID: uint32(address.ObjectID)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if storedCollationReferenceValid(view, c.consumer, c.ref, c.capability) {
				t.Fatal("accepted invalid retained collation authority")
			}
		})
	}
	if storedCollationReferenceValid(view, consumer, expiredRef, expiredCapability) {
		t.Fatal("accepted genuinely dropped retained provider")
	}
	detached := *consumer
	if storedCollationReferenceValid(view, &detached, ref, capability) {
		t.Fatal("accepted detached consumer instead of actual live descriptor")
	}
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	invalidSeed := *view.state.descriptors[seedAddress].(*collationDescriptor)
	invalidSeed.semantics = 811
	if err := stage.Replace(&invalidSeed); err != nil {
		t.Fatal(err)
	}
	if storedCollationReferenceValid(e.stageRead(stage), consumer, seedRef, seedCapability) {
		t.Fatal("accepted candidate with incompatible actual seed provider invariant")
	}
	// Render genuine retained trees through the complete serializer. Compare
	// complete ObjectRefs, including epochs, rather than addresses or one helper.
	assertManifest := func(name string, renderer scalarRenderer, expression BoundExpr, want []ObjectRef) {
		t.Helper()
		var got []ObjectRef
		renderer.dependencies = &got
		if _, err := renderer.sql(context.Background(), view, expression); err != nil {
			t.Fatal(name, "full retained expression rendering", err)
		}
		if len(got) != len(want) {
			t.Fatal(name, "full retained manifest length changed", got, want)
		}
		remaining := make(map[ObjectRef]int, len(want))
		for _, ref := range want {
			remaining[ref]++
		}
		for _, ref := range got {
			if remaining[ref] == 0 {
				t.Fatal(name, "full retained reference changed", ref, want)
			}
			remaining[ref]--
		}
	}
	for _, definition := range consumer.defaults {
		assertManifest("DEFAULT", scalarRenderer{provenance: consumer, capturedManifest: definition.dependencies}, definition.expression, definition.dependencies)
	}
	for _, definition := range consumer.generations {
		assertManifest("generated", scalarRenderer{owner: consumer, capturedManifest: definition.dependencies}, definition.expression, definition.dependencies)
	}
	for _, definition := range consumer.checkConstraints {
		want := make([]ObjectRef, len(definition.dependencies))
		for i, dependency := range definition.dependencies {
			want[i] = dependency.ref
		}
		assertManifest("CHECK", scalarRenderer{owner: consumer, capturedChecks: definition.dependencies}, definition.expression, want)
	}
	// All ordinary validators must accept the retained closed expression trees.
	sessionSQL(t, owner, `INSERT INTO retained_provider_local DEFAULT VALUES`)
	sessionSQL(t, owner, `UPDATE retained_provider_local SET v='é'`)
}
