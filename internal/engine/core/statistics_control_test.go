package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestStatisticsObjectControlRemap(t *testing.T) {
	source, relation := statisticsUnitEngine(t)
	op, done := enginetest.RoleOperation(t, source, "postgres")
	stage, err := statisticsUnitStage(t, source, op, statisticsUnitCreate())
	if err != nil {
		done()
		t.Fatal(err)
	}
	definition := statisticsUnitDescriptor(t, stage)
	extensionUnitPublish(t, source, op, stage)
	source.DiscardCatalogStage(stage)
	done()
	captured := source.CatalogRead()
	var text bytes.Buffer
	if err := engine.Dump(t.Context(), source, &text); err != nil {
		t.Fatal(err)
	}
	if text.Len() == 0 || !strings.Contains(text.String(), "CREATE STATISTICS") {
		t.Fatal("missing definition dump")
	}
	target, _ := statisticsUnitEngine(t)
	before := target.CatalogRead()
	if err := engine.Restore(t.Context(), target, bytes.NewReader(text.Bytes())); err != nil {
		t.Fatal(err)
	}
	current := target.CatalogRead()
	address := current.NameAddress(catalog.DescriptorNameKey(definition))
	restored, ok := current.Statistics(address)
	if !ok || restored == definition || restored.Selection != definition.Selection || restored.Relation.Domain() != current.Domain() || restored.Relation.Epoch != current.Epoch() || restored.Owner != target.RoleStore.Domain.Principal(definition.Owner.ID()) {
		t.Fatal("definition remap lost provenance", restored)
	}
	input, ok := current.Relation(restored.Relation.Address)
	if !ok || input.Name() != relation.Name() || input == relation || current.Domain() == captured.Domain() || current.Epoch() == before.Epoch() {
		t.Fatal("input relation identity not remapped", input)
	}
	if err := (catalog.StatisticsProvider{}).Validate(current, restored); err != nil {
		t.Fatal(err)
	}
	if err := (catalog.StatisticsProvider{}).Validate(captured, definition); err != nil {
		t.Fatal("original snapshot lost", err)
	}
	var again bytes.Buffer
	if err := engine.Dump(t.Context(), target, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(text.Bytes(), again.Bytes()) {
		t.Fatal("remapped dump changed", text.String(), again.String())
	}
}

func TestStatisticsObjectControlOrdinaryOwnership(t *testing.T) {
	e, _ := statisticsUnitEngine(t)
	aclUnitRoles(e, "statistics_guard_owner")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	stage, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
	if err != nil {
		done()
		t.Fatal(err)
	}
	original := statisticsUnitDescriptor(t, stage)
	extensionUnitPublish(t, e, op, stage)
	e.DiscardCatalogStage(stage)
	owner := e.Roles().LookupRef("statistics_guard_owner")
	e.Mu.Lock()
	stage = e.BeginCatalogStage(e.Catalog.Current())
	owned := *original
	owned.Owner = owner
	owned.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err == nil {
		err = stage.Replace(&owned)
	}
	e.Mu.Unlock()
	if err != nil {
		done()
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	e.DiscardCatalogStage(stage)
	done()
	var text bytes.Buffer
	if err := engine.Dump(t.Context(), e, &text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "ALTER STATISTICS") {
		t.Fatal("owner transfer not emitted")
	}
	before := e.CatalogRead()
	valid := strings.Replace(text.String(), "ALTER STATISTICS", "SET SESSION AUTHORIZATION DEFAULT;\nALTER STATISTICS", 1)
	if err := engine.Restore(t.Context(), e, strings.NewReader(valid)); err != nil {
		t.Fatal("ordinary ownership rejected an intervening directive", err)
	}
	current := e.CatalogRead()
	restored, ok := current.Statistics(current.NameAddress(catalog.DescriptorNameKey(original)))
	if !ok || restored.Owner != owner || restored.Selection != original.Selection ||
		restored.Relation.Domain() != current.Domain() || restored.Relation.Epoch != current.Epoch() ||
		restored == &owned || current.Epoch() == before.Epoch() {
		t.Fatal("ordinary owner replay lost definition/principal remapping", restored)
	}
	if err := (catalog.StatisticsProvider{}).Validate(current, restored); err != nil {
		t.Fatal(err)
	}
	if before.Get(original.Address()) != &owned || owned.Owner != owner {
		t.Fatal("successful replay mutated the retained source descriptor")
	}
	var again bytes.Buffer
	if err := engine.Dump(t.Context(), e, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(text.Bytes(), again.Bytes()) {
		t.Fatal("ordinary ownership changed canonical redump")
	}
	malformed := strings.Replace(text.String(), `OWNER TO "statistics_guard_owner"`, `OWNER TO "unknown_statistics_owner"`, 1)
	err = engine.Restore(t.Context(), e, strings.NewReader(malformed))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != `role "unknown_statistics_owner" does not exist` {
		t.Fatal("missing ordinary recipient diagnostic", err)
	}
	if e.CatalogRead().State() != current.State() {
		t.Fatal("missing recipient replaced destination root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Restore(ctx, e, bytes.NewReader(text.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != current.State() {
		t.Fatal("canceled replay replaced root")
	}
	for _, d := range before.Descriptors() {
		if s, ok := d.(*catalog.Statistics); ok && (s.Address().ClassID != ids.StatisticExtClassID || s.Selection.Len() != 2) {
			t.Fatal("original definition corrupted")
		}
	}
}
