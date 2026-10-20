package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestConversionHandlerEmptyProbePrecedence(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	create := &parser.CreateConversion{Name: conversionUnitName("public", "probe"), Source: "UTF8", Destination: "LATIN1", Function: conversionUnitName("pg_catalog", "utf8_to_iso8859_1")}
	view := auth.Catalog
	// Corrupt actual routine metadata in an unpublished candidate, preserving
	// its real six-argument identity/provider, to exercise return-type admission.
	candidate := catalog.CloneState(view.State())
	procedure := *candidate.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4375})
	procedure.Result = 25
	candidate.SetDescriptor(procedure.Address(), &procedure)
	auth.Recapture(e.catalog.read(candidate))
	stage := e.BeginCatalogStage(auth.Catalog)
	e.mu.Lock()
	err := e.stageCreateConversionLocked(op, auth, stage, create)
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P17" || diagnostic.Message != "encoding conversion function pg_catalog.utf8_to_iso8859_1 must return type integer" {
		t.Fatal("actual return contract", err)
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().State() != view.State() {
		t.Fatal("bad routine metadata published")
	}
	auth.Recapture(view)
	stage = e.BeginCatalogStage(view)
	if err = stage.Create(conversionUnitDefinition(e, 90000, "probe", true)); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	_, auth = conversionUnitAuth(t, e)
	create.Default, create.Destination = true, "LATIN2"
	stage = e.BeginCatalogStage(auth.Catalog)
	e.mu.Lock()
	err = e.stageCreateConversionLocked(op, auth, stage, create)
	e.mu.Unlock()
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != `expected destination encoding "LATIN1", but got "LATIN2"` {
		t.Fatal("real empty probe did not precede duplicate checks", err)
	}
	e.DiscardCatalogStage(stage)
	before := e.CatalogRead()
	ctx, cancel := context.WithCancel(context.Background())
	canceled, err := op.Session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer canceled.Done()
	cancel()
	create.Name, create.Default, create.Destination = conversionUnitName("public", "canceled_probe"), false, "LATIN1"
	stage = e.BeginCatalogStage(before)
	e.mu.Lock()
	err = e.stageCreateConversionLocked(canceled, auth, stage, create)
	e.mu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("real empty handler context entry", err)
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().State() != before.State() {
		t.Fatal("empty probe cancellation published catalog")
	}
}
