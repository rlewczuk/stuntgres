package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var gistOwners = []struct {
	name                 string
	oid                  ids.OID
	input                sqltype.ID
	text                 string
	returnable, distance bool
}{
	{"inet_ops", 10017, sqltype.Inet, "10.0.0.1/24", true, false},
	{"box_ops", 10060, sqltype.Box, "(2,2),(0,0)", true, true},
	{"point_ops", 10061, sqltype.Point, "(3,4)", true, true},
	{"poly_ops", 10062, sqltype.Polygon, "((0,0),(4,0),(0,4))", false, true},
	{"circle_ops", 10063, sqltype.Circle, "<(0,0),1>", false, true},
	{"tsvector_ops", 10072, sqltype.TSVector, "a:1 b:2", false, false},
	{"tsquery_ops", 10075, sqltype.TSQuery, "a & b", false, false},
	{"range_ops", 10078, sqltype.Int4Range, "[1,3)", true, false},
	{"multirange_ops", 10082, sqltype.Int4Multirange, "{[1,3),[7,9)}", false, false},
}

func TestGiSTClassInventory(t *testing.T) {
	view := engine.New().CatalogRead()
	count := 0
	for _, descriptor := range view.Descriptors() {
		if c, ok := descriptor.(*catalog.OperatorClass); ok && c.Method == 783 {
			count++
		}
	}
	if count != 9 {
		t.Fatal("class inventory", count)
	}
	for _, owner := range gistOwners {
		class, ok := view.IndexClass(owner.oid)
		typ, exists := view.LookupType(owner.input)
		if !ok || !exists || !class.IsGiST() || !class.Accepts(typ) {
			t.Fatal(owner, class)
		}
		def, found, err := view.DefaultIndexClass(783, typ)
		if err != nil || found != (owner.input != sqltype.Inet) || found && !def.SameIdentity(class) {
			t.Fatal(owner, def, found, err)
		}
	}
	for _, expected := range []struct {
		oid             ids.OID
		name, signature string
		result          ids.OID
	}{
		{2179, "gist_point_consistent", "2281,600,21,26,2281", 16},
		{2583, "gist_box_union", "2281,2281", 603},
		{1030, "gist_point_compress", "2281", 2281},
		{2581, "gist_box_penalty", "2281,2281,2281", 2281},
		{2582, "gist_box_picksplit", "2281,2281", 2281},
		{2584, "gist_box_same", "603,603,2281", 2281},
		{3064, "gist_point_distance", "2281,600,21,26,2281", 701},
		{3282, "gist_point_fetch", "2281", 2281},
		{3435, "gist_point_sortsupport", "2281", 2278},
		{2578, "gist_box_consistent", "2281,603,21,26,2281", 16},
		{3998, "gist_box_distance", "2281,603,21,26,2281", 701},
		{6347, "gist_translate_cmptype_common", "23", 21},
		{2585, "gist_poly_consistent", "2281,604,21,26,2281", 16},
		{2586, "gist_poly_compress", "2281", 2281},
		{3288, "gist_poly_distance", "2281,604,21,26,2281", 701},
		{2591, "gist_circle_consistent", "2281,718,21,26,2281", 16},
		{2592, "gist_circle_compress", "2281", 2281},
		{3280, "gist_circle_distance", "2281,718,21,26,2281", 701},
		{3654, "gtsvector_consistent", "2281,3614,21,26,2281", 16},
		{3651, "gtsvector_union", "2281,2281", 3642},
		{3648, "gtsvector_compress", "2281", 2281},
		{3649, "gtsvector_decompress", "2281", 2281},
		{3653, "gtsvector_penalty", "2281,2281,2281", 2281},
		{3650, "gtsvector_picksplit", "2281,2281", 2281},
		{3652, "gtsvector_same", "3642,3642,2281", 2281},
		{3434, "gtsvector_options", "2281", 2278},
		{3701, "gtsquery_consistent", "2281,3615,21,26,2281", 16},
		{3698, "gtsquery_union", "2281,2281", 20},
		{3695, "gtsquery_compress", "2281", 2281},
		{3700, "gtsquery_penalty", "2281,2281,2281", 2281},
		{3697, "gtsquery_picksplit", "2281,2281", 2281},
		{3699, "gtsquery_same", "20,20,2281", 2281},
		{3875, "range_gist_consistent", "2281,3831,21,26,2281", 16},
		{3876, "range_gist_union", "2281,2281", 3831},
		{3879, "range_gist_penalty", "2281,2281,2281", 2281},
		{3880, "range_gist_picksplit", "2281,2281", 2281},
		{3881, "range_gist_same", "3831,3831,2281", 2281},
		{6391, "range_sortsupport", "2281", 2278},
		{3553, "inet_gist_consistent", "2281,869,21,26,2281", 16},
		{3554, "inet_gist_union", "2281,2281", 869},
		{3555, "inet_gist_compress", "2281", 2281},
		{3557, "inet_gist_penalty", "2281,2281,2281", 2281},
		{3558, "inet_gist_picksplit", "2281,2281", 2281},
		{3559, "inet_gist_same", "869,869,2281", 2281},
		{3573, "inet_gist_fetch", "2281", 2281},
		{6154, "multirange_gist_consistent", "2281,4537,21,26,2281", 16},
		{6156, "multirange_gist_compress", "2281", 2281},
	} {
		identity, ok := catalog.BuiltinRoutineIdentityByOID(expected.oid)
		result, exists := catalog.BuiltinRoutineResultByOID(expected.oid)
		if !ok || !exists || identity.Name != expected.name || string(identity.Signature) != expected.signature || result != expected.result {
			t.Fatal("released GiST routine metadata", expected, identity, result)
		}
	}
}

func TestGiSTLogicalMembership(t *testing.T) {
	view := engine.New().CatalogRead()
	ctx := context.Background()
	for _, owner := range gistOwners {
		t.Run(owner.name, func(t *testing.T) {
			typ, _ := view.LookupType(owner.input)
			class, _ := view.IndexClass(owner.oid)
			value, err := typ.Input(ctx, owner.text, -1)
			if err != nil {
				t.Fatal(err)
			}
			parent := &catalog.Relation{Attributes: []int32{1}, Columns: []catalog.Column{{Name: "v", Usage: typ.Usage()}}}
			def := &catalog.IndexDefinition{Method: catalog.GistMethod, MethodOID: 783, Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Ready: true, Valid: true, Live: true}
			store := &storage.Relation{Versions: []storage.RowVersion{
				{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}},
				{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{sqltype.Null(typ)}},
				{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{value}},
			}}
			eval := &exec.Context{EvalStatement: exec.EvalStatement{Catalog: view, Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := index.BuildIndexRoot(eval, parent, def, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := []storage.IndexEntry{{Row: 1, Version: 1, Position: 0, Keys: []sqltype.Value{value}}, {Row: 2, Version: 2, Position: 1, Keys: []sqltype.Value{sqltype.Null(typ)}}, {Row: 3, Version: 3, Position: 2, Keys: []sqltype.Value{value}}}
			if root.Hash != nil || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("exact unsorted membership", root.Entries, want)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: view, Context: canceled}}, parent, def, store, nil)
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("canceled build changed predecessor", err)
			}
			idx := &catalog.Relation{SQLIndex: def}
			publication := &index.RowPublication{Writes: []storage.RowVersion{{Row: 1, Version: 4, Base: 1, Born: 2, Values: []sqltype.Value{sqltype.Null(typ)}}, {Row: 4, Version: 5, Born: 2, Values: []sqltype.Value{value}}}, SingleSlot: -1}
			next, err := index.MergeIndexRoot(eval, parent, idx, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(next.Entries) != 4 || next.Entries[0].Row != 2 || next.Entries[1].Row != 3 || next.Entries[2].Version != 4 || next.Entries[3].Version != 5 || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("immutable live delta", next.Entries)
			}
			column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
			boolean, _ := view.LookupType(sqltype.Bool)
			predicate := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: column, Not: true}
			partial := *def
			partial.Keys = []catalog.IndexKey{{Class: class, Derived: &catalog.DerivedIndexKey{Definition: column, Execution: column, Attributes: parent.Attributes}}}
			partial.Predicate = &catalog.IndexPredicate{Definition: predicate, Execution: predicate, Attributes: parent.Attributes}
			filtered, err := index.BuildIndexRoot(eval, parent, &partial, store, nil)
			if err != nil || len(filtered.Entries) != 2 || !reflect.DeepEqual(filtered.Entries[0], want[0]) || !reflect.DeepEqual(filtered.Entries[1], want[2]) {
				t.Fatal("partial derived membership lost exact values", filtered, err)
			}
		})
	}
}

func TestGiSTOptionCapture(t *testing.T) {
	view := engine.New().CatalogRead()
	class, _ := view.IndexClass(10072)
	options := func(value string) []parser.ViewOption {
		return []parser.ViewOption{{Name: parser.Identifier{Text: "siglen"}, Value: value, HasValue: true}}
	}
	a, err := index.BindClassOptions(view, class, options("32"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := index.BindClassOptions(view, class, options("2024"))
	if err != nil {
		t.Fatal(err)
	}
	absent, err := index.BindClassOptions(view, class, nil)
	if err != nil || a.EffectiveSiglen() != 32 || b.EffectiveSiglen() != 2024 || absent.EffectiveSiglen() != 124 || absent.Explicit {
		t.Fatal(a, b, absent, err)
	}
	for _, value := range []string{"0", "2025", "abc", "true"} {
		if _, err := index.BindClassOptions(view, class, options(value)); err == nil {
			t.Fatal("invalid siglen admitted", value)
		}
	}
	point, _ := view.IndexClass(10061)
	if _, err := index.BindClassOptions(view, point, options("32")); err == nil {
		t.Fatal("point acquired vector options")
	}
	if a.EffectiveSiglen() != 32 || b.EffectiveSiglen() != 2024 {
		t.Fatal("capture mutated")
	}
	original, _ := view.OperatorClass(10072)
	alias := *original
	alias.ObjectAddress.ObjectID, alias.ObjectName, alias.Default = 190001, "renamed_vector", false
	state := catalog.CloneState(view.State())
	state.SetDescriptor(alias.Address(), &alias)
	captured, ok := catalog.NewRead(view.Owner(), state).IndexClass(alias.Address().ObjectID)
	if !ok {
		t.Fatal("missing alias")
	}
	if option, err := index.BindClassOptions(catalog.NewRead(view.Owner(), state), captured, options("64")); err != nil || option.EffectiveSiglen() != 64 {
		t.Fatal("options followed spelling instead of provider", option, err)
	}
	member, _ := view.FamilySupport(3655, 3614, 3614, 10)
	state.RemoveDescriptor(member.Address())
	if _, err := index.BindClassOptions(catalog.NewRead(view.Owner(), state), captured, options("64")); err == nil {
		t.Fatal("removed options provider was resurrected")
	}
}

func TestGiSTCapturedFamilyCapabilities(t *testing.T) {
	view := engine.New().CatalogRead()
	for _, owner := range gistOwners {
		class, _ := view.IndexClass(owner.oid)
		def := &catalog.IndexDefinition{Method: catalog.GistMethod, MethodOID: 783, Keys: []catalog.IndexKey{{Class: class}}, Include: []int32{2}}
		for _, p := range []struct {
			name string
			want bool
		}{{"returnable", owner.returnable}, {"distance_orderable", owner.distance}, {"orderable", false}} {
			value, known := index.ColumnCapability(view, def, 1, p.name)
			if !known || value != p.want {
				t.Fatal(owner.name, p.name, value, known)
			}
		}
		for _, p := range []string{"returnable", "distance_orderable"} {
			if _, known := index.ColumnCapability(view, def, 2, p); known {
				t.Fatal("INCLUDE callback became known", p)
			}
		}
		state := catalog.CloneState(view.State())
		state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: owner.oid})
		if _, known := index.ColumnCapability(catalog.NewRead(view.Owner(), state), def, 1, "returnable"); known {
			t.Fatal("dropped class resurrected", owner.name)
		}
		if support, exists := view.FamilySupport(ids.OID(class.FamilyOID()), ids.OID(class.InputOID()), ids.OID(class.InputOID()), 8); exists {
			changed := catalog.CloneState(view.State())
			changed.RemoveDescriptor(support.Address())
			value, known := index.ColumnCapability(catalog.NewRead(view.Owner(), changed), def, 1, "distance_orderable")
			if !known || value {
				t.Fatal("distance ignored actual removed family support", owner.name, value, known)
			}
		}
	}
}
