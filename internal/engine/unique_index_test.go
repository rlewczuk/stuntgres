package engine

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestUniqueIndexProviderHashAgreement(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	ctx := context.Background()
	for _, c := range []struct {
		typ                  sqltype.ID
		class, first, second string
	}{
		{sqltype.Text, "text_ops", "a", "a"}, {sqltype.Text, "varchar_ops", "a", "a"},
		{sqltype.Text, "text_pattern_ops", "a", "a"}, {sqltype.Text, "varchar_pattern_ops", "a", "a"},
		{sqltype.VarChar, "text_ops", "a", "a"}, {sqltype.VarChar, "varchar_ops", "a", "a"},
		{sqltype.VarChar, "text_pattern_ops", "a", "a"}, {sqltype.VarChar, "varchar_pattern_ops", "a", "a"},
		{sqltype.BPChar, "bpchar_ops", "a", "a "}, {sqltype.BPChar, "bpchar_pattern_ops", "a", "a "},
		{sqltype.Int4, "oid_ops", "-1", "-1"}, {sqltype.Oid, "oid_ops", "42", "42"}, {sqltype.Regclass, "oid_ops", "42", "42"},
		{sqltype.Numeric, "numeric_ops", "1", "1.00"}, {sqltype.Float8, "float8_ops", "0", "-0"},
	} {
		t.Run(fmt.Sprintf("%s/%d", c.class, c.typ), func(t *testing.T) {
			typ, _ := view.LookupType(c.typ)
			class, _ := sqltype.LookupIndexClass(c.class)
			parent := &relationDescriptor{attributes: []int32{1}, columns: []ColumnDescriptor{{Usage: typ.Usage()}}}
			definition := &indexDefinition{keys: []indexKey{{attribute: 1, class: class}}, unique: true}
			if err := validateUniqueProviders(parent, definition, nil); err != nil {
				t.Fatal(err)
			}
			a, err := typ.Input(ctx, c.first, -1)
			if err != nil {
				t.Fatal(err)
			}
			b, err := typ.Input(ctx, c.second, -1)
			if err != nil {
				t.Fatal(err)
			}
			ordering, err := bindIndexOrdering(parent, definition)
			if err != nil {
				t.Fatal(err)
			}
			cmp, err := ordering.compareKeys(ctx, logicalIndexEntry{keys: []sqltype.Value{a}}, logicalIndexEntry{keys: []sqltype.Value{b}})
			if err != nil || cmp != 0 {
				t.Fatal(cmp, err)
			}
			group, err := sqltype.BindGrouping(indexKeyCollation(typ.Usage(), definition.keys[0]))
			if err != nil {
				t.Fatal(err)
			}
			ha, err := group.Hash(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			hb, err := group.Hash(ctx, b)
			if err != nil || ha != hb {
				t.Fatal("equal keys split normalized claim buckets", ha, hb, err)
			}
		})
	}
	for _, id := range []sqltype.ID{sqltype.Text, sqltype.VarChar} {
		typ, _ := view.LookupType(id)
		for _, name := range []string{"bpchar_ops", "bpchar_pattern_ops"} {
			class, _ := sqltype.LookupIndexClass(name)
			parent := &relationDescriptor{attributes: []int32{1}, columns: []ColumnDescriptor{{Usage: typ.Usage()}}}
			if err := validateUniqueProviders(parent, &indexDefinition{keys: []indexKey{{attribute: 1, class: class}}, unique: true}, nil); err == nil {
				t.Fatal("inconsistent hash provider accepted", id, name)
			}
		}
	}
}

func TestUniqueIndexUnattachedLifecycleAndBinding(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE ui_t(a INT,b TEXT)")
	ref := roleTableRef(t, e, "ui_t")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	// Enter real statement work without taking a lock on the parent, then keep
	// this scope to prove catalog membership invalidates a cached binding.
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	first, err := scope.bindUniqueWrite(ref)
	if err != nil || len(first.keys) != 0 {
		t.Fatal(first, err)
	}
	parent := first.parent
	// Use a separate physical owner, not recursive admission on this scope.
	peer, peerDone := roleOperation(t, e, "postgres")
	defer peerDone()
	roleExecute(t, e, peer, "CREATE UNIQUE INDEX ui_ab ON ui_t(a,b) NULLS NOT DISTINCT")
	second, err := scope.bindUniqueWrite(ref)
	if err != nil || second == first || second.parent != parent || len(second.keys) != 1 {
		t.Fatal("parent-only binding missed added index", err)
	}
	index, root := btreeRoot(t, e, "ui_ab")
	if !index.sqlIndex.unique || index.sqlIndex.constraint != nil || len(parent.uniqueConstraints) != 0 {
		t.Fatal("fake attachment")
	}
	if err := validateIndexDefinition(e.CatalogRead(), index); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, peer, "ALTER INDEX ui_ab RENAME TO ui_renamed")
	renamed, same := btreeRoot(t, e, "ui_renamed")
	if renamed.address != index.address || same != root || index.name != "ui_ab" {
		t.Fatal("standalone rename changed identity/root")
	}
	roleExecute(t, e, peer, "DROP INDEX ui_renamed")
	third, err := scope.bindUniqueWrite(ref)
	if err != nil || len(third.keys) != 0 {
		t.Fatal("removed index retained enforcement", err)
	}
	if !reflect.DeepEqual(parent.uniqueConstraints, third.parent.uniqueConstraints) {
		t.Fatal("standalone lifecycle mutated constraints")
	}
}
