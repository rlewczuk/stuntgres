package exec

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func crosstabTypeName(u sqltype.Usage, typeName func(sqltype.ID) string) string {
	// Catalog naming owns visibility and quoting, including recursive array
	// element names. Only builtin typmods need the declaration spelling below.
	if u.Typmod < 0 || !u.Type.IsBuiltin() {
		return typeName(u.Type.ID())
	}
	declaration, err := u.Declaration()
	if err != nil {
		return typeName(u.Type.ID())
	}
	name := strings.ToLower(declaration)
	if u.Type.ID() == sqltype.VarChar {
		return strings.Replace(name, "varchar", "character varying", 1)
	}
	return name
}
func crosstabReturnError(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "invalid crosstab return type", Detail: detail}
}

// CrosstabCompatibility compares declared identities, not coercion paths. An
// unconstrained return typmod accepts a constrained source, never vice versa.
func CrosstabCompatibility(source []catalog.Column, result *sqltype.Descriptor, name func(sqltype.ID) string) error {
	if len(source) != 3 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid crosstab source data query", Detail: "The query must return 3 columns: row_name, category, and value."}
	}
	if result == nil || !result.IsComposite() || result.CompositeLen() < 2 {
		return crosstabReturnError("Return row must have at least two columns.")
	}
	for i := range result.CompositeLen() {
		src := source[2].Usage
		if i == 0 {
			src = source[0].Usage
		}
		dst := result.CompositeMember(i).Usage
		if src.Type.ID() != dst.Type.ID() || dst.Typmod >= 0 && src.Typmod != dst.Typmod {
			if i == 0 {
				return crosstabReturnError(fmt.Sprintf("Source row_name datatype %s does not match return row_name datatype %s.", crosstabTypeName(src, name), crosstabTypeName(dst, name)))
			}
			return crosstabReturnError(fmt.Sprintf("Source value datatype %s does not match return value datatype %s in column %d.", crosstabTypeName(src, name), crosstabTypeName(dst, name), i+1))
		}
	}
	return nil
}

// ConsumeCrosstab closes the source before returning owned immutable tuples.
// Complete source evaluation precedes compatibility, like SPI_execute's result.
func ConsumeCrosstab(ctx context.Context, rows RowCursor, result *sqltype.Descriptor, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, sourceSQL string, name func(sqltype.ID) string) (values []sqltype.Value, err error) {
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			values = nil
		}
	}()
	schema := rows.Schema()
	width := len(schema)
	scratch := make([]sqltype.Value, width)
	var source []sqltype.Value
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		present, e := rows.Next(ctx, scratch)
		if e != nil {
			return nil, RewriteSQLDiagnostic(e, sourceSQL)
		}
		if !present {
			break
		}
		count++
		if width == 3 {
			source = append(source, scratch...)
		}
	}
	if count == 0 {
		return nil, nil
	}
	if err := CrosstabCompatibility(schema, result, name); err != nil {
		return nil, err
	}
	return pivotCrosstab(ctx, source, result, input, output)
}

func pivotCrosstab(ctx context.Context, source []sqltype.Value, result *sqltype.Descriptor, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) ([]sqltype.Value, error) {
	var rows []sqltype.Value
	var fields []sqltype.Value
	var key string
	var nullKey bool
	slot := 0
	finish := func() error {
		if fields == nil {
			return nil
		}
		value, err := sqltype.OwnedCompositeValue(result, fields)
		if err != nil {
			return err
		}
		rows = append(rows, value)
		return nil
	}
	convert := func(value sqltype.Value, usage sqltype.Usage) (sqltype.Value, error) {
		if value.IsNull() {
			return sqltype.Null(usage.Type), nil
		}
		text, err := value.Type().OutputWithEnvironment(ctx, value, output)
		if err != nil {
			return sqltype.Value{}, err
		}
		return usage.Type.InputWithEnvironment(ctx, text, usage.Typmod, input)
	}
	for i := 0; i < len(source); i += 3 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := source[i]
		text := ""
		if !name.IsNull() {
			var err error
			text, err = name.Type().OutputWithEnvironment(ctx, name, output)
			if err != nil {
				return nil, err
			}
		}
		if fields == nil || nullKey != name.IsNull() || key != text {
			if err := finish(); err != nil {
				return nil, err
			}
			fields = make([]sqltype.Value, result.CompositeLen())
			for j := range fields {
				fields[j] = sqltype.Null(result.CompositeMember(j).Usage.Type)
			}
			var err error
			fields[0], err = convert(name, result.CompositeMember(0).Usage)
			if err != nil {
				return nil, err
			}
			key, nullKey, slot = text, name.IsNull(), 1
		}
		if slot < len(fields) {
			var err error
			fields[slot], err = convert(source[i+2], result.CompositeMember(slot).Usage)
			if err != nil {
				return nil, err
			}
			slot++
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return rows, nil
}
