package stats

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Project the retained construction facts, not the binary pg_mcv_list datum.
// Each dictionary value is rendered once under this capture's output settings.
func extendedViewMCV(ctx context.Context, source *catalog.Relation, fact *storage.ExtendedStatistics, row []sqltype.Value, output sqltype.OutputEnvironment) error {
	data := fact.MCVData
	if data == nil {
		if fact.MCV.Type() != nil && !fact.MCV.IsNull() {
			return fmt.Errorf("stuntgres: MCV construction facts unavailable")
		}
		return nil
	}
	width := fact.Selection.DimensionLen()
	if width < 1 || width > 8 || data.Rows == 0 || len(data.Items) == 0 {
		return fmt.Errorf("stuntgres: invalid MCV construction fact")
	}
	textType := source.Columns[11].Usage.Type.Element()
	boolType := source.Columns[12].Usage.Type.Element()
	numberType := source.Columns[13].Usage.Type.Element()
	var dictionaries [8][]sqltype.Value
	for dimension := range width {
		dictionaries[dimension] = make([]sqltype.Value, len(data.Dictionaries[dimension]))
		for i, value := range data.Dictionaries[dimension] {
			if value.IsNull() || !value.ValidResult(fact.Usages[dimension]) {
				return fmt.Errorf("stuntgres: invalid MCV dictionary value")
			}
			text, err := value.Type().OutputWithEnvironment(ctx, value, output)
			if err != nil {
				return err
			}
			dictionaries[dimension][i] = sqltype.String(textType, text)
		}
	}
	values := make([]sqltype.Value, len(data.Items)*width)
	nulls := make([]sqltype.Value, len(values))
	frequencies := make([]sqltype.Value, len(data.Items))
	baselines := make([]sqltype.Value, len(data.Items))
	for i, item := range data.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.Count == 0 || item.Count > data.Rows {
			return fmt.Errorf("stuntgres: invalid MCV count")
		}
		baseline := 1.0
		for dimension := range width {
			if item.MarginalCounts[dimension] < item.Count || item.MarginalCounts[dimension] > data.Rows {
				return fmt.Errorf("stuntgres: invalid MCV marginal count")
			}
			baseline *= float64(item.MarginalCounts[dimension]) / float64(data.Rows)
			slot := i*width + dimension
			nulls[slot] = sqltype.Boolean(boolType, item.Nulls[dimension])
			if item.Nulls[dimension] {
				values[slot] = sqltype.Null(textType)
			} else {
				index := int(item.Indexes[dimension])
				if index >= len(dictionaries[dimension]) {
					return fmt.Errorf("stuntgres: invalid MCV dictionary index")
				}
				values[slot] = dictionaries[dimension][index]
			}
		}
		frequencies[i], _ = sqltype.Float(numberType, float64(item.Count)/float64(data.Rows))
		baselines[i], _ = sqltype.Float(numberType, baseline)
	}
	dimensions := []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(data.Items))}, {Lower: 1, Length: int32(width)}}
	var err error
	row[11], err = sqltype.OwnedArrayValue(source.Columns[11].Usage.Type, dimensions, values)
	if err != nil {
		return err
	}
	row[12], err = sqltype.OwnedArrayValue(source.Columns[12].Usage.Type, dimensions, nulls)
	if err != nil {
		return err
	}
	row[13], err = statisticsViewArray(source.Columns[13].Usage.Type, frequencies)
	if err != nil {
		return err
	}
	row[14], err = statisticsViewArray(source.Columns[14].Usage.Type, baselines)
	return err
}
