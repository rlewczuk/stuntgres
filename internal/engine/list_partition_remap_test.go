package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestListPartitionLogicalRemap(t *testing.T) {
	source := New()
	op, done := roleOperation(t, source, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TYPE list_color AS ENUM('red','green','blue')",
		"CREATE DOMAIN list_id AS BIGINT",
		`CREATE COLLATION list_coll FROM pg_catalog."C"`,
		"CREATE TABLE list_target(k INT)",
		"CREATE FUNCTION list_fn(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
		"CREATE TABLE list_enum(k list_color) PARTITION BY LIST(k)",
		"CREATE TABLE list_enum_leaf PARTITION OF list_enum FOR VALUES IN('green',NULL)",
		"CREATE TABLE list_domain(k list_id) PARTITION BY LIST(k)",
		"CREATE TABLE list_domain_leaf PARTITION OF list_domain FOR VALUES IN(9007199254740993,NULL)",
		"CREATE TABLE list_text(k TEXT) PARTITION BY LIST(k COLLATE list_coll)",
		"CREATE TABLE list_text_leaf PARTITION OF list_text FOR VALUES IN('a',NULL)",
		"CREATE TABLE list_class(k REGCLASS) PARTITION BY LIST(k)",
		"CREATE TABLE list_class_leaf PARTITION OF list_class FOR VALUES IN('list_target'::REGCLASS,NULL)",
		"CREATE TABLE list_type(k REGTYPE) PARTITION BY LIST(k)",
		"CREATE TABLE list_type_leaf PARTITION OF list_type FOR VALUES IN('list_color'::REGTYPE,NULL)",
		"CREATE TABLE list_routine(k REGPROCEDURE) PARTITION BY LIST(k)",
		"CREATE TABLE list_routine_leaf PARTITION OF list_routine FOR VALUES IN('list_fn(integer)'::REGPROCEDURE,NULL)",
	} {
		roleExecute(t, source, op, query)
	}
	retained := source.CatalogRead()
	oldValues := make(map[string][]catalog.PartitionDatum)
	for _, name := range []string{"enum", "domain", "text", "class", "type", "routine"} {
		leaf := rangePartitionRelation(t, source, "list_"+name+"_leaf")
		oldValues[name] = slices.Clone(leaf.Membership.Values)
	}
	// LIST values must participate in the existing scalar-generation detector,
	// even when this leaf has no partition-key declaration of its own.
	leaf := rangePartitionRelation(t, source, "list_domain_leaf")
	var replacement *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			replacement = typ
			break
		}
	}
	if !stored.ExpressionDescriptorsChanged(retained, leaf, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == leaf.Membership.Values[0].Value.Type() {
			return replacement
		}
		return typ
	}) {
		t.Fatal("LIST leaf datum type change was invisible")
	}
	target := New()
	targetOp, targetDone := roleOperation(t, target, "postgres")
	defer targetDone()
	for _, query := range []string{"CREATE TYPE allocation_history AS ENUM('before','after')", "CREATE TABLE history(k allocation_history)"} {
		roleExecute(t, target, targetOp, query)
	}
	// Permanent allocator history alone permits immutable source codec sharing.
	// Pin destination builtin generations through a live TEMP consumer, forcing
	// imported LIST keys, their dependent domain and every datum to be reowned.
	roleExecute(t, target, targetOp, "CREATE TEMP TABLE list_remap_retained(k BIGINT,t TEXT,c REGCLASS,y REGTYPE,r REGPROCEDURE)")
	roleExecute(t, target, targetOp, "INSERT INTO list_remap_retained VALUES(42,'retained','pg_class'::REGCLASS,'integer'::REGTYPE,'abs(integer)'::REGPROCEDURE)")
	tempNamespace := targetOp.Session.TemporaryNamespace
	tempNamespaceDescriptor := target.Catalog.State.Get(tempNamespace)
	tempAddress := target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: tempNamespace, Name: "list_remap_retained"})
	tempDescriptor := target.Catalog.State.Get(tempAddress)
	tempStore := target.Storage.Relations[tempAddress]
	tempVersions := slices.Clone(tempStore.Versions)
	before := target.CatalogRead()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := target.ReplaceCatalog(ctx, source, retained); !errors.Is(err, context.Canceled) || target.CatalogRead().State() != before.State() {
		t.Fatal("canceled remap published", err)
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, retained); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"enum", "domain", "text", "class", "type", "routine"} {
		root := rangePartitionRelation(t, target, "list_"+name)
		leaf := rangePartitionRelation(t, target, "list_"+name+"_leaf")
		if err := partition.ValidateDescriptor(target.CatalogRead(), root); err != nil {
			t.Fatal(name, err)
		}
		if err := partition.ValidateDescriptor(target.CatalogRead(), leaf); err != nil {
			t.Fatal(name, err)
		}
		key := root.Partition.Keys[0]
		ownedType, ok := target.CatalogRead().LookupType(key.Usage.Type.ID())
		if !ok || ownedType != key.Usage.Type {
			t.Fatal("imported key type is not the destination catalog authority", name)
		}
		if len(leaf.Membership.Values) != 2 || !leaf.Membership.Values[1].Value.IsNull() {
			t.Fatal("typed NULL lost", name)
		}
		for _, datum := range leaf.Membership.Values {
			if datum.Value.Type() != key.Usage.Type || datum.Value.Type() == oldValues[name][0].Value.Type() {
				t.Fatal("import retained source scalar ownership", name)
			}
		}
		if oldLeaf := rangePartitionRelation(t, source, "list_"+name+"_leaf"); !reflect.DeepEqual(oldLeaf.Membership.Values, oldValues[name]) {
			t.Fatal("remap mutated source membership", name)
		}
		// A destination write exercises the imported key comparator and NULL
		// ownership, not just a textual observation of the descriptor.
		roleExecute(t, target, targetOp, "INSERT INTO list_"+name+" VALUES(NULL)")
		if got := tuples(roleExecute(t, target, targetOp, "SELECT k FROM list_"+name+"_leaf")); !reflect.DeepEqual(got, [][]driver.Value{{nil}}) {
			t.Fatal(name, got)
		}
	}
	if source.CatalogRead().State() != retained.State() {
		t.Fatal("source catalog changed")
	}
	for _, c := range []struct{ name, value string }{{"enum", "'green'"}, {"domain", "9007199254740993"}, {"text", "'a'"}, {"class", "'list_target'::REGCLASS"}, {"type", "'list_color'::REGTYPE"}, {"routine", "'list_fn(integer)'::REGPROCEDURE"}} {
		roleExecute(t, target, targetOp, "INSERT INTO list_"+c.name+" VALUES("+c.value+")")
		if got := tuples(roleExecute(t, target, targetOp, "SELECT COUNT(*) FROM list_"+c.name+"_leaf")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
			t.Fatal("destination comparator lost NULL or nonnull routing", c.name, got)
		}
	}
	if targetOp.Session.TemporaryNamespace != tempNamespace || target.Catalog.State.Get(tempNamespace) != tempNamespaceDescriptor || target.Catalog.State.Get(tempAddress) != tempDescriptor || target.Storage.Relations[tempAddress] != tempStore || !reflect.DeepEqual(tempStore.Versions, tempVersions) {
		t.Fatal("LIST import changed exact retained TEMP descriptor/storage state")
	}
	if got := tuples(roleExecute(t, target, targetOp, "SELECT k,t FROM list_remap_retained")); !reflect.DeepEqual(got, [][]driver.Value{{int64(42), "retained"}}) {
		t.Fatal("LIST import changed retained TEMP values", got)
	}
}
