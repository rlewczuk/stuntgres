package tablecmds_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestDefaultFailedRestorePreservesEligibilityAndSurvivors(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE USER default_owner`)
	enginetest.Run(t, e, `CREATE TABLE sentinel(id INT,a INT DEFAULT 7,b INT DEFAULT 9)`)
	enginetest.Run(t, e, `INSERT INTO sentinel(id) VALUES(1)`)
	ref := enginetest.RoleTableRef(t, e, "sentinel")
	old := e.Catalog.State.MustRelation(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	epoch := e.CatalogRead().Epoch()
	owner := e.Catalog.Domain
	stage := e.BeginCatalogStage(e.CatalogRead())
	next := *old
	next.ObjectRevision++
	next.Comment = "eligible default descriptor"
	next.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("default_owner")
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	held := enginetest.Run(t, e, `SELECT * FROM sentinel`)
	var before bytes.Buffer
	if err := engine.Dump(context.Background(), e, &before); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{`CREATE TABLE bad(i INT DEFAULT 'oops')`, `CREATE TABLE bad(i INT DEFAULT $1)`, `CREATE TABLE bad(i INT DEFAULT i)`, `CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2)`, `CREATE TABLE bad(i INT DEFAULT NULL NOT NULL);INSERT INTO bad DEFAULT VALUES`} {
		if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted", script)
		}
		if e.Catalog.Domain != owner || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.CatalogRead().Epoch() != epoch {
			t.Fatal("failed restore changed identity/storage")
		}
		var after bytes.Buffer
		if err := engine.Dump(context.Background(), e, &after); err != nil || after.String() != before.String() {
			t.Fatal(err, after.String())
		}
	}
	if got := enginetest.Tuples(held); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(7), int64(9)}}) {
		t.Fatal("cursor changed", got)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: epoch}); err != nil {
		t.Fatal("eligible stage fenced", err)
	}
	enginetest.Run(t, e, `ALTER TABLE sentinel DROP a`)
	live := e.Catalog.State.MustRelation(ref.Address)
	if len(live.ColumnMeta.Defaults) != 1 || live.ColumnMeta.Defaults[3].Expression == nil || old.ColumnMeta.Defaults[2].Expression == nil {
		t.Fatal("attribute-key ownership changed", live.ColumnMeta.Defaults, old.ColumnMeta.Defaults)
	}
	enginetest.Run(t, e, `INSERT INTO sentinel(id) VALUES(2)`)
	if got := enginetest.Tuples(enginetest.Run(t, e, `SELECT * FROM sentinel ORDER BY id`)); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(9)}, {int64(2), int64(9)}}) {
		t.Fatal(got)
	}
}
