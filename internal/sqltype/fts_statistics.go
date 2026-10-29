package sqltype

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/fts"
)

func (v Value) AccumulateFTSStatistics(ctx context.Context, mask byte, into fts.Statistics) error {
	if v.typ == nil || v.typ.BaseType().ID() != TSVector || into == nil {
		return fmt.Errorf("invalid statistics vector authority")
	}
	if v.null {
		return ctx.Err()
	}
	if !v.validFTS() {
		return fmt.Errorf("invalid statistics vector payload")
	}
	return v.ftsVector.AccumulateStatistics(ctx, mask, into)
}
func FTSStatisticsRows(ctx context.Context, counts fts.Statistics, result *Descriptor) ([]Value, error) {
	if result == nil || !result.IsComposite() || result.ID() != 2249 || result.CompositeLen() != 3 {
		return nil, fmt.Errorf("invalid statistics result authority")
	}
	ids := [3]ID{Text, Int4, Int4}
	names := [3]string{"word", "ndoc", "nentry"}
	for i, id := range ids {
		m := result.CompositeMember(i)
		if m.Name != names[i] || m.Attribute != int32(i+1) || m.Usage.Type == nil || m.Usage.Type.ID() != id {
			return nil, fmt.Errorf("invalid statistics result member")
		}
	}
	words := make([]string, 0, len(counts))
	for word := range counts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		words = append(words, word)
	}
	slices.Sort(words)
	rows := make([]Value, len(words))
	for i, word := range words {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count := counts[word]
		documents, err := Integer(result.CompositeMember(1).Usage.Type, int64(count.Documents))
		if err != nil {
			return nil, err
		}
		entries, err := Integer(result.CompositeMember(2).Usage.Type, int64(count.Entries))
		if err != nil {
			return nil, err
		}
		rows[i], err = OwnedCompositeValue(result, []Value{String(result.CompositeMember(0).Usage.Type, word), documents, entries})
		if err != nil {
			return nil, err
		}
	}
	return rows, ctx.Err()
}
