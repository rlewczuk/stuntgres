package tablecmds_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestNotNullFailedRestorePreservesCatalogEligibility(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE sentinel(a INT CONSTRAINT nn NOT NULL)")
	enginetest.Run(t, e, "INSERT INTO sentinel VALUES(91)")
	ref := enginetest.RoleTableRef(t, e, "sentinel")
	old := e.Catalog.State.Get(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	epoch := e.CatalogRead().Epoch()
	stage := e.BeginCatalogStage(e.CatalogRead())
	next := *old.(*catalog.Relation)
	next.ObjectRevision++
	next.Comment = "still eligible"
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	held := enginetest.Run(t, e, "SELECT * FROM sentinel")
	for _, script := range []string{"CREATE TABLE replacement(a INT NOT NULL);INSERT INTO replacement VALUES(NULL)", "CREATE TABLE replacement(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", "CREATE TABLE replacement(a INT,NOT NULL missing)", "CREATE TABLE replacement(a INT NOT NULL,b INT CONSTRAINT replacement_a_not_null NOT NULL)"} {
		var before bytes.Buffer
		if err := engine.Dump(context.Background(), e, &before); err != nil {
			t.Fatal(err)
		}
		if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore succeeded")
		}
		if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.CatalogRead().Epoch() != epoch {
			t.Fatal("failed restore changed target")
		}
		var after bytes.Buffer
		if err := engine.Dump(context.Background(), e, &after); err != nil || before.String() != after.String() {
			t.Fatal(err)
		}
	}
	if got := enginetest.Tuples(held); !reflect.DeepEqual(got, [][]driver.Value{{int64(91)}}) {
		t.Fatal(got)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "ALTER TABLE sentinel DROP CONSTRAINT nn")
}
