package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
	consumer := before.MustRelation(before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.temporaryNamespace, Name: "retained_provider_local"}))
	address := before.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: owner.temporaryNamespace, Name: "retained_provider"})
	provider := before.MustCollation(address)
	capability, err := provider.Usage()
	if err != nil {
		t.Fatal(err)
	}
	ref := e.catalog.Domain.Ref(before.Epoch(), address)
	expiredAddress := before.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: owner.temporaryNamespace, Name: "expired_provider"})
	expiredCapability, err := before.MustCollation(expiredAddress).Usage()
	if err != nil {
		t.Fatal(err)
	}
	expiredRef := e.catalog.Domain.Ref(before.Epoch(), expiredAddress)
	sessionSQL(t, owner, `DROP COLLATION pg_temp.expired_provider`)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	if view.Epoch() == before.Epoch() || view.Get(consumer.Address()) != consumer {
		t.Fatal("fixture did not retain exact TEMP descriptor across replacement")
	}
	if !storedCollationReferenceValid(view, consumer, ref, capability) {
		t.Fatal("lawful physical-owner retained provider rejected")
	}
	seedAddress := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 100}
	seedCapability, err := view.MustCollation(seedAddress).Usage()
	if err != nil {
		t.Fatal(err)
	}
	seedRef := e.catalog.Domain.Ref(before.Epoch(), seedAddress)
	if !storedCollationReferenceValid(view, consumer, seedRef, seedCapability) {
		t.Fatal("lawful retained actual pinned seed rejected")
	}
	foreignAddress := before.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: peer.temporaryNamespace, Name: "foreign_provider"})
	foreignCapability, err := before.MustCollation(foreignAddress).Usage()
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
	permanent := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "permanent_consumer"}))
	permanentAddress := view.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "permanent_provider"})
	permanentCapability, err := view.MustCollation(permanentAddress).Usage()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		consumer   *catalog.Relation
		ref        ids.ObjectRef
		capability sqltype.CollationRef
	}{
		{"foreign engine", consumer, New().catalog.Domain.Ref(ref.Epoch, address), capability},
		{"zero epoch", consumer, e.catalog.Domain.Ref(0, address), capability},
		{"future epoch", consumer, e.catalog.Domain.Ref(view.Epoch()+1, address), capability},
		{"missing identity", consumer, e.catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: address.ObjectID + 100000}), capability},
		{"wrong class", consumer, e.catalog.Domain.Ref(ref.Epoch, consumer.Address()), capability},
		{"wrong subid", consumer, e.catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: address.ObjectID, SubID: 1}), capability},
		{"other physical owner", consumer, e.catalog.Domain.Ref(ref.Epoch, foreignAddress), foreignCapability},
		{"persistent consumer", permanent, ref, capability},
		{"persistent dynamic target", consumer, e.catalog.Domain.Ref(ref.Epoch, permanentAddress), permanentCapability},
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
	invalidSeed := *view.MustCollation(seedAddress)
	invalidSeed.Semantics = 811
	if err := stage.Replace(&invalidSeed); err != nil {
		t.Fatal(err)
	}
	if storedCollationReferenceValid(e.stageRead(stage), consumer, seedRef, seedCapability) {
		t.Fatal("accepted candidate with incompatible actual seed provider invariant")
	}
	// Render genuine retained trees through the complete serializer. Compare
	// complete ObjectRefs, including epochs, rather than addresses or one helper.
	assertManifest := func(name string, renderer scalarRenderer, expression expr.Expr, want []ids.ObjectRef) {
		t.Helper()
		var got []ids.ObjectRef
		renderer.dependencies = &got
		if _, err := renderer.sql(context.Background(), view, expression); err != nil {
			t.Fatal(name, "full retained expression rendering", err)
		}
		if len(got) != len(want) {
			t.Fatal(name, "full retained manifest length changed", got, want)
		}
		remaining := make(map[ids.ObjectRef]int, len(want))
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
	for _, definition := range consumer.ColumnMeta.Defaults {
		assertManifest("DEFAULT", scalarRenderer{provenance: consumer, capturedManifest: definition.Dependencies}, definition.Expression, definition.Dependencies)
	}
	for _, definition := range consumer.ColumnMeta.Generations {
		assertManifest("generated", scalarRenderer{owner: consumer, capturedManifest: definition.Dependencies}, definition.Expression, definition.Dependencies)
	}
	for _, definition := range consumer.CheckConstraints {
		want := make([]ids.ObjectRef, len(definition.Dependencies))
		for i, dependency := range definition.Dependencies {
			want[i] = dependency.Ref
		}
		assertManifest("CHECK", scalarRenderer{owner: consumer, capturedChecks: definition.Dependencies}, definition.Expression, want)
	}
	// All ordinary validators must accept the retained closed expression trees.
	sessionSQL(t, owner, `INSERT INTO retained_provider_local DEFAULT VALUES`)
	sessionSQL(t, owner, `UPDATE retained_provider_local SET v='é'`)
}
