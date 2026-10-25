package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestDefaultFailedRestorePreservesEligibilityAndSurvivors(t *testing.T) {
	e := New()
	run(t, e, `CREATE USER default_owner`)
	run(t, e, `CREATE TABLE sentinel(id INT,a INT DEFAULT 7,b INT DEFAULT 9)`)
	run(t, e, `INSERT INTO sentinel(id) VALUES(1)`)
	ref := roleTableRef(t, e, "sentinel")
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
	held := run(t, e, `SELECT * FROM sentinel`)
	var before bytes.Buffer
	if err := e.Dump(context.Background(), &before); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{`CREATE TABLE bad(i INT DEFAULT 'oops')`, `CREATE TABLE bad(i INT DEFAULT $1)`, `CREATE TABLE bad(i INT DEFAULT i)`, `CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2)`, `CREATE TABLE bad(i INT DEFAULT NULL NOT NULL);INSERT INTO bad DEFAULT VALUES`} {
		if err := e.Restore(context.Background(), strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted", script)
		}
		if e.Catalog.Domain != owner || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.CatalogRead().Epoch() != epoch {
			t.Fatal("failed restore changed identity/storage")
		}
		var after bytes.Buffer
		if err := e.Dump(context.Background(), &after); err != nil || after.String() != before.String() {
			t.Fatal(err, after.String())
		}
	}
	if got := tuples(held); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(7), int64(9)}}) {
		t.Fatal("cursor changed", got)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: epoch}); err != nil {
		t.Fatal("eligible stage fenced", err)
	}
	run(t, e, `ALTER TABLE sentinel DROP a`)
	live := e.Catalog.State.MustRelation(ref.Address)
	if len(live.ColumnMeta.Defaults) != 1 || live.ColumnMeta.Defaults[3].Expression == nil || old.ColumnMeta.Defaults[2].Expression == nil {
		t.Fatal("attribute-key ownership changed", live.ColumnMeta.Defaults, old.ColumnMeta.Defaults)
	}
	run(t, e, `INSERT INTO sentinel(id) VALUES(2)`)
	if got := tuples(run(t, e, `SELECT * FROM sentinel ORDER BY id`)); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(9)}, {int64(2), int64(9)}}) {
		t.Fatal(got)
	}
}
