package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// StatisticsSnapshotCodec is the existing control renderer/input authority
// supplied by the composing engine. Storage never binds SQL or owns providers.
type StatisticsSnapshotCodec interface {
	Expression(*catalog.Relation, expr.Expr) (string, error)
	Encode(sqltype.Usage, sqltype.Value) (json.RawMessage, error)
	Decode(sqltype.Usage, json.RawMessage) (sqltype.Value, error)
}

type StatisticsExpressionSnapshot struct {
	Definition    string                  `json:"definition"`
	Usage         NDistinctUsageRecord    `json:"usage"`
	Inputs        []int32                 `json:"inputs"`
	InputUsages   []NDistinctUsageRecord  `json:"input_usages"`
	Rows          uint64                  `json:"rows"`
	NullCount     uint64                  `json:"null_count"`
	DistinctCount uint64                  `json:"distinct_count"`
	DistinctKnown bool                    `json:"distinct_known"`
	AverageWidth  int32                   `json:"average_width"`
	MCV           []StatisticsMCVSnapshot `json:"mcv"`
	Histogram     []json.RawMessage       `json:"histogram"`
}

type StatisticsMCVSnapshot struct {
	Value json.RawMessage `json:"value"`
	Count uint64          `json:"count"`
}

func captureExpressionSnapshots(ctx context.Context, read catalog.Read, relation *catalog.Relation, fact *ExtendedStatistics, codec StatisticsSnapshotCodec) ([]StatisticsExpressionSnapshot, error) {
	records := make([]StatisticsExpressionSnapshot, fact.ExpressionLen())
	for i := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if codec == nil {
			return nil, fmt.Errorf("missing statistics snapshot codec")
		}
		recipe, column := fact.Recipes.At(i), fact.Expression(i)
		r := &records[i]
		var err error
		r.Definition, err = codec.Expression(relation, recipe.Definition())
		if err != nil {
			return nil, err
		}
		r.Usage = ndistinctUsageRecord(read, column.Usage)
		r.Inputs = make([]int32, recipe.InputLen())
		r.InputUsages = make([]NDistinctUsageRecord, len(r.Inputs))
		for j := range r.Inputs {
			attribute := recipe.Input(j).Address.SubID
			slot := relation.IndexForAttribute(attribute)
			if slot < 0 {
				return nil, fmt.Errorf("missing statistics input")
			}
			r.Inputs[j], r.InputUsages[j] = attribute, ndistinctUsageRecord(read, relation.Columns[slot].Usage)
		}
		r.Rows, r.NullCount, r.DistinctCount, r.DistinctKnown = column.Rows, column.NullCount, column.DistinctCount, column.DistinctKnown
		r.AverageWidth = column.AverageWidth
		r.MCV = make([]StatisticsMCVSnapshot, len(column.MCV))
		for j, mcv := range column.MCV {
			r.MCV[j].Count = mcv.Count
			r.MCV[j].Value, err = codec.Encode(column.Usage, mcv.Value)
			if err != nil {
				return nil, err
			}
		}
		r.Histogram = make([]json.RawMessage, len(column.Histogram))
		for j, value := range column.Histogram {
			r.Histogram[j], err = codec.Encode(column.Usage, value)
			if err != nil {
				return nil, err
			}
		}
	}
	return records, nil
}

func (r StatisticsExpressionSnapshot) validate() error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid expression statistics snapshot") }
	if r.Definition == "" || r.Usage.TypeSchema == "" || r.Usage.TypeName == "" || r.Rows == 0 || r.Rows > math.MaxInt32 || r.NullCount > r.Rows || r.DistinctCount > r.Rows-r.NullCount || len(r.Inputs) != len(r.InputUsages) || len(r.MCV) > StatisticsTarget || len(r.Histogram) > StatisticsTarget+1 {
		return invalid()
	}
	if r.AverageWidth < 0 {
		return invalid()
	}
	if !r.DistinctKnown && (r.DistinctCount != 0 || len(r.MCV) != 0 || len(r.Histogram) != 0) {
		return invalid()
	}
	if r.DistinctKnown && ((r.Rows == r.NullCount) != (r.DistinctCount == 0)) {
		return invalid()
	}
	seen := make(map[int32]bool, len(r.Inputs))
	for i, input := range r.Inputs {
		if input <= 0 || seen[input] || r.InputUsages[i].TypeSchema == "" || r.InputUsages[i].TypeName == "" {
			return invalid()
		}
		seen[input] = true
	}
	var total uint64
	for _, mcv := range r.MCV {
		if mcv.Count < 2 || mcv.Count > r.Rows-r.NullCount || total > r.Rows-r.NullCount-mcv.Count {
			return invalid()
		}
		total += mcv.Count
	}
	if uint64(len(r.MCV)) > r.DistinctCount || len(r.Histogram) != 0 && uint64(len(r.MCV)) == r.DistinctCount {
		return invalid()
	}
	return nil
}

func restoreExpressionSnapshots(ctx context.Context, read catalog.Read, relation *catalog.Relation, d *catalog.Statistics, records []StatisticsExpressionSnapshot, fact *ExtendedStatistics, codec StatisticsSnapshotCodec) error {
	if len(records) != d.Expressions.Len() {
		return fmt.Errorf("stuntgres: incompatible expression statistics snapshot")
	}
	fact.Recipes = d.Expressions
	fact.expressions = make([]*ColumnStatistics, len(records))
	inputs := make(map[int32]bool)
	for i, r := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if codec == nil {
			return fmt.Errorf("missing statistics snapshot codec")
		}
		recipe := d.Expressions.At(i)
		definition, err := codec.Expression(relation, recipe.Definition())
		if err != nil {
			return err
		}
		if definition != r.Definition || ndistinctUsageRecord(read, recipe.Usage()) != r.Usage || recipe.InputLen() != len(r.Inputs) {
			return fmt.Errorf("stuntgres: incompatible expression statistics recipe")
		}
		for j, attribute := range r.Inputs {
			slot := relation.IndexForAttribute(attribute)
			if slot < 0 || recipe.Input(j).Address.SubID != attribute || ndistinctUsageRecord(read, relation.Columns[slot].Usage) != r.InputUsages[j] {
				return fmt.Errorf("stuntgres: incompatible statistics input")
			}
			if !inputs[attribute] {
				fact.inputs = append(fact.inputs, StatisticsColumn{Key: StatisticsKey{Attribute: attribute}, Usage: relation.Columns[slot].Usage})
				inputs[attribute] = true
			}
		}
		column := &ColumnStatistics{Usage: recipe.Usage(), Rows: r.Rows, NullCount: r.NullCount, DistinctCount: r.DistinctCount, DistinctKnown: r.DistinctKnown}
		column.AverageWidth = r.AverageWidth
		column.MCV = make([]StatisticMCV, len(r.MCV))
		for j, mcv := range r.MCV {
			value, err := codec.Decode(column.Usage, mcv.Value)
			if err != nil {
				return err
			}
			if value.IsNull() || value.Type() != column.Usage.Type {
				return fmt.Errorf("stuntgres: invalid statistics MCV type")
			}
			column.MCV[j] = StatisticMCV{Value: value, Count: mcv.Count}
		}
		column.Histogram = make([]sqltype.Value, len(r.Histogram))
		for j, encoded := range r.Histogram {
			value, err := codec.Decode(column.Usage, encoded)
			if err != nil {
				return err
			}
			if value.IsNull() || value.Type() != column.Usage.Type {
				return fmt.Errorf("stuntgres: invalid statistics histogram type")
			}
			column.Histogram[j] = value
		}
		if err := validateRestoredExpressionValues(ctx, column); err != nil {
			return err
		}
		fact.expressions[i], fact.Usages[d.Selection.Len()+i] = column, column.Usage
	}
	return ctx.Err()
}

func validateRestoredExpressionValues(ctx context.Context, column *ColumnStatistics) error {
	if len(column.MCV) == 0 && len(column.Histogram) == 0 {
		return ctx.Err()
	}
	grouping, err := sqltype.BindGrouping(column.Usage)
	if err != nil {
		return err
	}
	for i, mcv := range column.MCV {
		for j := range i {
			equal, err := grouping.Equal(ctx, column.MCV[j].Value, mcv.Value)
			if err != nil {
				return err
			}
			if equal {
				return fmt.Errorf("stuntgres: duplicate statistics MCV")
			}
		}
	}
	if len(column.Histogram) != 0 {
		ordering, err := sqltype.BindOrdering(column.Usage)
		if err != nil {
			return err
		}
		for i := 1; i < len(column.Histogram); i++ {
			order, err := ordering.Compare(ctx, column.Histogram[i-1], column.Histogram[i], sqltype.InputEnvironment{})
			if err != nil {
				return err
			}
			if order > 0 {
				return fmt.Errorf("stuntgres: unordered statistics histogram")
			}
		}
	}
	return ctx.Err()
}
