package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestCatalogRelationsInventory(t *testing.T) {
	call := catalogRelationsUnitCall(t)
	if len(catalogForeignKeys) != 219 {
		t.Fatal("released linkage count", len(catalogForeignKeys))
	}
	// Independent selected REL_18_0 header facts cover all flag quadrants,
	// final-column array semantics, zero-reference optionality and unavailable
	// native catalog providers. The complete independent bag lives in reference/.
	witnesses := []catalogForeignKey{
		{1255, 2615, [2]string{"pronamespace"}, [2]string{"oid"}, false, false},
		{1255, 1247, [2]string{"provariadic"}, [2]string{"oid"}, false, true},
		{1255, 1247, [2]string{"proargtypes"}, [2]string{"oid"}, true, false},
		{1255, 1247, [2]string{"proallargtypes"}, [2]string{"oid"}, true, false},
		{2604, 1249, [2]string{"adrelid", "adnum"}, [2]string{"attrelid", "attnum"}, false, false},
		{2606, 1249, [2]string{"conrelid", "conkey"}, [2]string{"attrelid", "attnum"}, true, true},
		{2606, 1249, [2]string{"confrelid", "confkey"}, [2]string{"attrelid", "attnum"}, true, false},
		{2610, 3456, [2]string{"indcollation"}, [2]string{"oid"}, true, true},
		{2610, 2616, [2]string{"indclass"}, [2]string{"oid"}, true, false},
		{2610, 1249, [2]string{"indrelid", "indkey"}, [2]string{"attrelid", "attnum"}, true, true},
		{1261, 1260, [2]string{"roleid"}, [2]string{"oid"}, false, false},
	}
	for _, want := range witnesses {
		count := 0
		for _, got := range catalogForeignKeys {
			if got == want {
				count++
			}
		}
		if count != 1 {
			t.Fatal("missing/duplicated release fact", want, count)
		}
	}
	quadrants := 0
	for i, link := range catalogForeignKeys {
		flag := 0
		if link.array {
			flag |= 2
		}
		if link.optional {
			flag |= 1
		}
		quadrants |= 1 << flag
		for _, slot := range []int{1, 3} {
			array := call.metadata.fields[i][slot]
			if array.IsNull() || array.Type().ID() != 1009 || array.Type().Element().ID() != sqltype.Text || array.ArrayRank() != 1 {
				t.Fatal("TEXT array identity", i, slot)
			}
			dimension := array.ArrayDimension(0)
			if dimension.Lower != 1 || int(dimension.Length) != array.ArrayLen() || array.ArrayLen() < 1 || array.ArrayLen() > 2 {
				t.Fatal("ordered column array bounds", i, dimension)
			}
			for j := range array.ArrayLen() {
				if array.ArrayElement(j).IsNull() || array.ArrayElement(j).Type() != array.Type().Element() {
					t.Fatal("foreign/null column name", i, j)
				}
			}
			detached := array.ArrayDimensions()
			detached[0].Lower = 99
			if array.ArrayDimension(0).Lower != 1 {
				t.Fatal("array accessor exposed mutable metadata")
			}
		}
	}
	if quadrants != 15 {
		t.Fatal("missing array/optional quadrant", quadrants)
	}
}

func TestCatalogRelationsCursor(t *testing.T) {
	call := catalogRelationsUnitCall(t)
	evaluation := &catalogRelationsUnitContext{Context: context.Background()}
	first, err := call.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := call.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.State != call.metadata || second.State != call.metadata || first.Position != 0 || second.Position != 0 || first.Values != nil || second.Values != nil {
		t.Fatal("opening eagerly materialized rows or copied immutable metadata")
	}
	value, present, err := first.Next(context.Background())
	if err != nil || !present || value.IsNull() || value.Type() != call.Usage.Type || first.Position != 1 || second.Position != 0 {
		t.Fatal("invocations share index", present, err)
	}
	for i := range 6 {
		if value.CompositeField(i).IsNull() || value.CompositeField(i).Type() != call.Usage.Type.CompositeMember(i).Usage.Type {
			t.Fatal("composite field generation", i)
		}
	}
	second.Close()
	if second.State != nil || !second.Exhausted {
		t.Fatal("early close retained invocation state")
	}
	count := 1
	for {
		_, present, err := first.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		count++
	}
	if count != 219 || !first.Exhausted || first.State != nil {
		t.Fatal("EOF ownership/count", count)
	}
	if _, present, err := first.Next(context.Background()); present || err != nil {
		t.Fatal("EOF is not stable", present, err)
	}
	restarted, err := call.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, present, err := restarted.Next(ctx); present || !errors.Is(err, context.Canceled) || restarted.Position != 0 {
		t.Fatal("canceled cursor advanced", present, err)
	}
	restarted.Close()
	if restarted.State != nil {
		t.Fatal("canceled cursor retained metadata")
	}
}

type catalogRelationsUnitContext struct {
	unitCapabilities
	context.Context
}

func (c *catalogRelationsUnitContext) FunctionContext() context.Context { return c.Context }
func catalogRelationsUnitCall(t *testing.T) CatalogForeignKeys {
	t.Helper()
	types := formatUnitTypes()
	names := [6]string{"fktable", "fkcols", "pktable", "pkcols", "is_array", "is_opt"}
	ids := [6]sqltype.ID{sqltype.Regclass, 1009, sqltype.Regclass, 1009, sqltype.Bool, sqltype.Bool}
	members := make([]sqltype.CompositeMember, 6)
	for i := range members {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: names[i], Usage: types[ids[i]].Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := newCatalogForeignKeyValues(shape)
	if err != nil {
		t.Fatal(err)
	}
	return CatalogForeignKeys{Info: expr.Info{Usage: shape.Usage()}, metadata: metadata}
}
func TestCatalogRelationsMissingMetadata(t *testing.T) {
	call := catalogRelationsUnitCall(t)
	call.metadata = nil
	if call.Validate() == nil {
		t.Fatal("missing immutable metadata accepted")
	}
}
