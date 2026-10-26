package exec

import (
	"context"
	"database/sql/driver"
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// The scan and actual mutation-result paths share one codec/domain/typmod
// authority. Nil slots denotes the full declared tuple; nonnil denotes a scan
// plan's precomputed sparse layout, including a genuine zero-width layout.
func (f *ForeignQuerySource) ImportValues(ctx context.Context, row catalog.ForeignRow, slots []int, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, node *plan.SourceNode, source *Source) ([]sqltype.Value, error) {
	width := len(f.Columns)
	if slots != nil {
		width = len(slots)
	}
	if len(row) != width {
		return nil, errors.New("stuntgres: foreign row width does not match its descriptor")
	}
	values := make([]sqltype.Value, len(f.Columns))
	if width < len(f.Columns) {
		for i, column := range f.Columns {
			values[i] = sqltype.Null(column.Usage.Type)
		}
	}
	for i := range row {
		slot := i
		if slots != nil {
			slot = slots[i]
		}
		cell, err := driver.DefaultParameterConverter.ConvertValue(row[i])
		if err != nil {
			return nil, err
		}
		if bytes, ok := cell.([]byte); ok && bytes != nil {
			cell = string(bytes)
		}
		usage := f.Columns[slot].Usage
		value, err := f.ImportValue(ctx, cell, usage, input)
		if err != nil {
			if node != nil {
				err = f.columnDiagnostic(err, node, source, slot)
			}
			return nil, err
		}
		// Parameter input defers ordinary typmods; external table cells do not.
		if !value.IsNull() && (usage.Typmod != -1 || !value.ValidResult(usage)) {
			plan, err := sqltype.PlanCoercion(value.Type().Usage(), usage, sqltype.Assignment)
			if err != nil {
				return nil, err
			}
			value, err = plan.Apply(ctx, value, input, output)
			if err != nil {
				if node != nil {
					err = f.columnDiagnostic(err, node, source, slot)
				}
				return nil, err
			}
		}
		values[slot] = value
	}
	return values, nil
}
