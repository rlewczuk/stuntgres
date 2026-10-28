package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ExtendedMCVSnapshot retains construction counts, not source-native datum IDs.
type ExtendedMCVSnapshot struct {
	Rows         uint64                    `json:"rows"`
	Dictionaries [][]json.RawMessage       `json:"dictionaries"`
	Items        []ExtendedMCVSnapshotItem `json:"items"`
}

type ExtendedMCVSnapshotItem struct {
	Nulls          []bool   `json:"nulls"`
	Indexes        []uint16 `json:"indexes"`
	Count          uint64   `json:"count"`
	MarginalCounts []uint64 `json:"marginal_counts"`
}

func invalidMCVSnapshot() error { return fmt.Errorf("stuntgres: invalid MCV statistics snapshot") }

func (r *ExtendedMCVSnapshot) validate(width int) error {
	if width < 1 || width > 8 || r.Rows == 0 || r.Rows > math.MaxInt32 || len(r.Dictionaries) != width || len(r.Items) == 0 || len(r.Items) > 10000 {
		return invalidMCVSnapshot()
	}
	var next [8]int
	var total uint64
	tuples := make(map[[8]uint32]bool, len(r.Items))
	var previous uint64 = math.MaxUint64
	// First-retained-occurrence dictionary order is part of canonical capture.
	var marginals [8]map[uint32]uint64
	var retained [8]map[uint32]uint64
	for d, values := range r.Dictionaries {
		if values == nil || len(values) > len(r.Items) {
			return invalidMCVSnapshot()
		}
		marginals[d] = make(map[uint32]uint64)
		retained[d] = make(map[uint32]uint64)
	}
	for _, item := range r.Items {
		if len(item.Nulls) != width || len(item.Indexes) != width || len(item.MarginalCounts) != width || item.Count == 0 || item.Count > previous || item.Count > r.Rows-total {
			return invalidMCVSnapshot()
		}
		previous = item.Count
		total += item.Count
		var tuple [8]uint32
		for d := range width {
			index, marginal := int(item.Indexes[d]), item.MarginalCounts[d]
			if marginal < item.Count || marginal > r.Rows {
				return invalidMCVSnapshot()
			}
			if item.Nulls[d] {
				if index != 0 {
					return invalidMCVSnapshot()
				}
			} else {
				if index >= len(r.Dictionaries[d]) || index > next[d] {
					return invalidMCVSnapshot()
				}
				if index == next[d] {
					next[d]++
				}
				tuple[d] = uint32(index) + 1
			}
			key := tuple[d]
			if old, ok := marginals[d][key]; ok && old != marginal {
				return invalidMCVSnapshot()
			}
			marginals[d][key] = marginal
			retained[d][key] += item.Count
			if retained[d][key] > marginal {
				return invalidMCVSnapshot()
			}
		}
		if tuples[tuple] {
			return invalidMCVSnapshot()
		}
		tuples[tuple] = true
	}
	for d := range width {
		if next[d] != len(r.Dictionaries[d]) {
			return invalidMCVSnapshot()
		}
		var sum uint64
		for _, count := range marginals[d] {
			if count > r.Rows-sum {
				return invalidMCVSnapshot()
			}
			sum += count
		}
	}
	return nil
}

func captureMCVSnapshot(ctx context.Context, fact *ExtendedStatistics, codec StatisticsSnapshotCodec) (*ExtendedMCVSnapshot, error) {
	if fact.MCVData == nil {
		return nil, nil
	}
	if codec == nil {
		return nil, fmt.Errorf("missing statistics snapshot codec")
	}
	data, width := fact.MCVData, fact.Selection.DimensionLen()
	r := &ExtendedMCVSnapshot{Rows: data.Rows, Dictionaries: make([][]json.RawMessage, width), Items: make([]ExtendedMCVSnapshotItem, len(data.Items))}
	for d := range width {
		r.Dictionaries[d] = make([]json.RawMessage, len(data.Dictionaries[d]))
		for i, v := range data.Dictionaries[d] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			encoded, err := codec.Encode(fact.Usages[d], v)
			if err != nil {
				return nil, err
			}
			r.Dictionaries[d][i] = encoded
		}
	}
	for i, item := range data.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.Items[i] = ExtendedMCVSnapshotItem{Nulls: append([]bool{}, item.Nulls[:width]...), Indexes: append([]uint16{}, item.Indexes[:width]...), Count: item.Count, MarginalCounts: append([]uint64{}, item.MarginalCounts[:width]...)}
	}
	return r, r.validate(width)
}

func restoreMCVSnapshot(ctx context.Context, read catalog.Read, r *ExtendedMCVSnapshot, fact *ExtendedStatistics, codec StatisticsSnapshotCodec) error {
	if r == nil {
		return nil
	}
	if codec == nil {
		return fmt.Errorf("missing statistics snapshot codec")
	}
	width := fact.Selection.DimensionLen()
	if err := r.validate(width); err != nil {
		return err
	}
	data := &ExtendedMCV{Rows: r.Rows, Items: make([]ExtendedMCVItem, len(r.Items))}
	for d := range width {
		grouping, err := sqltype.BindGrouping(fact.Usages[d])
		if err != nil {
			return err
		}
		buckets := make(map[uint64][]sqltype.Value, len(r.Dictionaries[d]))
		data.Dictionaries[d] = make([]sqltype.Value, len(r.Dictionaries[d]))
		for i, raw := range r.Dictionaries[d] {
			if err := ctx.Err(); err != nil {
				return err
			}
			v, err := codec.Decode(fact.Usages[d], raw)
			if err != nil {
				return err
			}
			if v.IsNull() || v.Type() != fact.Usages[d].Type {
				return invalidMCVSnapshot()
			}
			v, err = grouping.PrepareKey(ctx, v)
			if err != nil {
				return err
			}
			hash, err := grouping.Hash(ctx, v)
			if err != nil {
				return err
			}
			for _, old := range buckets[hash] {
				equal, err := grouping.Equal(ctx, old, v)
				if err != nil {
					return err
				}
				if equal {
					return invalidMCVSnapshot()
				}
			}
			buckets[hash] = append(buckets[hash], v)
			data.Dictionaries[d][i] = v
		}
	}
	for i, item := range r.Items {
		copy(data.Items[i].Nulls[:], item.Nulls)
		copy(data.Items[i].Indexes[:], item.Indexes)
		copy(data.Items[i].MarginalCounts[:], item.MarginalCounts)
		data.Items[i].Count = item.Count
	}
	typ, ok := read.LookupType(sqltype.PgMCVList)
	if !ok {
		return invalidMCVSnapshot()
	}
	value, err := BuildExtendedMCV(ctx, typ, fact.Usages[:width], data)
	if err != nil {
		return err
	}
	fact.MCVData, fact.MCV = data, value
	return ctx.Err()
}

// Require every nested field, including false and zero-valued item members.
func validateMCVSnapshotJSON(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 3 || fields["rows"] == nil || fields["dictionaries"] == nil || fields["items"] == nil {
		return invalidMCVSnapshot()
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(fields["items"], &items); err != nil {
		return err
	}
	for _, item := range items {
		if len(item) != 4 || item["nulls"] == nil || item["indexes"] == nil || item["count"] == nil || item["marginal_counts"] == nil {
			return invalidMCVSnapshot()
		}
	}
	return nil
}
