package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

func TestPgBuffercachePublication(t *testing.T) {
	e := bufferUnitEngine(t)
	for _, q := range []string{"CREATE TABLE buffer_publication(k integer)", "INSERT INTO buffer_publication VALUES(1)"} {
		enginetest.Run(t, e, q)
	}
	owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	capture := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_publication")
	if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
		t.Fatal(err)
	}
	state := exec.State{Source: capture.source, Retain: true, SourceMode: exec.SourceBorrowed}
	stage, err := exec.OpenNode(capture.op.Ctx, &state, capture.root)
	if err != nil {
		t.Fatal(err)
	}
	oldLease := capture.source.ReadLease
	oldNode := bufferUnitNode(t, e, "buffer_publication")
	oldSnapshot := bufferUnitCursor(t, e, observer)
	defer oldSnapshot.Close()
	bufferUnitFact(t, e, observer, oldNode, 1, 1)
	stage.Close()
	capture.close(t)
	before := e.CatalogRead()
	bad := catalog.CloneState(before.State())
	installed := before.MustExtension(before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_buffercache"}))
	function := before.MustProcedure(installed.Members[0])
	bad.RemoveDescriptor(function.Address())
	if _, err := e.ReplaceCatalog(context.Background(), e, e.Catalog.Read(bad)); err == nil {
		t.Fatal("invalid provider replacement accepted")
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("failed replacement changed committed catalog root")
	}
	bufferUnitFact(t, e, observer, oldNode, 1, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ReplaceCatalog(ctx, e, before); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled replacement boundary", err)
	}
	bufferUnitFact(t, e, observer, oldNode, 1, 0)
	if _, err := e.ReplaceCatalog(context.Background(), e, before); err != nil {
		t.Fatal(err)
	}
	newNode := bufferUnitNode(t, e, "buffer_publication")
	bufferUnitFact(t, e, observer, newNode, 0, 0)
	fresh := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_publication")
	if err := exec.SelectQuerySources(fresh.op.Ctx, fresh.source); err != nil {
		t.Fatal(err)
	}
	freshState := exec.State{Source: fresh.source, Retain: true, SourceMode: exec.SourceBorrowed}
	freshStage, err := exec.OpenNode(fresh.op.Ctx, &freshState, fresh.root)
	if err != nil {
		t.Fatal(err)
	}
	defer freshStage.Close()
	bufferUnitFact(t, e, observer, newNode, 1, 1)
	// The old operational bridge is generation-bound. Neither late opening nor
	// late release can resurrect old occupancy or decrement the new owner.
	oldLease.Open()
	oldLease.Close()
	bufferUnitFact(t, e, observer, newNode, 1, 1)
	found := false
	for {
		row, present, err := oldSnapshot.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		if row.CompositeField(1).IsNull() {
			continue
		}
		node, err := row.CompositeField(1).OID()
		if err != nil {
			t.Fatal(err)
		}
		if node == uint32(oldNode) {
			pins, err := row.CompositeField(8).Integer()
			if err != nil || pins != 1 {
				t.Fatal("replacement mutated an old detached snapshot", pins, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("old snapshot lost its actual occupied identity")
	}
	freshStage.Close()
	fresh.close(t)
	enginetest.Run(t, e, "ALTER TABLE buffer_publication RENAME TO buffer_renamed")
	if got := bufferUnitNode(t, e, "buffer_renamed"); got != newNode {
		t.Fatal("presentation change reset storage incarnation", newNode, got)
	}
	bufferUnitFact(t, e, observer, newNode, 1, 0)
	enginetest.SessionSQL(t, owner, "BEGIN")
	enginetest.SessionSQL(t, owner, "TRUNCATE buffer_renamed")
	enginetest.SessionSQL(t, owner, "INSERT INTO buffer_renamed VALUES(1)")
	private := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_renamed")
	if private.source.ReadLease == nil {
		t.Fatal("rewritten source lost its independent statistics lease")
	}
	beforePrivate := bufferUnitFacts(t, e, observer)
	private.source.ReadLease.Open()
	afterPrivate := bufferUnitFacts(t, e, observer)
	if len(afterPrivate) != len(beforePrivate) {
		t.Fatal("private rewritten read changed shared cache occupancy", beforePrivate, afterPrivate)
	}
	for node, fact := range beforePrivate {
		if afterPrivate[node] != fact {
			t.Fatal("private rewritten read acquired shared cache usage or pins", beforePrivate, afterPrivate)
		}
	}
	private.close(t)
	enginetest.SessionSQL(t, owner, "ROLLBACK")
	bufferUnitFact(t, e, observer, newNode, 1, 0)
	enginetest.Run(t, e, "TRUNCATE buffer_renamed")
	if current := bufferUnitNode(t, e, "buffer_renamed"); current == newNode {
		t.Fatal("committed rewrite retained storage locator")
	}
	bufferUnitFact(t, e, observer, newNode, 0, 0)
}
