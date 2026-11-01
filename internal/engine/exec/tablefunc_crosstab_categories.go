package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PostgreSQL's category hash stores at most NAMEDATALEN-2 bytes. These keys
// are opaque byte strings: a truncated UTF8 prefix must not be re-decoded.
func crosstabCategoryKey(text string) string {
	if len(text) > 62 {
		return text[:62]
	}
	return text
}

func ValidateCrosstabCategoriesResult(result *sqltype.Descriptor) error {
	if result == nil || !result.IsComposite() || result.CompositeLen() < 2 {
		return crosstabReturnError("Return row must have at least two columns.")
	}
	return nil
}

// drainCrosstabHash finishes SPI evaluation before shape checks or destination
// conversion. Values are immutable; the flat slice owns its row slots and never
// retains the cursor's scratch backing. Malformed widths need only a row count.
func drainCrosstabHash(ctx context.Context, rows RowCursor, retain bool, text string) (source []sqltype.Value, count int, err error) {
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			source = nil
		}
	}()
	width := len(rows.Schema())
	scratch := make([]sqltype.Value, width)
	for {
		if err := ctx.Err(); err != nil {
			return nil, count, err
		}
		present, nextErr := rows.Next(ctx, scratch)
		if nextErr != nil {
			return nil, count, RewriteSQLDiagnostic(nextErr, text)
		}
		if !present {
			return source, count, nil
		}
		count++
		if retain {
			source = append(source, scratch...)
		}
	}
}

// ConsumeCrosstabCategories closes and completely evaluates category SQL before
// checking the nonempty result's width and processing ordered NULL/duplicate keys.
func ConsumeCrosstabCategories(ctx context.Context, rows RowCursor, output sqltype.OutputEnvironment, text string) (map[string]int, error) {
	width := len(rows.Schema())
	source, count, err := drainCrosstabHash(ctx, rows, width == 1, text)
	if err != nil || count == 0 {
		return nil, err
	}
	if width != 1 {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid crosstab categories query", Detail: "The query must return one column."}
	}
	ordinals := make(map[string]int, count)
	for _, value := range source {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if value.IsNull() {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "crosstab category value must not be null"}
		}
		category, err := value.Type().OutputWithEnvironment(ctx, value, output)
		if err != nil {
			return nil, err
		}
		key := crosstabCategoryKey(category)
		if _, exists := ordinals[key]; exists {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: "duplicate category name"}
		}
		ordinals[key] = len(ordinals)
	}
	return ordinals, nil
}

// ConsumeCrosstabHash intentionally does not use single-query identity
// compatibility: every selected final string enters its declared input codec.
func ConsumeCrosstabHash(ctx context.Context, rows RowCursor, result *sqltype.Descriptor, categories map[string]int, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, text string) ([]sqltype.Value, error) {
	width := len(rows.Schema())
	retain := width >= 3 && len(categories) > 0 && result != nil && result.IsComposite() && result.CompositeLen() == width-2+len(categories)
	source, count, err := drainCrosstabHash(ctx, rows, retain, text)
	if err != nil || count == 0 {
		return nil, err
	}
	if len(categories) == 0 {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "crosstab categories query must return at least one row"}
	}
	if width < 3 {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid crosstab source data query", Detail: "The query must return at least 3 columns: row_name, category, and value."}
	}
	if err := ValidateCrosstabCategoriesResult(result); err != nil {
		return nil, err
	}
	if expected := width - 2 + len(categories); result.CompositeLen() != expected {
		return nil, crosstabReturnError(fmt.Sprintf("Return row must have %d columns, not %d.", expected, result.CompositeLen()))
	}
	return pivotCrosstabHash(ctx, source, width, result, categories, input, output)
}

type crosstabString struct {
	text string
	null bool
}

func crosstabOutput(ctx context.Context, value sqltype.Value, output sqltype.OutputEnvironment) (crosstabString, error) {
	if value.IsNull() {
		return crosstabString{null: true}, nil
	}
	text, err := value.Type().OutputWithEnvironment(ctx, value, output)
	return crosstabString{text: text}, err
}

func pivotCrosstabHash(ctx context.Context, source []sqltype.Value, width int, result *sqltype.Descriptor, categories map[string]int, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) ([]sqltype.Value, error) {
	selected := make([]crosstabString, result.CompositeLen())
	var values []sqltype.Value
	var key crosstabString
	group := false
	finish := func() error {
		if !group {
			return nil
		}
		fields := make([]sqltype.Value, len(selected))
		for i, cell := range selected {
			usage := result.CompositeMember(i).Usage
			if cell.null {
				fields[i] = sqltype.Null(usage.Type)
				if err := usage.Type.CheckDomainInput(ctx, fields[i], input); err != nil {
					return err
				}
			} else {
				value, err := usage.Type.InputWithEnvironment(ctx, cell.text, usage.Typmod, input)
				if err != nil {
					return err
				}
				fields[i] = value
			}
		}
		value, err := sqltype.OwnedCompositeValue(result, fields)
		if err != nil {
			return err
		}
		values = append(values, value)
		return nil
	}
	for offset := 0; offset < len(source); offset += width {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := source[offset : offset+width]
		name, err := crosstabOutput(ctx, row[0], output)
		if err != nil {
			return nil, err
		}
		if !group || name != key {
			if err := finish(); err != nil {
				return nil, err
			}
			for i := range selected {
				selected[i] = crosstabString{null: true}
			}
			selected[0] = name
			for i := 1; i < width-2; i++ {
				selected[i], err = crosstabOutput(ctx, row[i], output)
				if err != nil {
					return nil, err
				}
			}
			key, group = name, true
		}
		if row[width-2].IsNull() {
			continue
		}
		category, err := row[width-2].Type().OutputWithEnvironment(ctx, row[width-2], output)
		if err != nil {
			return nil, err
		}
		if ordinal, exists := categories[crosstabCategoryKey(category)]; exists {
			selected[width-2+ordinal], err = crosstabOutput(ctx, row[width-1], output)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return values, nil
}
