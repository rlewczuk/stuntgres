package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestIndexMaintenanceMetadataDecorated(t *testing.T) {
	e := New()
	for _, q := range []string{
		"CREATE TABLE t(unused INT,n INT,s TEXT,p TEXT,j JSON)",
		`INSERT INTO t VALUES(0,-1,'a ','tail','{"k":1}'),(0,2,'a',NULL,NULL),(0,2,'a',NULL,NULL),(0,NULL,NULL,NULL,NULL)`,
		`CREATE INDEX decorated ON t(n oid_ops DESC NULLS LAST,s COLLATE "C" bpchar_ops NULLS FIRST) INCLUDE(p,p,j)`,
		"CREATE INDEX plain ON t(n)", "ALTER TABLE t DROP COLUMN unused",
	} {
		run(t, e, q)
	}
	capture := e.CatalogRead()
	index, oldRoot := btreeRoot(t, e, "decorated")
	plain, oldSibling := btreeRoot(t, e, "plain")
	got, names := indexMaintenanceMetadata(t, e, capture, "decorated")
	want := indexMaintenanceMetadataFlags(index.address.ObjectID, index.sqlIndex.parent.ObjectID, 2)
	want.IndNatts = 5
	want.IndKey = []int16{2, 3, 4, 4, 5}
	want.IndClass = []ids.OID{1981, 10004}
	want.IndCollation = []ids.OID{0, 950}
	want.IndOption = []int16{1, 2}
	if !reflect.DeepEqual(got, want) || names.IndexDef != `CREATE INDEX decorated ON public.t USING btree (n oid_ops DESC NULLS LAST, s COLLATE "C" bpchar_ops NULLS FIRST) INCLUDE (p, p, j)` {
		t.Fatal("actual classes, key-only vectors or include attributes lost", got, names, want)
	}
	tuples := [][]string{{"-1", "a "}, {"2", "a"}, {"2", "a"}, {"NULL", "NULL"}}
	expressionWantRoot(t, e, "decorated", tuples)
	originalEntries := append([]logicalIndexEntry(nil), oldRoot.entries...)
	originalTuples := expressionRootTuples(t, oldRoot)
	run(t, e, "ALTER INDEX decorated RENAME TO renamed")
	if e.indexRoots[index.address] != oldRoot || e.indexRoots[plain.address] != oldSibling {
		t.Fatal("decorated rename rebuilt substantive roots")
	}
	run(t, e, "REINDEX INDEX renamed")
	if e.indexRoots[index.address] == oldRoot || e.indexRoots[plain.address] != oldSibling {
		t.Fatal("selected decorated rebuild affected a sibling or did no real work")
	}
	expressionWantRoot(t, e, "renamed", tuples)
	selected := e.indexRoots[index.address]
	run(t, e, "REINDEX TABLE t")
	if e.indexRoots[index.address] == selected || e.indexRoots[plain.address] == oldSibling {
		t.Fatal("TABLE omitted an implemented root")
	}
	if !reflect.DeepEqual(oldRoot.entries, originalEntries) || !reflect.DeepEqual(expressionRootTuples(t, oldRoot), originalTuples) {
		t.Fatal("retained predecessor root mutated")
	}
	run(t, e, "ALTER TABLE t RENAME COLUMN p TO payload")
	current, currentNames := indexMaintenanceMetadata(t, e, e.CatalogRead(), "renamed")
	if !reflect.DeepEqual(current, want) || currentNames.IndexDef != `CREATE INDEX renamed ON public.t USING btree (n oid_ops DESC NULLS LAST, s COLLATE "C" bpchar_ops NULLS FIRST) INCLUDE (payload, payload, j)` {
		t.Fatal("current include names/stable vector lost", current, currentNames)
	}
	old, oldNames, err := projectIndexMetadata(context.Background(), capture, e.domain.Ref(capture.Epoch(), index.address))
	if err != nil || !reflect.DeepEqual(old, want) || oldNames != names {
		t.Fatal("old decorated capture consulted current names", old, oldNames, err)
	}
	got.IndKey[2] = 0
	detached, _ := indexMaintenanceMetadata(t, e, e.CatalogRead(), "renamed")
	if !reflect.DeepEqual(detached, want) || index.sqlIndex.include[0] != 4 {
		t.Fatal("include vector aliases immutable definition")
	}
	run(t, e, "UPDATE t SET payload='changed' WHERE n=2")
	run(t, e, "REINDEX INDEX renamed")
	expressionWantRoot(t, e, "renamed", tuples)
	run(t, e, "DROP INDEX renamed,renamed")
	if e.indexRoots[index.address] != nil || e.indexRoots[plain.address] == nil {
		t.Fatal("decorated DROP removed a sibling or retained selected root")
	}
}

func TestIndexMaintenanceKeyEquivalenceDecorations(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT,s TEXT,p TEXT,j JSON)", `CREATE INDEX ix ON t(n oid_ops,s COLLATE "C" bpchar_ops) INCLUDE(p,j)`, "CREATE INDEX plain ON t(n)"} {
		run(t, e, q)
	}
	index, _ := btreeRoot(t, e, "ix")
	for _, change := range []string{"include_order", "include_repeat", "include_removed", "class", "collation"} {
		t.Run(change, func(t *testing.T) {
			next := *index
			definition := *index.sqlIndex
			next.sqlIndex = &definition
			definition.keys = append([]indexKey(nil), definition.keys...)
			definition.include = append([]int32(nil), definition.include...)
			switch change {
			case "include_order":
				definition.include[0], definition.include[1] = definition.include[1], definition.include[0]
			case "include_repeat":
				definition.include[1] = definition.include[0]
			case "include_removed":
				definition.include = definition.include[:1]
			case "class":
				other, _ := btreeRoot(t, e, "plain")
				definition.keys[0].class = other.sqlIndex.keys[0].class
			case "collation":
				definition.keys[1].collation = 951
			}
			if sameIndexKeySemantics(index, &next) {
				t.Fatal("changed actual decoration treated as name-only metadata", change)
			}
		})
	}
	next := *index
	next.name = "renamed"
	if !sameIndexKeySemantics(index, &next) {
		t.Fatal("name-only metadata invalidated real key semantics")
	}
}

func TestIndexMaintenanceMetadataAliasAndPatternClasses(t *testing.T) {
	e := New()
	for _, q := range []string{
		"CREATE TABLE t(unused INT,n INT,s TEXT,v VARCHAR(8),b CHAR(8),p TEXT)",
		`CREATE INDEX mixed ON t(lower(s) COLLATE "POSIX" text_pattern_ops,v varchar_ops,n int4_ops DESC NULLS FIRST) INCLUDE(p,p)`,
		`CREATE INDEX patterns ON t(v COLLATE "C" varchar_pattern_ops,b COLLATE "POSIX" bpchar_pattern_ops)`,
		"ALTER TABLE t DROP COLUMN unused",
	} {
		run(t, e, q)
	}
	// Independent PG18 observations: varchar_ops10044 is not text_ops3126;
	// each pattern class has its own released identity, not the base default.
	for _, phase := range []string{"initial", "after_rebuild"} {
		if phase == "after_rebuild" {
			run(t, e, "REINDEX TABLE t")
		}
		for _, c := range []struct {
			name                string
			natts, nkeys        int16
			attrs               []int16
			classes, collations []ids.OID
			options             []int16
			definition          string
		}{
			{"mixed", 5, 3, []int16{0, 4, 2, 6, 6}, []ids.OID{4217, 10044, 1978}, []ids.OID{951, 100, 0}, []int16{0, 0, 3}, `CREATE INDEX mixed ON public.t USING btree (lower(s) COLLATE "POSIX" text_pattern_ops, v varchar_ops, n DESC) INCLUDE (p, p)`},
			{"patterns", 2, 2, []int16{4, 5}, []ids.OID{4218, 4219}, []ids.OID{950, 951}, []int16{0, 0}, `CREATE INDEX patterns ON public.t USING btree (v COLLATE "C" varchar_pattern_ops, b COLLATE "POSIX" bpchar_pattern_ops)`},
		} {
			t.Run(phase+"/"+c.name, func(t *testing.T) {
				index, _ := btreeRoot(t, e, c.name)
				want := indexMaintenanceMetadataFlags(index.address.ObjectID, index.sqlIndex.parent.ObjectID, c.nkeys)
				want.IndNatts = c.natts
				want.IndKey = c.attrs
				want.IndClass = c.classes
				want.IndCollation = c.collations
				want.IndOption = c.options
				if c.name == "mixed" {
					want.IndexExpressions = []BoundExpr{index.sqlIndex.keys[0].derived.definition}
				}
				got, names := indexMaintenanceMetadata(t, e, e.CatalogRead(), c.name)
				if !reflect.DeepEqual(got, want) || names.IndexDef != c.definition || names.Tablespace != nil {
					t.Fatal("actual class/include vectors or original expressions changed", got, names, want)
				}
			})
		}
	}
}
