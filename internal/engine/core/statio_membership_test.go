package core_test

import (
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
)

func TestStatioAllIndexesMembership(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE statio_member(k integer PRIMARY KEY)",
		"CREATE INDEX statio_member_i ON statio_member USING hash(k)",
		"CREATE TABLE information_schema.statio_system(k integer)",
		"CREATE INDEX statio_system_i ON information_schema.statio_system(k)",
		"CREATE TABLE statio_partition(k integer) PARTITION BY RANGE(k)",
		"CREATE TABLE statio_leaf PARTITION OF statio_partition FOR VALUES FROM(0) TO(10)",
		"CREATE INDEX statio_leaf_i ON statio_leaf(k)",
		"CREATE MATERIALIZED VIEW statio_materialized AS SELECT k FROM statio_member",
		"CREATE INDEX statio_materialized_i ON statio_materialized(k)",
	} {
		enginetest.Run(t, e, q)
	}
	key := statioKey(t, e, "statio_member_i")
	var w statistics.Writer
	for range 3 {
		w.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, false)
	}
	for range 4 {
		w.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, true)
	}
	w.Flush(&e.Statistics, time.Now(), true)
	s := enginetest.StartSession(t, e, nil)
	op := enginetest.TransactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	read := auth.Catalog
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatioAllIndexesOID}
	source := read.MustRelation(address)
	if !pgcatalog.RegisteredSource(read, source) || !source.VirtualView || source.History.HasIndexes || source.RowType != 12202 {
		t.Fatal("typed readonly view", source)
	}
	for i, column := range source.Columns {
		collation := uint32(0)
		if i >= 2 && i <= 4 {
			collation = 950
		}
		if !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.OriginTable != uint32(address.ObjectID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("attribute metadata", i, column)
		}
	}
	// Exercise core's actual source policy, not a runtime with injected Auth:
	// the registered producer needs the admitted session's statistics Reader.
	e.Mu.Lock()
	captured, err := core.EngineOpenCatalogSourceLocked(e, scope, auth.Ref(address), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	rows := captured.Anonymous
	expected := make(map[ids.OID]*catalog.Relation)
	for _, descriptor := range read.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := read.Relation(index.SQLIndex.Parent)
		if !ok {
			continue
		}
		kind := pgcatalog.ClassKind(read, parent)
		if kind == "r" || kind == "t" || kind == "m" {
			expected[index.Address().ObjectID] = index
		}
	}
	all, user, system := 0, 0, 0
	seen := make(map[ids.OID]bool)
	for _, row := range rows {
		indexID, err := row[1].OID()
		if err != nil {
			t.Fatal(err)
		}
		index := expected[ids.OID(indexID)]
		if index == nil || seen[ids.OID(indexID)] {
			t.Fatal("invented/duplicate index", indexID)
		}
		seen[ids.OID(indexID)] = true
		parent := read.MustRelation(index.SQLIndex.Parent)
		parentID, _ := row[0].OID()
		schema, _ := row[2].Text()
		table, _ := row[3].Text()
		name, _ := row[4].Text()
		if parentID != uint32(parent.Address().ObjectID) || table != parent.Name() || name != index.Name() {
			t.Fatal("real descriptor identity", row)
		}
		isSystem := schema == "pg_catalog" || schema == "information_schema" || strings.HasPrefix(schema, "pg_toast")
		all++
		if isSystem {
			system++
		} else {
			user++
		}
		readCount, _ := row[5].Integer()
		hitCount, _ := row[6].Integer()
		if name == "statio_member_i" {
			if isSystem || readCount != 3 || hitCount != 4 {
				t.Fatal("user/all shared fact", row)
			}
		} else if readCount != 0 || hitCount != 0 {
			t.Fatal("missing activity not zero", row)
		}
	}
	if len(seen) != len(expected) || all != user+system || system != 1 {
		t.Fatal("disjoint represented union", len(seen), len(expected), all, user, system)
	}
	if a := read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "pg_statio_user_indexes"}); a.ObjectID != pgcatalog.StatioUserIndexesOID {
		t.Fatal("selected USER source missing", a)
	}
	// SYS remains owned by its separately scheduled part.
	for _, name := range []string{"pg_statio_sys_indexes"} {
		if a := read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: name}); a != (ids.ObjectAddress{}) {
			t.Fatal("out-of-scope sibling installed", name, a)
		}
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
}
