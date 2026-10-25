package tablecmds_test

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestAlterRenameIdentity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT,c BIGINT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(7,'keep',9)")
	enginetest.Run(t, e, "COMMENT ON COLUMN t.c IS 'survivor'")
	enginetest.Run(t, e, "ALTER TABLE t DROP b")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	backing := &storage.Versions[0].Values[0]
	held := enginetest.Run(t, e, "SELECT * FROM t")
	enginetest.Run(t, e, "ALTER TABLE t RENAME COLUMN c TO renamed")
	middle := e.Catalog.State.MustRelation(ref.Address)
	enginetest.Run(t, e, "ALTER TABLE t RENAME TO newtable")
	next := e.Catalog.State.MustRelation(ref.Address)
	if enginetest.RoleTableRef(t, e, "newtable") != ref || next.Address() != old.Address() || next.Namespace() != old.Namespace() || next.Owner != old.Owner || next.Revision() != old.Revision()+2 || middle.Revision() != old.Revision()+1 {
		t.Fatal("rename changed identity", old, middle, next)
	}
	if e.Storage.Relations[ref.Address] != storage || &storage.Versions[0].Values[0] != backing || !reflect.DeepEqual(next.Attributes, old.Attributes) || !reflect.DeepEqual(next.Constraints.NotNull, old.Constraints.NotNull) || !reflect.DeepEqual(next.ColumnMeta.Comments, old.ColumnMeta.Comments) || !reflect.DeepEqual(next.Security.ColumnACL, old.Security.ColumnACL) || !reflect.DeepEqual(next.Security.ACL, old.Security.ACL) {
		t.Fatal("rename changed storage/dependencies", old, next)
	}
	if next.Columns[1].Name != "renamed" || next.Columns[1].OriginAttribute != 3 || next.ColumnMeta.Comments[3] != "survivor" || old.Columns[1].Name != "c" {
		t.Fatal("sparse identity or immutable descriptor lost", old, next)
	}
	if held.Rows.Schema()[1].Name != "c" || !reflect.DeepEqual(enginetest.Tuples(held), [][]driver.Value{{int64(7), int64(9)}}) {
		t.Fatal("held snapshot changed")
	}
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM newtable")), [][]driver.Value{{int64(7), int64(9)}}) {
		t.Fatal("rows changed")
	}
}
