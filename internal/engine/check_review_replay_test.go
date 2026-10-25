package engine

import (
	"bytes"
	"context"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestCheckReviewReplayHistoryFailurePreservesDestination(t *testing.T) {
	ctx := context.Background()
	source := New()
	s := sessionStart(t, source, nil)
	sessionSQL(t, s, "CREATE SEQUENCE s START 1")
	sessionSQL(t, s, "CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))")
	sequenceScalar(t, s, "SELECT nextval('s')", 1)
	sessionSQL(t, s, "INSERT INTO t VALUES(9)")
	sequenceScalar(t, s, "SELECT currval('s')", 2)
	var dump bytes.Buffer
	if err := Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	sequenceScalar(t, s, "SELECT currval('s')", 2)
	// Global state, rather than only session currval, proves Dump did not Eval.
	sequenceScalar(t, s, "SELECT nextval('s')", 3)
	transactionRows(t, s, "SELECT a FROM t", 9)
	target := New()
	peer := sessionStart(t, target, nil)
	sessionSQL(t, peer, "CREATE TABLE sentinel(a INT)")
	sessionSQL(t, peer, "INSERT INTO sentinel VALUES(7)")
	before := target.CatalogRead()
	ref := catalogRef(t, target, before, "sentinel")
	storage := target.Storage.Relations[ref.Address]
	stage := target.BeginCatalogStage(before)
	defer target.DiscardCatalogStage(stage)
	err := Restore(ctx, target, bytes.NewReader(dump.Bytes()))
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "c"`, Detail: "Failing row contains (9).", Schema: "public", Table: "t", Constraint: "c"})
	after := target.CatalogRead()
	if after.State() != before.State() || after.Epoch() != before.Epoch() || target.Storage.Relations[ref.Address] != storage {
		t.Fatal("failed volatile replay replaced destination")
	}
	if err := target.ValidateObjectRef(after, ref); err != nil {
		t.Fatal("old reference invalidated", err)
	}
	if _, err := target.ValidateCatalogStage(ctx, stage); err != nil {
		t.Fatal("retained stage invalidated", err)
	}
	transactionRows(t, peer, "SELECT a FROM sentinel", 7)
}
