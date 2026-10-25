package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func listPartitionUnitDescriptor(t *testing.T) (*Engine, catalog.Read, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e, view, root, leaf := hashPartitionUnitEngine(t)
	usage := root.Columns[0].Usage
	class, ok := sqltype.DefaultIndexClass(usage.Type)
	if !ok {
		t.Fatal("missing integer ordering class")
	}
	comparison, err := operatorFamilyTestOrdering(usage, class)
	if err != nil {
		t.Fatal(err)
	}
	root.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionList, Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: usage, Class: class, Comparison: comparison, Label: "k"}}, Children: []ids.ObjectAddress{leaf.Address()}}
	one, err := sqltype.Integer(usage.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionList, Values: []catalog.PartitionDatum{{Value: one}, {Value: sqltype.Null(usage.Type)}}}
	return e, view, root, leaf
}

func TestListPartitionDescriptor(t *testing.T) {
	e, view, root, leaf := listPartitionUnitDescriptor(t)
	for _, relation := range []*catalog.Relation{root, leaf} {
		if err := partition.ValidateDescriptor(view, relation); err != nil {
			t.Fatal(err)
		}
	}
	originalKeys := slices.Clone(root.Partition.Keys)
	originalValues := slices.Clone(leaf.Membership.Values)
	cases := []struct {
		name   string
		change func(*catalog.Relation, *catalog.Relation)
	}{
		{"multikey", func(r, l *catalog.Relation) { r.Partition.Keys = append(r.Partition.Keys, r.Partition.Keys[0]) }},
		{"empty", func(r, l *catalog.Relation) { l.Membership.Values = nil }},
		{"sentinel", func(r, l *catalog.Relation) { l.Membership.Values[0].Kind = -1 }},
		{"wrong_generation", func(r, l *catalog.Relation) {
			for _, typ := range sqltype.Builtins() {
				if typ.ID() == sqltype.Int4 {
					l.Membership.Values[0].Value = sqltype.Null(typ)
					break
				}
			}
		}},
		{"duplicate_value", func(r, l *catalog.Relation) {
			l.Membership.Values = append(l.Membership.Values, l.Membership.Values[0])
		}},
		{"duplicate_null", func(r, l *catalog.Relation) {
			l.Membership.Values = append(l.Membership.Values, l.Membership.Values[1])
		}},
		{"range_shape", func(r, l *catalog.Relation) { l.Membership.Lower = slices.Clone(l.Membership.Values) }},
		{"hash_shape", func(r, l *catalog.Relation) { l.Membership.Modulus = 1 }},
		{"default_values", func(r, l *catalog.Relation) { l.Membership.DefaultBound = true; r.Partition.DefaultChild = l.Address() }},
		{"strategy", func(r, l *catalog.Relation) { l.Membership.Strategy = parser.PartitionRange }},
		{"parent", func(r, l *catalog.Relation) { l.Membership.Parent = l.Address() }},
		{"cycle", func(r, l *catalog.Relation) {
			r.Membership = &catalog.PartitionMembership{Parent: l.Address(), Strategy: parser.PartitionList, Values: slices.Clone(l.Membership.Values)}
			l.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionList, Keys: slices.Clone(r.Partition.Keys), Children: []ids.ObjectAddress{r.Address()}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nextRoot, nextLeaf := *root, *leaf
			definition, membership := *root.Partition, *leaf.Membership
			definition.Keys = slices.Clone(definition.Keys)
			membership.Values = slices.Clone(membership.Values)
			nextRoot.Partition, nextLeaf.Membership = &definition, &membership
			c.change(&nextRoot, &nextLeaf)
			state := catalog.CloneState(view.State())
			state.SetDescriptor(root.Address(), &nextRoot)
			state.SetDescriptor(leaf.Address(), &nextLeaf)
			candidate := e.Catalog.Read(state)
			if err := partition.ValidateDescriptor(candidate, &nextRoot); err == nil {
				t.Fatal("malformed hierarchy accepted")
			}
		})
	}
	for _, value := range originalValues {
		state := catalog.CloneState(view.State())
		nextRoot, sibling := *root, *leaf
		definition := *root.Partition
		nextRoot.Partition = &definition
		sibling.ObjectAddress.ObjectID++
		sibling.ObjectName = "list_sibling"
		sibling.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionList, Values: []catalog.PartitionDatum{value}}
		definition.Children = append(slices.Clone(definition.Children), sibling.Address())
		state.SetDescriptor(root.Address(), &nextRoot)
		state.SetDescriptor(sibling.Address(), &sibling)
		if err := partition.ValidateDescriptor(e.Catalog.Read(state), &nextRoot); err == nil {
			t.Fatal("sibling collision accepted", value)
		}
	}
	if !reflect.DeepEqual(root.Partition.Keys, originalKeys) || !reflect.DeepEqual(leaf.Membership.Values, originalValues) {
		t.Fatal("descriptor validation mutated retained metadata")
	}
}

func TestListPartitionComparison(t *testing.T) {
	e := New()
	for _, c := range []struct {
		name               string
		id                 sqltype.ID
		bound, equal, miss string
	}{
		{"wide", sqltype.Int8, "9007199254740993", "9007199254740993", "9007199254740992"},
		{"text", sqltype.Text, "NULL", "NULL", "null"},
		{"numeric", sqltype.Numeric, "1.00", "1.000", "1.01"},
		{"padded", sqltype.BPChar, "x ", "x", "y"},
	} {
		t.Run(c.name, func(t *testing.T) {
			typ, ok := e.CatalogRead().LookupType(c.id)
			if !ok {
				t.Fatal(c.id)
			}
			class, ok := sqltype.DefaultIndexClass(typ)
			if !ok {
				t.Fatal("missing comparison class", c.id)
			}
			usage := typ.Usage()
			if c.id == sqltype.Text {
				provider := collationUnitDefinition(e, 90000, "list_comparison")
				capability, err := provider.Usage()
				if err != nil {
					t.Fatal(err)
				}
				usage.Collation = capability
			}
			comparison, err := operatorFamilyTestOrdering(usage, class)
			if err != nil {
				t.Fatal(err)
			}
			key := catalog.PartitionKeyEntry{Attribute: 1, Usage: usage, Class: class, Comparison: comparison}
			boundValue, err := typ.Input(context.Background(), c.bound, -1)
			if err != nil {
				t.Fatal(err)
			}
			bound := &catalog.PartitionMembership{Strategy: parser.PartitionList, Values: []catalog.PartitionDatum{{Value: boundValue}, {Value: sqltype.Null(typ)}}}
			root := &catalog.Relation{Attributes: []int32{1}, Partition: &catalog.PartitionDefinition{Strategy: parser.PartitionList, Keys: []catalog.PartitionKeyEntry{key}}}
			for _, witness := range []struct {
				text       string
				null, want bool
			}{{c.equal, false, true}, {c.miss, false, false}, {"", true, true}} {
				value := sqltype.Null(typ)
				if !witness.null {
					value, err = typ.Input(context.Background(), witness.text, -1)
					if err != nil {
						t.Fatal(err)
					}
				}
				var scratch [1]catalog.PartitionDatum
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
				actualKey, err := partition.RowKey(root, []sqltype.Value{value}, &evaluation, scratch[:])
				if err != nil {
					t.Fatal(err)
				}
				got, err := partition.Contains(context.Background(), root.Partition.Keys, bound, actualKey, sqltype.InputEnvironment{})
				if err != nil || got != witness.want {
					t.Fatal(witness, got, err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, err := partition.Contains(ctx, root.Partition.Keys, bound, actualKey, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
}
