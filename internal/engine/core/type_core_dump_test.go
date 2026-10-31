package core_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestFloatFailedRestorePreservesEpochAndCatalog(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE f(r REAL,d DOUBLE PRECISION)")
	enginetest.Run(t, e, "INSERT INTO f VALUES('7.038531e-26','-0')")
	before := e.CatalogRead()
	var original bytes.Buffer
	if err := engine.Dump(context.Background(), e, &original); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"CREATE TABLE replacement(r REAL); INSERT INTO replacement VALUES('1'),('oops')", "CREATE TABLE replacement(d DOUBLE PRECISION); INSERT INTO replacement VALUES('1'),('1e999')"} {
		if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore")
		}
		if current := e.CatalogRead(); current.State() != before.State() || current.Epoch() != before.Epoch() {
			t.Fatal("failed float restore changed catalog generation")
		}
		var dump bytes.Buffer
		if err := engine.Dump(context.Background(), e, &dump); err != nil || dump.String() != original.String() {
			t.Fatal(dump.String(), err)
		}
	}
}
