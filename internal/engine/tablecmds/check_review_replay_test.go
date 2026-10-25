package tablecmds_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestCheckReviewReplayHistoryFailurePreservesDestination(t *testing.T) {
	ctx := context.Background()
	source := engine.New()
	s := enginetest.StartSession(t, source, nil)
	enginetest.SessionSQL(t, s, "CREATE SEQUENCE s START 1")
	enginetest.SessionSQL(t, s, "CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))")
	sequenceScalar(t, s, "SELECT nextval('s')", 1)
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(9)")
	sequenceScalar(t, s, "SELECT currval('s')", 2)
	var dump bytes.Buffer
	if err := engine.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	sequenceScalar(t, s, "SELECT currval('s')", 2)
	// Global state, rather than only session currval, proves Dump did not Eval.
	sequenceScalar(t, s, "SELECT nextval('s')", 3)
	enginetest.TransactionRows(t, s, "SELECT a FROM t", 9)
	target := engine.New()
	peer := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE sentinel(a INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO sentinel VALUES(7)")
	before := target.CatalogRead()
	ref := enginetest.CatalogRef(t, target, before, "sentinel")
	storage := target.Storage.Relations[ref.Address]
	stage := target.BeginCatalogStage(before)
	defer target.DiscardCatalogStage(stage)
	err := engine.Restore(ctx, target, bytes.NewReader(dump.Bytes()))
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "c"`, Detail: "Failing row contains (9).", Schema: "public", Table: "t", Constraint: "c"})
	after := target.CatalogRead()
	if after.State() != before.State() || after.Epoch() != before.Epoch() || target.Storage.Relations[ref.Address] != storage {
		t.Fatal("failed volatile replay replaced destination")
	}
	if err := enginetest.ValidateObjectRef(target, after, ref); err != nil {
		t.Fatal("old reference invalidated", err)
	}
	if _, err := target.ValidateCatalogStage(ctx, stage); err != nil {
		t.Fatal("retained stage invalidated", err)
	}
	enginetest.TransactionRows(t, peer, "SELECT a FROM sentinel", 7)
}
