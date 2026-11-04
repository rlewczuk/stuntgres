package sqltype

import "fmt"

// statisticsMCVPayload is frozen only by the canonical typed builder. Values
// already own immutable backing; copying their handles does not clone datums.
type statisticsMCVPayload struct {
	width        int
	usages       [8]Usage
	dictionaries [8][]Value
	items        []StatisticsMCVItem
}

// StatisticsMCVView borrows immutable typed construction without exposing its
// dictionaries or their slice storage. Raw serialized images grant no such view.
type StatisticsMCVView struct{ payload *statisticsMCVPayload }

func (v Value) StatisticsMCV() (StatisticsMCVView, error) {
	if v.IsNull() || !v.validStatistics() || v.statistics.kind != PgMCVList || v.statistics.mcv == nil {
		return StatisticsMCVView{}, fmt.Errorf("MCV list has no typed construction")
	}
	return StatisticsMCVView{payload: v.statistics.mcv}, nil
}

func (v StatisticsMCVView) Width() int {
	if v.payload == nil {
		return 0
	}
	return v.payload.width
}

func (v StatisticsMCVView) Len() int {
	if v.payload == nil {
		return 0
	}
	return len(v.payload.items)
}

// Item returns independent fixed-size facts, never mutable construction storage.
func (v StatisticsMCVView) Item(index int) (StatisticsMCVItem, error) {
	if v.payload == nil || index < 0 || index >= len(v.payload.items) {
		return StatisticsMCVItem{}, fmt.Errorf("MCV item index out of bounds")
	}
	return v.payload.items[index], nil
}

// Value preserves the exact dimension usage and SQL NULL identity captured by
// the producer. It does not render text or resolve a new descriptor generation.
func (v StatisticsMCVView) Value(index, dimension int) (Value, error) {
	if v.payload == nil || index < 0 || index >= len(v.payload.items) {
		return Value{}, fmt.Errorf("MCV item index out of bounds")
	}
	if dimension < 0 || dimension >= v.payload.width {
		return Value{}, fmt.Errorf("MCV dimension index out of bounds")
	}
	item := &v.payload.items[index]
	if item.Nulls[dimension] {
		return Null(v.payload.usages[dimension].Type), nil
	}
	return v.payload.dictionaries[dimension][item.Indexes[dimension]], nil
}
