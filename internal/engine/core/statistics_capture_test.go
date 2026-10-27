package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestStatisticsObjectCaptureRoleGeneration(t *testing.T) {
	e, _ := statisticsUnitEngine(t)
	aclUnitRoles(e, "statistics_capture_owner")
	owner := e.Roles().LookupRef("statistics_capture_owner")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
	if err != nil {
		t.Fatal(err)
	}
	original := statisticsUnitDescriptor(t, stage)
	extensionUnitPublish(t, e, op, stage)
	e.DiscardCatalogStage(stage)
	e.Mu.Lock()
	stage = e.BeginCatalogStage(e.Catalog.Current())
	definition := *original
	definition.Owner = owner
	definition.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err == nil {
		err = stage.Replace(&definition)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	e.DiscardCatalogStage(stage)
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatisticExtOID}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing source")
	}
	input := pgcatalog.Input{Context: t.Context(), Catalog: view, Owner: view.Owner(), Ref: view.Domain().Ref(view.Epoch(), address), Source: source, Roles: e.RoleStore.PublishedRead(view.Owner(), view)}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatisticExtOID)
	if !ok {
		t.Fatal("missing producer")
	}
	retained, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || retained == nil || len(retained.Anonymous) != 1 {
		t.Fatal(retained, err)
	}
	e.Mu.Lock()
	roles := cloneRoleFixture(e.RoleStore.Published)
	role := fixtureRole(roles, owner)
	retireFixtureRole(roles, owner)
	role.Ref = e.RoleStore.Domain.Principal(owner.ID() + 100000)
	putFixtureRole(roles, role)
	e.RoleStore.PublishLocked(roles)
	e.Mu.Unlock()
	wrong := input
	wrong.Roles = e.RoleStore.PublishedRead(view.Owner(), view)
	if rows, err := produce(wrong); rows != nil || err == nil {
		t.Fatal("recreated owner authorized old definition", rows, err)
	}
	if rows, err := core.CaptureCatalogSourceRows(wrong, produce); rows != nil || err == nil {
		t.Fatal("mismatched capture published", rows, err)
	}
	rows, err := produce(input)
	if err != nil || len(rows) != 1 || rows[0][4] != retained.Anonymous[0][4] {
		t.Fatal("old immutable authority lost", rows, err)
	}
	foreign, _ := statisticsUnitEngine(t)
	wrong = input
	wrong.Roles = foreign.Roles()
	if rows, err := produce(wrong); rows != nil || err == nil {
		t.Fatal("foreign role generation accepted", rows, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	wrong = input
	wrong.Context = canceled
	if rows, err := produce(wrong); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture returned prefix", rows, err)
	}
	value, err := retained.Anonymous[0][4].Type().Output(t.Context(), retained.Anonymous[0][4])
	if err != nil || value == "0" {
		t.Fatal("retained owner metadata lost", value, err)
	}
	// The descriptor itself and its dependencies remain bound to the original role.
	if definition.Owner != owner || definition.Selection != original.Selection || definition.Relation != original.Relation {
		t.Fatal("capture rewrote definition")
	}
	if err = (catalog.StatisticsProvider{}).Validate(view, &definition); err != nil {
		t.Fatal(err)
	}
}
