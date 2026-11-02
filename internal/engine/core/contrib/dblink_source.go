package contrib

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// dblinkSourceKeys validates only the used logical live-column prefix.
func dblinkSourceKeys(ctx context.Context, target *catalog.Relation, keys, count, source, remote sqltype.Value) (int, error) {
	requested, err := count.Integer()
	if err != nil {
		return 0, err
	}
	effective := int(min(requested, int64(keys.ArrayLen())))
	if effective <= 0 {
		return 0, sqlError("22023", "number of key attributes must be > 0")
	}
	for i := range effective {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		number, err := keys.ArrayElement(i).Integer()
		if err != nil {
			return 0, err
		}
		if number <= 0 || number > int64(len(target.Columns)) {
			return 0, sqlError("22023", fmt.Sprintf("invalid attribute number %d", number))
		}
	}
	if source.ArrayLen() != effective {
		return 0, sqlError("2202E", "source key array length must match number of key attributes")
	}
	if remote.ArrayLen() != effective {
		return 0, sqlError("2202E", "target key array length must match number of key attributes")
	}
	return effective, nil
}

// The source command's volatile policy ends before typed output formatting.
func dblinkSourceTuple[A Authority](h Host[A], procedure *catalog.Procedure, caller, query string) (dblinkSource, error) {
	consumer := dblinkSource{ctx: h.Operation().Context()}
	restorePolicy := h.ActivateContribRoutineCommand(procedure)
	defer restorePolicy()
	if err := h.EnterNestedSQL(); err != nil {
		return dblinkSource{}, err
	}
	defer h.LeaveNestedSQL()
	read, statements, err := h.ParseNestedSQL(consumer.ctx, query)
	if err != nil {
		return dblinkSource{}, exec.SPIStatementDiagnostic(h.Operation().CommandError(err), query)
	}
	if len(statements) != 1 {
		return dblinkSource{}, fmt.Errorf("stuntgres: invalid dblink source command")
	}
	request := exec.NestedRequest{Text: query, Diagnose: true, Caller: caller, FreshCommand: true, CloseFailed: true}
	if err := h.ExecuteNestedSQL(statements[0], read, request, &consumer); err != nil {
		return dblinkSource{}, err
	}
	return consumer, nil
}

func dblinkRelationSQL(out *strings.Builder, target *catalog.Relation, schema string, qualified bool, identifier func(string) string) {
	if qualified {
		out.WriteString(identifier(schema))
		out.WriteByte('.')
	}
	out.WriteString(identifier(target.Name()))
}

func dblinkSourceSQL(ctx context.Context, target *catalog.Relation, schema string, qualified bool, keys sqltype.Value, count int, source sqltype.Value, output sqltype.OutputEnvironment) (string, error) {
	identifier := catalog.DependencyIdentifier
	if output.QuoteAllIdentifiers {
		identifier = catalog.QuoteIdentifier
	}
	var out strings.Builder
	out.WriteString("SELECT ")
	// Physical dropped slots remain NULL in the source tuple, as in SPI's
	// released SELECT. Catalog/virtual relations can have non-user attnums;
	// only the actual dropped metadata introduces holes.
	physical := int32(1)
	for i, column := range target.Columns {
		if i != 0 {
			out.WriteString(", ")
		}
		attribute := target.Attributes[i]
		for physical < attribute {
			if _, dropped := target.ColumnMeta.Dropped[physical]; dropped {
				out.WriteString("NULL, ")
			}
			physical++
		}
		out.WriteString(identifier(column.Name))
		physical = attribute + 1
	}
	for ; physical <= target.ColumnMeta.HighWater; physical++ {
		if _, dropped := target.ColumnMeta.Dropped[physical]; dropped {
			out.WriteString(", NULL")
		}
	}
	out.WriteString(" FROM ")
	dblinkRelationSQL(&out, target, schema, qualified, identifier)
	out.WriteString(" WHERE ")
	for i := range count {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			out.WriteString(" AND ")
		}
		number, _ := keys.ArrayElement(i).Integer()
		out.WriteString(identifier(target.Columns[number-1].Name))
		value := source.ArrayElement(i)
		if value.IsNull() {
			out.WriteString(" IS NULL")
		} else {
			out.WriteString(" = ")
			if err := functions.AppendSQLLiteral(ctx, &out, value, output); err != nil {
				return "", err
			}
		}
	}
	return out.String(), ctx.Err()
}

// Keep only an owned first-row vector. Values themselves are immutable typed
// datums. Saturate cardinality but drain every row so late errors/effects win.
type dblinkSource struct {
	ctx   context.Context
	count uint8
	first []sqltype.Value
}

func (*dblinkSource) NestedCommand(parser.Statement) (bool, error) { return false, nil }
func (*dblinkSource) NestedAnalyzed(parser.Statement) error        { return nil }
func (c *dblinkSource) NestedRows(_ parser.Statement, rows exec.RowResult) (err error) {
	if rows == nil {
		return fmt.Errorf("stuntgres: dblink source returned no cursor")
	}
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}()
	buffer := make([]sqltype.Value, len(rows.Schema()))
	for {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		present, err := rows.Next(c.ctx, buffer)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
		if c.count == 0 {
			c.first = buffer
			buffer = make([]sqltype.Value, len(buffer))
		}
		if c.count < 2 {
			c.count++
		}
	}
}

// Walk physical projection slots once, skipping only genuine dropped metadata.
func dblinkSourceValue(target *catalog.Relation, row []sqltype.Value, column int, physical *int32, slot *int) (sqltype.Value, error) {
	attribute := target.Attributes[column]
	for *physical < attribute {
		if _, dropped := target.ColumnMeta.Dropped[*physical]; dropped {
			*slot++
		}
		*physical++
	}
	if *slot >= len(row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid dblink source tuple width")
	}
	value := row[*slot]
	*slot++
	*physical = attribute + 1
	return value, nil
}

// Repeated key attributes replace a SET/VALUES column at the first occurrence.
func dblinkTargetValue(keys sqltype.Value, count int, remote sqltype.Value, column int, source sqltype.Value) sqltype.Value {
	for j := range count {
		number, _ := keys.ArrayElement(j).Integer()
		if number == int64(column+1) {
			return remote.ArrayElement(j)
		}
	}
	return source
}
