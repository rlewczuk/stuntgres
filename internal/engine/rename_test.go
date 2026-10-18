package engine

import (
	"database/sql/driver"
	"reflect"
	"testing"
)

func TestAlterRenameIdentity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT,c BIGINT)")
	run(t, e, "INSERT INTO t VALUES(7,'keep',9)")
	run(t, e, "COMMENT ON COLUMN t.c IS 'survivor'")
	run(t, e, "ALTER TABLE t DROP b")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	storage := e.storage[ref.Address]
	backing := &storage.versions[0].values[0]
	held := run(t, e, "SELECT * FROM t")
	run(t, e, "ALTER TABLE t RENAME COLUMN c TO renamed")
	middle := e.catalog.state.MustRelation(ref.Address)
	run(t, e, "ALTER TABLE t RENAME TO newtable")
	next := e.catalog.state.MustRelation(ref.Address)
	if roleTableRef(t, e, "newtable") != ref || next.address != old.address || next.namespace != old.namespace || next.owner != old.owner || next.revision != old.revision+2 || middle.revision != old.revision+1 {
		t.Fatal("rename changed identity", old, middle, next)
	}
	if e.storage[ref.Address] != storage || &storage.versions[0].values[0] != backing || !reflect.DeepEqual(next.attributes, old.attributes) || !reflect.DeepEqual(next.notNullConstraints, old.notNullConstraints) || !reflect.DeepEqual(next.columnComments, old.columnComments) || !reflect.DeepEqual(next.columnACL, old.columnACL) || !reflect.DeepEqual(next.acl, old.acl) {
		t.Fatal("rename changed storage/dependencies", old, next)
	}
	if next.columns[1].Name != "renamed" || next.columns[1].OriginAttribute != 3 || next.columnComments[3] != "survivor" || old.columns[1].Name != "c" {
		t.Fatal("sparse identity or immutable descriptor lost", old, next)
	}
	if held.Rows.Schema()[1].Name != "c" || !reflect.DeepEqual(tuples(held), [][]driver.Value{{int64(7), int64(9)}}) {
		t.Fatal("held snapshot changed")
	}
	if !reflect.DeepEqual(tuples(run(t, e, "SELECT * FROM newtable")), [][]driver.Value{{int64(7), int64(9)}}) {
		t.Fatal("rows changed")
	}
}
