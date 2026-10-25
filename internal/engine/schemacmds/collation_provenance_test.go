package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestCollationRetainedProviderProvenance(t *testing.T) {
	e := engine.New()
	owner, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, `CREATE COLLATION pg_temp.retained_provider FROM pg_catalog.pg_unicode_fast`)
	enginetest.SessionSQL(t, owner, `CREATE TEMP TABLE retained_provider_local(v TEXT COLLATE pg_temp.retained_provider DEFAULT ('ß' COLLATE pg_temp.retained_provider),g TEXT GENERATED ALWAYS AS (upper(v COLLATE pg_temp.retained_provider)) STORED,CHECK(v COLLATE pg_temp.retained_provider > ''))`)
	enginetest.SessionSQL(t, peer, `CREATE COLLATION pg_temp.foreign_provider FROM pg_catalog.pg_c_utf8`)
	enginetest.SessionSQL(t, owner, `CREATE COLLATION pg_temp.expired_provider FROM pg_catalog.pg_unicode_fast`)
	before := e.CatalogRead()
	consumer := before.MustRelation(before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "retained_provider_local"}))
	address := before.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: owner.TemporaryNamespace, Name: "retained_provider"})
	provider := before.MustCollation(address)
	capability, err := provider.Usage()
	if err != nil {
		t.Fatal(err)
	}
	ref := e.Catalog.Domain.Ref(before.Epoch(), address)
	expiredAddress := before.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: owner.TemporaryNamespace, Name: "expired_provider"})
	expiredCapability, err := before.MustCollation(expiredAddress).Usage()
	if err != nil {
		t.Fatal(err)
	}
	expiredRef := e.Catalog.Domain.Ref(before.Epoch(), expiredAddress)
	enginetest.SessionSQL(t, owner, `DROP COLLATION pg_temp.expired_provider`)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	if view.Epoch() == before.Epoch() || view.Get(consumer.Address()) != consumer {
		t.Fatal("fixture did not retain exact TEMP descriptor across replacement")
	}
	if !stored.CollationReferenceValid(view, consumer, ref, capability) {
		t.Fatal("lawful physical-owner retained provider rejected")
	}
	seedAddress := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 100}
	seedCapability, err := view.MustCollation(seedAddress).Usage()
	if err != nil {
		t.Fatal(err)
	}
	seedRef := e.Catalog.Domain.Ref(before.Epoch(), seedAddress)
	if !stored.CollationReferenceValid(view, consumer, seedRef, seedCapability) {
		t.Fatal("lawful retained actual pinned seed rejected")
	}
	foreignAddress := before.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: peer.TemporaryNamespace, Name: "foreign_provider"})
	foreignCapability, err := before.MustCollation(foreignAddress).Usage()
	if err != nil {
		t.Fatal(err)
	}
	incompatible, err := sqltype.RegisteredCollation(uint32(address.ObjectID), 950)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, owner, `CREATE COLLATION permanent_provider FROM pg_catalog.pg_unicode_fast`)
	enginetest.SessionSQL(t, owner, `CREATE TABLE permanent_consumer(v INT)`)
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
		{"foreign engine", consumer, engine.New().Catalog.Domain.Ref(ref.Epoch, address), capability},
		{"zero epoch", consumer, e.Catalog.Domain.Ref(0, address), capability},
		{"future epoch", consumer, e.Catalog.Domain.Ref(view.Epoch()+1, address), capability},
		{"missing identity", consumer, e.Catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: address.ObjectID + 100000}), capability},
		{"wrong class", consumer, e.Catalog.Domain.Ref(ref.Epoch, consumer.Address()), capability},
		{"wrong subid", consumer, e.Catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: address.ObjectID, SubID: 1}), capability},
		{"other physical owner", consumer, e.Catalog.Domain.Ref(ref.Epoch, foreignAddress), foreignCapability},
		{"persistent consumer", permanent, ref, capability},
		{"persistent dynamic target", consumer, e.Catalog.Domain.Ref(ref.Epoch, permanentAddress), permanentCapability},
		{"incompatible capability", consumer, ref, incompatible},
		{"invalid capability", consumer, ref, sqltype.CollationRef{ID: uint32(address.ObjectID)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if stored.CollationReferenceValid(view, c.consumer, c.ref, c.capability) {
				t.Fatal("accepted invalid retained collation authority")
			}
		})
	}
	if stored.CollationReferenceValid(view, consumer, expiredRef, expiredCapability) {
		t.Fatal("accepted genuinely dropped retained provider")
	}
	detached := *consumer
	if stored.CollationReferenceValid(view, &detached, ref, capability) {
		t.Fatal("accepted detached consumer instead of actual live descriptor")
	}
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	invalidSeed := *view.MustCollation(seedAddress)
	invalidSeed.Semantics = 811
	if err := stage.Replace(&invalidSeed); err != nil {
		t.Fatal(err)
	}
	if stored.CollationReferenceValid(e.StageRead(stage), consumer, seedRef, seedCapability) {
		t.Fatal("accepted candidate with incompatible actual seed provider invariant")
	}
	// Render genuine retained trees through the complete serializer. Compare
	// complete ObjectRefs, including epochs, rather than addresses or one helper.
	assertManifest := func(name string, renderer stored.ScalarRenderer, expression expr.Expr, want []ids.ObjectRef) {
		t.Helper()
		var got []ids.ObjectRef
		renderer.Dependencies = &got
		if _, err := renderer.SQL(context.Background(), view, expression); err != nil {
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
		assertManifest("DEFAULT", stored.ScalarRenderer{Provenance: consumer, CapturedManifest: definition.Dependencies}, definition.Expression, definition.Dependencies)
	}
	for _, definition := range consumer.ColumnMeta.Generations {
		assertManifest("generated", stored.ScalarRenderer{Owner: consumer, CapturedManifest: definition.Dependencies}, definition.Expression, definition.Dependencies)
	}
	for _, definition := range consumer.Constraints.Check {
		want := make([]ids.ObjectRef, len(definition.Dependencies))
		for i, dependency := range definition.Dependencies {
			want[i] = dependency.Ref
		}
		assertManifest("CHECK", stored.ScalarRenderer{Owner: consumer, CapturedChecks: definition.Dependencies}, definition.Expression, want)
	}
	// All ordinary validators must accept the retained closed expression trees.
	enginetest.SessionSQL(t, owner, `INSERT INTO retained_provider_local DEFAULT VALUES`)
	enginetest.SessionSQL(t, owner, `UPDATE retained_provider_local SET v='é'`)
}
