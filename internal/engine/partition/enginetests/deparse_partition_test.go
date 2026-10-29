package partition_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type deparsePartitionFixture struct {
	view          catalog.Read
	parent, child *catalog.Relation
	input         table.DeparseInputs
}

func newDeparsePartitionFixture(t *testing.T, strategy parser.PartitionStrategy, types ...sqltype.ID) *deparsePartitionFixture {
	t.Helper()
	view := engine.New().CatalogRead()
	view = view.WithState(catalog.CloneState(view.State()))
	f := &deparsePartitionFixture{view: view}
	f.parent = &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 190001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "deparse_parent", ObjectRevision: 1, RelationKind: catalog.PartitionedRelation, Index: map[string]int{}, Partition: &catalog.PartitionDefinition{Strategy: strategy}}
	for i, id := range types {
		typ, ok := view.LookupType(id)
		if !ok {
			t.Fatal("missing fixture type", id)
		}
		name := "k"
		if len(types) > 1 {
			name = string(rune('a' + i))
		}
		f.parent.Columns = append(f.parent.Columns, catalog.Column{Name: name, Usage: typ.Usage()})
		f.parent.Attributes = append(f.parent.Attributes, int32(7+i*2))
		f.parent.Index[name] = i
		method := ids.OID(403)
		if strategy == parser.PartitionHash {
			method = 405
		}
		class, ok := view.UniqueDefaultIndexClass(method, typ)
		if !ok {
			t.Fatal("missing fixture class", id)
		}
		key := catalog.PartitionKeyEntry{Attribute: f.parent.Attributes[i], Usage: typ.Usage(), Class: class}
		var err error
		if strategy == parser.PartitionHash {
			key.Hash, err = routine.BindPartitionHash(view, key.Usage, class)
		} else {
			key.Comparison, err = routine.BindIndexOrdering(view, key.Usage, class)
		}
		if err != nil {
			t.Fatal(err)
		}
		f.parent.Partition.Keys = append(f.parent.Partition.Keys, key)
	}
	f.child = &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 190002}, ObjectNamespace: f.parent.Namespace(), ObjectName: "deparse_child", ObjectRevision: 1, RelationKind: catalog.TableRelation, Columns: append([]catalog.Column(nil), f.parent.Columns...), Attributes: append([]int32(nil), f.parent.Attributes...), Index: map[string]int{}, Membership: &catalog.PartitionMembership{Parent: f.parent.Address(), Strategy: strategy}}
	for name, slot := range f.parent.Index {
		f.child.Index[name] = slot
	}
	f.parent.Partition.Children = []ids.ObjectAddress{f.child.Address()}
	f.put(f.parent, f.child)
	f.input = table.DeparseInputs{Catalog: view,
		ResolveRelation: func(parser.ObjectName) (ids.ObjectRef, error) {
			t.Fatal("partition OID inquiry resolved a text name")
			return ids.ObjectRef{}, nil
		},
		Temporary: func() ids.ObjectAddress {
			t.Fatal("partition inquiry read unrelated TEMP authority")
			return ids.ObjectAddress{}
		},
	}
	return f
}

func (f *deparsePartitionFixture) put(descriptors ...catalog.Descriptor) {
	for _, d := range descriptors {
		f.view.State().SetDescriptor(d.Address(), d)
		f.view.State().BindName(catalog.DescriptorNameKey(d), d.Address())
	}
}

func (f *deparsePartitionFixture) datum(t *testing.T, key int, text string) catalog.PartitionDatum {
	t.Helper()
	usage := f.parent.Partition.Keys[key].Usage
	value, err := usage.Type.Input(context.Background(), text, usage.Typmod)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.PartitionDatum{Value: value}
}

func (f *deparsePartitionFixture) call(ctx context.Context, signature functions.DeparseSignature, id ids.OID) (sqltype.Value, error) {
	oid, _ := f.view.LookupType(sqltype.Oid)
	text, _ := f.view.LookupType(sqltype.Text)
	value, err := oid.Input(context.Background(), strconv.FormatUint(uint64(id), 10), -1)
	if err != nil {
		return sqltype.Value{}, err
	}
	return partition.Deparse(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, f.input, signature, [3]sqltype.Value{value}, text)
}

func (f *deparsePartitionFixture) check(t *testing.T, signature functions.DeparseSignature, id ids.OID, want any) {
	t.Helper()
	value, err := f.call(context.Background(), signature, id)
	if err != nil {
		t.Fatal(err)
	}
	if value.Type().ID() != sqltype.Text {
		t.Fatal("lost TEXT result")
	}
	if want == nil {
		if !value.IsNull() {
			t.Fatal("expected typed NULL", value)
		}
	} else if text, err := value.Text(); err != nil || text != want {
		t.Fatalf("definition got %q (%v), want %q", text, err, want)
	}
}

func TestDeparsePartitionPresenterRange(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lower, upper []string
		want         string
	}{
		{"ordinary", []string{"0"}, []string{"10"}, "((k IS NOT NULL) AND (k >= 0) AND (k < 10))"},
		{"minimum", []string{"MIN"}, []string{"10"}, "((k IS NOT NULL) AND (k < 10))"},
		{"unbounded", []string{"MIN"}, []string{"MAX"}, "(k IS NOT NULL)"},
		{"equal_prefix", []string{"1", "2"}, []string{"1", "5"}, "((a IS NOT NULL) AND (b IS NOT NULL) AND (a = 1) AND (b >= 2) AND (b < 5))"},
		{"lexicographic", []string{"1", "2"}, []string{"3", "4"}, "((a IS NOT NULL) AND (b IS NOT NULL) AND ((a > 1) OR ((a = 1) AND (b >= 2))) AND ((a < 3) OR ((a = 3) AND (b < 4))))"},
		{"suffix", []string{"1", "MIN"}, []string{"3", "MAX"}, "((a IS NOT NULL) AND (b IS NOT NULL) AND (a >= 1) AND (a <= 3))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			types := []sqltype.ID{sqltype.Int4}
			if len(tc.lower) == 2 {
				types = append(types, sqltype.Int4)
			}
			f := newDeparsePartitionFixture(t, parser.PartitionRange, types...)
			for i := range tc.lower {
				for _, bound := range []struct {
					text   string
					target *[]catalog.PartitionDatum
				}{{tc.lower[i], &f.child.Membership.Lower}, {tc.upper[i], &f.child.Membership.Upper}} {
					datum := catalog.PartitionDatum{}
					if bound.text == "MIN" {
						datum.Kind = -1
					} else if bound.text == "MAX" {
						datum.Kind = 1
					} else {
						datum = f.datum(t, i, bound.text)
					}
					*bound.target = append(*bound.target, datum)
				}
			}
			beforeLower := append([]catalog.PartitionDatum(nil), f.child.Membership.Lower...)
			beforeKeys := append([]catalog.PartitionKeyEntry(nil), f.parent.Partition.Keys...)
			f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, tc.want)
			f.check(t, functions.DeparsePartitionConstraint, f.parent.Address().ObjectID, nil)
			if !reflect.DeepEqual(beforeLower, f.child.Membership.Lower) || !reflect.DeepEqual(beforeKeys, f.parent.Partition.Keys) {
				t.Fatal("inquiry mutated immutable descriptors")
			}
		})
	}
}

func TestDeparsePartitionPresenterList(t *testing.T) {
	for _, tc := range []struct {
		values   []string
		nullable bool
		want     string
	}{
		{nil, true, "(k IS NULL)"},
		{[]string{"1"}, false, "((k IS NOT NULL) AND (k = 1))"},
		{[]string{"3", "1"}, false, "((k IS NOT NULL) AND (k = ANY (ARRAY[3, 1])))"},
		{[]string{"3", "1"}, true, "((k IS NULL) OR (k = ANY (ARRAY[3, 1])))"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			f := newDeparsePartitionFixture(t, parser.PartitionList, sqltype.Int4)
			for _, value := range tc.values {
				f.child.Membership.Values = append(f.child.Membership.Values, f.datum(t, 0, value))
			}
			if tc.nullable {
				f.child.Membership.Values = append(f.child.Membership.Values, catalog.PartitionDatum{Value: sqltype.Null(f.parent.Partition.Keys[0].Usage.Type)})
			}
			before := append([]catalog.PartitionDatum(nil), f.child.Membership.Values...)
			f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, tc.want)
			f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, "LIST (k)")
			if !reflect.DeepEqual(before, f.child.Membership.Values) {
				t.Fatal("LIST declaration order was changed")
			}
		})
	}
}

func TestDeparsePartitionPresenterHashAndAbsence(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionHash, sqltype.Int4, sqltype.Text)
	f.child.Membership.Modulus, f.child.Membership.Remainder = 4, 1
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, "HASH (a, b)")
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, "satisfies_hash_partition('190001'::oid, 4, 1, a, b)")
	f.check(t, functions.DeparsePartitionKey, f.child.Address().ObjectID, nil)
	f.check(t, functions.DeparsePartitionKey, 0, nil)
	f.check(t, functions.DeparsePartitionConstraint, 4294967295, nil)
	f.parent.ObjectAddress.ObjectID = 190099
	f.child.Membership.Parent = f.parent.Address()
	f.put(f.parent)
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, "satisfies_hash_partition('190099'::oid, 4, 1, a, b)")
	f.child.Membership = nil
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, nil)
	f.child.Partition = nil
	f.check(t, functions.DeparsePartitionKey, f.child.Address().ObjectID, nil)
}

func TestDeparsePartitionPresenterDefaultAndAncestry(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Int4)
	f.child.Membership.Lower = []catalog.PartitionDatum{f.datum(t, 0, "0")}
	f.child.Membership.Upper = []catalog.PartitionDatum{f.datum(t, 0, "10")}
	f.child.RelationKind = catalog.PartitionedRelation
	f.child.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionRange, Keys: append([]catalog.PartitionKeyEntry(nil), f.parent.Partition.Keys...)}
	leaf := *f.child
	leaf.ObjectAddress.ObjectID++
	leaf.ObjectName = "deparse_leaf"
	leaf.Partition = nil
	leaf.RelationKind = catalog.TableRelation
	leaf.Membership = &catalog.PartitionMembership{Parent: f.child.Address(), Strategy: parser.PartitionRange, Lower: []catalog.PartitionDatum{f.datum(t, 0, "2")}, Upper: []catalog.PartitionDatum{f.datum(t, 0, "5")}}
	f.child.Partition.Children = []ids.ObjectAddress{leaf.Address()}
	f.put(&leaf)
	f.check(t, functions.DeparsePartitionConstraint, leaf.Address().ObjectID, "((k IS NOT NULL) AND (k >= 0) AND (k < 10) AND (k IS NOT NULL) AND (k >= 2) AND (k < 5))")
	leaf.Membership = &catalog.PartitionMembership{Parent: f.child.Address(), Strategy: parser.PartitionRange, DefaultBound: true}
	f.child.Partition.DefaultChild = leaf.Address()
	f.check(t, functions.DeparsePartitionConstraint, leaf.Address().ObjectID, "((k IS NOT NULL) AND (k >= 0) AND (k < 10))")
	f.child.Membership.DetachPending = true
	f.check(t, functions.DeparsePartitionConstraint, leaf.Address().ObjectID, "((k IS NOT NULL) AND (k >= 0) AND (k < 10))")
	f.child.Membership = nil
	f.check(t, functions.DeparsePartitionConstraint, leaf.Address().ObjectID, nil)
	f.check(t, functions.DeparsePartitionKey, f.child.Address().ObjectID, "RANGE (k)")
	// DEFAULT's LIST union is sorted without mutating ordinary declaration order.
	g := newDeparsePartitionFixture(t, parser.PartitionList, sqltype.Int4)
	g.child.Membership.Values = []catalog.PartitionDatum{g.datum(t, 0, "3"), g.datum(t, 0, "1")}
	def := *g.child
	def.ObjectAddress.ObjectID++
	def.ObjectName = "deparse_default"
	def.Membership = &catalog.PartitionMembership{Parent: g.parent.Address(), Strategy: parser.PartitionList, DefaultBound: true}
	g.parent.Partition.Children = append(g.parent.Partition.Children, def.Address())
	g.parent.Partition.DefaultChild = def.Address()
	g.put(&def)
	g.check(t, functions.DeparsePartitionConstraint, def.Address().ObjectID, "(NOT ((k IS NOT NULL) AND (k = ANY (ARRAY[1, 3]))))")
	g.check(t, functions.DeparsePartitionConstraint, g.child.Address().ObjectID, "((k IS NOT NULL) AND (k = ANY (ARRAY[3, 1])))")
}

func TestDeparsePartitionPresenterCancellationAndAuthority(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Int4)
	f.child.Membership.Lower = []catalog.PartitionDatum{f.datum(t, 0, "0")}
	f.child.Membership.Upper = []catalog.PartitionDatum{f.datum(t, 0, "10")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, signature := range []functions.DeparseSignature{functions.DeparsePartitionKey, functions.DeparsePartitionConstraint} {
		if _, err := f.call(ctx, signature, f.child.Address().ObjectID); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
	}
	key := f.parent.Partition.Keys[0]
	f.parent.Partition.Keys[0].Comparison = sqltype.IndexComparison{}
	if _, err := f.call(context.Background(), functions.DeparsePartitionConstraint, f.child.Address().ObjectID); err == nil {
		t.Fatal("changed comparator returned successful definition")
	}
	f.parent.Partition.Keys[0] = key
	class, _ := f.view.OperatorClass(ids.OID(key.Class.OID()))
	member, _ := f.view.FamilyOperator(class.Family, class.InputType, class.InputType, 4, 's')
	bad := *member
	bad.Operator = 4294967295
	f.put(&bad)
	if _, err := f.call(context.Background(), functions.DeparsePartitionConstraint, f.child.Address().ObjectID); err == nil {
		t.Fatal("missing family operator returned guessed comparison")
	}
	f.put(member)
	f.child.Membership.Lower[0].Value = sqltype.Null(key.Usage.Type)
	if _, err := f.call(context.Background(), functions.DeparsePartitionConstraint, f.child.Address().ObjectID); err == nil {
		t.Fatal("NULL range bound became an invented predicate")
	}
}

func TestDeparsePartitionPresenterTypedBoundsAndRangeDefault(t *testing.T) {
	for _, tc := range []struct {
		typ         sqltype.ID
		value, want string
	}{
		{sqltype.Text, "it's", "((k IS NOT NULL) AND (k = 'it''s'::text))"},
		{sqltype.Int4, "-2", "((k IS NOT NULL) AND (k = '-2'::integer))"},
		{sqltype.Int8, "9", "((k IS NOT NULL) AND (k = '9'::bigint))"},
		{sqltype.Date, "2025-01-01", "((k IS NOT NULL) AND (k = '2025-01-01'::date))"},
		{sqltype.Numeric, "1.25", "((k IS NOT NULL) AND (k = 1.25))"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			f := newDeparsePartitionFixture(t, parser.PartitionList, tc.typ)
			f.child.Membership.Values = []catalog.PartitionDatum{f.datum(t, 0, tc.value)}
			f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, tc.want)
		})
	}
	f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Int4)
	f.child.Membership.DefaultBound = true
	f.parent.Partition.DefaultChild = f.child.Address()
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, nil)
	explicit := *f.child
	explicit.ObjectAddress.ObjectID++
	explicit.ObjectName = "range_explicit"
	explicit.Membership = &catalog.PartitionMembership{Parent: f.parent.Address(), Strategy: parser.PartitionRange, Lower: []catalog.PartitionDatum{f.datum(t, 0, "0")}, Upper: []catalog.PartitionDatum{f.datum(t, 0, "10")}}
	f.parent.Partition.Children = append(f.parent.Partition.Children, explicit.Address())
	f.put(&explicit)
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, "(NOT ((k IS NOT NULL) AND ((k >= 0) AND (k < 10))))")
	second := explicit
	second.ObjectAddress.ObjectID++
	second.ObjectName = "range_second"
	second.Membership = &catalog.PartitionMembership{Parent: f.parent.Address(), Strategy: parser.PartitionRange, Lower: []catalog.PartitionDatum{f.datum(t, 0, "20")}, Upper: []catalog.PartitionDatum{f.datum(t, 0, "30")}}
	// Deliberately place siblings out of range order in immutable catalog input.
	f.parent.Partition.Children = []ids.ObjectAddress{second.Address(), f.child.Address(), explicit.Address()}
	f.put(&second)
	before := append([]ids.ObjectAddress(nil), f.parent.Partition.Children...)
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, "(NOT ((k IS NOT NULL) AND (((k >= 0) AND (k < 10)) OR ((k >= 20) AND (k < 30)))))")
	if !reflect.DeepEqual(before, f.parent.Partition.Children) {
		t.Fatal("DEFAULT presentation reordered catalog children")
	}
}
