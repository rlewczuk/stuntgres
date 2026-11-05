package sqltype

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/xml"
)

// XML2Table converts a completely materialized two-column source using actual
// source OUTPUT and declared record-member INPUT, including NULL domain checks.
func XML2Table(ctx context.Context, source []Value, result *Descriptor, paths string, input InputEnvironment, output OutputEnvironment, notice func(sqlstate.Error)) ([]Value, error) {
	if result == nil || !result.IsComposite() || result.CompositeLen() < 1 || len(source)%2 != 0 {
		return nil, fmt.Errorf("invalid xpath_table materialization")
	}
	table, err := xml.NewLegacyXPathTable(paths, result.CompositeLen()-1)
	if err != nil {
		return nil, err
	}
	var rows []Value
	for offset := 0; offset < len(source); offset += 2 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, document := source[offset], source[offset+1]
		var keyText, documentText string
		if !key.IsNull() {
			keyText, err = key.Type().OutputWithEnvironment(ctx, key, output)
			if err != nil {
				return nil, err
			}
		}
		var documentInput *string
		if !document.IsNull() {
			documentText, err = document.Type().OutputWithEnvironment(ctx, document, output)
			if err != nil {
				return nil, err
			}
			documentInput = &documentText
		}
		err = table.Document(ctx, documentInput, func(cells []xml.LegacyCell) error {
			fields := make([]Value, result.CompositeLen())
			for i := range fields {
				if err := ctx.Err(); err != nil {
					return err
				}
				usage := result.CompositeMember(i).Usage
				text, null := keyText, key.IsNull()
				if i > 0 {
					text, null = cells[i-1].Text, cells[i-1].Null
				}
				if null {
					fields[i] = Null(usage.Type)
					err = usage.Type.CheckDomainInput(ctx, fields[i], input)
				} else {
					fields[i], err = usage.Type.InputWithEnvironment(ctx, text, usage.Typmod, input)
				}
				if err != nil {
					return err
				}
			}
			value, err := OwnedCompositeValue(result, fields)
			if err == nil {
				rows = append(rows, value)
			}
			return err
		}, func(kind int) {
			message := "unsupported XQuery result: 3"
			if kind == 2 {
				message = "unsupported XQuery result: 2"
			}
			notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
		})
		if err != nil {
			var legacy *xml.LegacyXPathError
			if errors.As(err, &legacy) {
				return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "10608", Message: legacy.Error(), Detail: legacy.Detail}
			}
			return nil, xmlError(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
