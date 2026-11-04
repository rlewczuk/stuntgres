package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func mcvBridgeTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	return types
}

func TestStatisticsMCVTypedBridge(t *testing.T) {
	types := mcvBridgeTypes()
	integer, text, list := types[sqltype.Int4], types[sqltype.Text], types[sqltype.PgMCVList]
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	usages := []sqltype.Usage{integer.Usage(), text.Usage()}
	dictionaries := [8][]sqltype.Value{{one, two}, {sqltype.String(text, "NULL"), sqltype.String(text, "")}}
	items := []sqltype.StatisticsMCVItem{
		{Frequency: .5, BaseFrequency: .25},
		{Indexes: [8]uint16{1, 1}, Frequency: .25, BaseFrequency: .0625},
		{Nulls: [8]bool{true, true}, Frequency: .25, BaseFrequency: .0625},
	}
	value, err := sqltype.StatisticsMCVBuild(t.Context(), list, usages, dictionaries, items)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := sqltype.StatisticsMCVBuild(t.Context(), list, usages, dictionaries, items)
	if err != nil {
		t.Fatal(err)
	}
	view, err := value.StatisticsMCV()
	if err != nil || view.Width() != 2 || view.Len() != 3 {
		t.Fatal(view, err)
	}
	other, err := equivalent.StatisticsMCV()
	if err != nil || view == other {
		t.Fatal("independent construction must own independent backing", err)
	}
	usages[0] = text.Usage()
	dictionaries[0][0] = two
	dictionaries[1][0] = sqltype.String(text, "changed")
	items[0].Frequency = 1
	items[0].Nulls[0] = true
	item, err := view.Item(0)
	if err != nil || item.Frequency != .5 || item.BaseFrequency != .25 || item.Nulls[0] {
		t.Fatal("caller items escaped", item, err)
	}
	item.Nulls[0], item.Frequency = true, 1
	again, _ := view.Item(0)
	if again.Frequency != .5 || again.Nulls[0] {
		t.Fatal("item accessor lends mutable backing", again)
	}
	for _, c := range []struct {
		row, dimension int
		text           string
		null           bool
	}{
		{0, 0, "1", false}, {0, 1, "NULL", false}, {1, 0, "2", false},
		{1, 1, "", false}, {2, 0, "", true}, {2, 1, "", true},
	} {
		got, err := view.Value(c.row, c.dimension)
		wantType := integer
		if c.dimension == 1 {
			wantType = text
		}
		if err != nil || got.Type() != wantType || got.IsNull() != c.null {
			t.Fatal("frozen typed dimension", c, got, err)
		}
		if !c.null {
			out, err := got.Type().OutputWithEnvironment(t.Context(), got, sqltype.OutputEnvironment{})
			if err != nil || out != c.text {
				t.Fatal(c, out, err)
			}
		}
	}
	for _, c := range [][2]int{{-1, 0}, {3, 0}, {0, -1}, {0, 2}} {
		if _, err := view.Value(c[0], c[1]); err == nil {
			t.Fatal("accepted out-of-range dimension", c)
		}
	}
	for _, index := range []int{-1, 3} {
		if _, err := view.Item(index); err == nil {
			t.Fatal("accepted out-of-range item", index)
		}
	}
	var empty sqltype.StatisticsMCVView
	if empty.Len() != 0 || empty.Width() != 0 {
		t.Fatal("zero view facts")
	}
	if _, err := empty.Value(0, 0); err == nil {
		t.Fatal("zero view lookup")
	}
	raw, err := value.Type().AppendBinaryWithEnvironment(t.Context(), nil, value, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	image, err := sqltype.StatisticsMCVValue(list, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []sqltype.Value{equivalent, image} {
		if !sqltype.SameDatum(value, candidate) {
			t.Fatal("auxiliary typed construction changed representation identity")
		}
		if same, err := value.SamePayload(t.Context(), candidate); err != nil || !same {
			t.Fatal("auxiliary typed construction changed payload equivalence", err)
		}
	}
	for _, invalid := range []sqltype.Value{image, sqltype.Null(list), one, {}} {
		if _, err := invalid.StatisticsMCV(); err == nil {
			t.Fatal("nonproducer gained typed inspection authority")
		}
	}
	retagged, err := value.Retag(mcvBridgeTypes()[sqltype.PgMCVList])
	if err != nil {
		t.Fatal(err)
	}
	retained, err := retagged.StatisticsMCV()
	if err != nil || retained != view {
		t.Fatal("compatible outer retag copied/reminted dictionary backing", err)
	}
	var allNull sqltype.StatisticsMCVItem
	allNull.Frequency, allNull.BaseFrequency = 1, 1
	wide := make([]sqltype.Usage, 8)
	for i := range wide {
		wide[i], allNull.Nulls[i] = integer.Usage(), true
	}
	wideValue, err := sqltype.StatisticsMCVBuild(t.Context(), list, wide, [8][]sqltype.Value{}, []sqltype.StatisticsMCVItem{allNull})
	if err != nil {
		t.Fatal(err)
	}
	wideView, err := wideValue.StatisticsMCV()
	if err != nil || wideView.Width() != 8 || wideView.Len() != 1 {
		t.Fatal(err)
	}
	for i := range 8 {
		v, err := wideView.Value(0, i)
		if err != nil || !v.IsNull() || v.Type() != integer {
			t.Fatal("wide typed NULL", i, err)
		}
	}
	measured := &mcvBridgeBudget{Context: context.Background()}
	if _, err := sqltype.StatisticsMCVBuild(measured, list, wide, [8][]sqltype.Value{}, []sqltype.StatisticsMCVItem{allNull}); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, measured.calls - 1} {
		ctx := &mcvBridgeBudget{Context: context.Background(), limit: limit}
		v, err := sqltype.StatisticsMCVBuild(ctx, list, wide, [8][]sqltype.Value{}, []sqltype.StatisticsMCVItem{allNull})
		if !errors.Is(err, context.Canceled) || v.Type() != nil {
			t.Fatal("early/late cancellation published construction", limit, v, err)
		}
	}
	t.Run("recursive-dictionary", mcvBridgeRecursive)
}

type mcvBridgeBudget struct {
	context.Context
	calls, limit int
}

func (c *mcvBridgeBudget) Err() error {
	c.calls++
	if c.limit != 0 && c.calls >= c.limit {
		return context.Canceled
	}
	return nil
}

func mcvBridgeRecursive(t *testing.T) {
	types := mcvBridgeTypes()
	enum, err := sqltype.EnumWithMembers(91001, "mcv_e", "public.mcv_e", []sqltype.EnumMember{{ID: 117, Label: "z", SortOrder: 1}})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(91002, "mcv_d", "public.mcv_d", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(91003, "_mcv_d", domain)
	if err != nil {
		t.Fatal(err)
	}
	original, err := array.Input(t.Context(), "[0:1]={z,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	dictionaries := [8][]sqltype.Value{{original}}
	value, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], []sqltype.Usage{array.Usage()}, dictionaries, []sqltype.StatisticsMCVItem{{Frequency: 1, BaseFrequency: 1}})
	if err != nil {
		t.Fatal(err)
	}
	dictionaries[0][0] = sqltype.Null(array)
	view, err := value.StatisticsMCV()
	if err != nil {
		t.Fatal(err)
	}
	got, err := view.Value(0, 0)
	if err != nil || !sqltype.SameDatum(got, original) || got.Type() != array || got.ArrayElement(0).Type() != domain || got.ArrayElement(0).Type().BaseType() != enum || !got.ArrayElement(1).IsNull() {
		t.Fatal("recursive immutable ownership", got, err)
	}
	out, err := got.Type().OutputWithEnvironment(t.Context(), got, sqltype.OutputEnvironment{})
	if err != nil || out != "[0:1]={z,NULL}" {
		t.Fatal(out, err)
	}
}
