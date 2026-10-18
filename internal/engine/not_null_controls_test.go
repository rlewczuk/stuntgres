package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
)

func TestNotNullFailedRestorePreservesCatalogEligibility(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE sentinel(a INT CONSTRAINT nn NOT NULL)")
	run(t, e, "INSERT INTO sentinel VALUES(91)")
	ref := roleTableRef(t, e, "sentinel")
	old := e.catalog.state.Get(ref.Address)
	storage := e.storage[ref.Address]
	epoch := e.CatalogRead().Epoch()
	stage := e.BeginCatalogStage(e.CatalogRead())
	next := *old.(*relationDescriptor)
	next.ObjectRevision++
	next.Comment = "still eligible"
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	held := run(t, e, "SELECT * FROM sentinel")
	for _, script := range []string{"CREATE TABLE replacement(a INT NOT NULL);INSERT INTO replacement VALUES(NULL)", "CREATE TABLE replacement(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", "CREATE TABLE replacement(a INT,NOT NULL missing)", "CREATE TABLE replacement(a INT NOT NULL,b INT CONSTRAINT replacement_a_not_null NOT NULL)"} {
		var before bytes.Buffer
		if err := e.Dump(context.Background(), &before); err != nil {
			t.Fatal(err)
		}
		if err := e.Restore(context.Background(), strings.NewReader(script)); err == nil {
			t.Fatal("bad restore succeeded")
		}
		if e.catalog.state.Get(ref.Address) != old || e.storage[ref.Address] != storage || e.CatalogRead().Epoch() != epoch {
			t.Fatal("failed restore changed target")
		}
		var after bytes.Buffer
		if err := e.Dump(context.Background(), &after); err != nil || before.String() != after.String() {
			t.Fatal(err)
		}
	}
	if got := tuples(held); !reflect.DeepEqual(got, [][]driver.Value{{int64(91)}}) {
		t.Fatal(got)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	run(t, e, "ALTER TABLE sentinel DROP CONSTRAINT nn")
}
