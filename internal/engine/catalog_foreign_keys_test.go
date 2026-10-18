package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestCatalogRelationsInventory(t *testing.T) {
	e := New()
	call := catalogRelationsUnitBind(t, e, "pg_get_catalog_foreign_keys").(catalogForeignKeysExpression)
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
	e := New()
	call := catalogRelationsUnitBind(t, e, "pg_get_catalog_foreign_keys").(catalogForeignKeysExpression)
	evaluation := &EvalContext{Context: context.Background()}
	first, err := openFunctionCall(evaluation, call)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := openFunctionCall(evaluation, call)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	if first.catalogKeys != call.metadata || second.catalogKeys != call.metadata || first.position != 0 || second.position != 0 || first.owned != nil || second.owned != nil {
		t.Fatal("opening eagerly materialized rows or copied immutable metadata")
	}
	value, present, err := first.next(context.Background(), call.Usage.Type)
	if err != nil || !present || value.IsNull() || value.Type() != call.Usage.Type || first.position != 1 || second.position != 0 {
		t.Fatal("invocations share index", present, err)
	}
	for i := range 6 {
		if value.CompositeField(i).IsNull() || value.CompositeField(i).Type() != call.Usage.Type.CompositeMember(i).Usage.Type {
			t.Fatal("composite field generation", i)
		}
	}
	second.close()
	if second.catalogKeys != nil || !second.exhausted {
		t.Fatal("early close retained invocation state")
	}
	count := 1
	for {
		_, present, err := first.next(context.Background(), call.Usage.Type)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		count++
	}
	if count != 219 || !first.exhausted || first.catalogKeys != nil {
		t.Fatal("EOF ownership/count", count)
	}
	if _, present, err := first.next(context.Background(), call.Usage.Type); present || err != nil {
		t.Fatal("EOF is not stable", present, err)
	}
	restarted, err := openFunctionCall(evaluation, call)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, present, err := restarted.next(ctx, call.Usage.Type); present || !errors.Is(err, context.Canceled) || restarted.position != 0 {
		t.Fatal("canceled cursor advanced", present, err)
	}
	restarted.close()
	if restarted.catalogKeys != nil {
		t.Fatal("canceled cursor retained metadata")
	}
}
