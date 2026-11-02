package contrib

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DblinkBuildSQLInsert admits a runtime-named relation, then uses the ordinary
// SQL executor for conversion, equality, RLS, inheritance and tuple visibility.
// The temporary admission lock outlives both source execution and formatting.
func DblinkBuildSQLInsert[A Authority](h Host[A], n plan.InstalledDblinkBuildSQLInsertExpr, values [5]sqltype.Value) (string, error) {
	text, err := values[0].Text()
	if err != nil {
		return "", err
	}
	target, schema, qualified, release, err := dblinkRelation(h, n.Procedure, n.Ref, text)
	if err != nil {
		return "", err
	}
	defer release()
	ctx := h.Operation().Context()
	keys, count, source, remote := values[1], values[2], values[3], values[4]
	requested, err := count.Integer()
	if err != nil {
		return "", err
	}
	effective := int(min(requested, int64(keys.ArrayLen())))
	if effective <= 0 {
		return "", sqlError("22023", "number of key attributes must be > 0")
	}
	for i := range effective {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		number, err := keys.ArrayElement(i).Integer()
		if err != nil {
			return "", err
		}
		if number <= 0 || number > int64(len(target.Columns)) {
			return "", sqlError("22023", fmt.Sprintf("invalid attribute number %d", number))
		}
	}
	if source.ArrayLen() != effective {
		return "", sqlError("2202E", "source key array length must match number of key attributes")
	}
	if remote.ArrayLen() != effective {
		return "", sqlError("2202E", "target key array length must match number of key attributes")
	}
	_, output := h.Environments()
	query, err := dblinkInsertSourceSQL(ctx, target, schema, qualified, keys, effective, source, output)
	if err != nil {
		return "", err
	}
	// Capture the relation spelling before VOLATILE source effects, directly
	// into the final builder so it cannot be re-quoted under later settings.
	identifier := catalog.DependencyIdentifier
	if output.QuoteAllIdentifiers {
		identifier = catalog.QuoteIdentifier
	}
	var out strings.Builder
	out.WriteString("INSERT INTO ")
	dblinkRelationSQL(&out, target, schema, qualified, identifier)
	consumer, err := dblinkInsertTuple(h, n.Procedure, query)
	if err != nil {
		return "", err
	}
	if consumer.count == 0 {
		return "", sqlError("21000", "source row not found")
	}
	if consumer.count > 1 {
		return "", sqlError("21000", "source criteria matched more than one record")
	}
	// Nested restoration refreshes presentation for escaped source SET effects.
	// Only column identifiers and tuple output use this post-source environment.
	_, output = h.Environments()
	return dblinkInsertSQL(ctx, &out, target, keys, effective, remote, consumer.first, output)
}

// The source command's volatile policy ends before typed output formatting.
func dblinkInsertTuple[A Authority](h Host[A], procedure *catalog.Procedure, query string) (dblinkInsertSource, error) {
	consumer := dblinkInsertSource{ctx: h.Operation().Context()}
	restorePolicy := h.ActivateContribRoutineCommand(procedure)
	defer restorePolicy()
	if err := h.EnterNestedSQL(); err != nil {
		return dblinkInsertSource{}, err
	}
	defer h.LeaveNestedSQL()
	read, statements, err := h.ParseNestedSQL(consumer.ctx, query)
	if err != nil {
		return dblinkInsertSource{}, exec.SPIStatementDiagnostic(h.Operation().CommandError(err), query)
	}
	if len(statements) != 1 {
		return dblinkInsertSource{}, fmt.Errorf("stuntgres: invalid dblink source command")
	}
	request := exec.NestedRequest{Text: query, Diagnose: true, Caller: functions.DblinkInsertName, FreshCommand: true, CloseFailed: true}
	if err := h.ExecuteNestedSQL(statements[0], read, request, &consumer); err != nil {
		return dblinkInsertSource{}, err
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

func dblinkInsertSourceSQL(ctx context.Context, target *catalog.Relation, schema string, qualified bool, keys sqltype.Value, count int, source sqltype.Value, output sqltype.OutputEnvironment) (string, error) {
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
type dblinkInsertSource struct {
	ctx   context.Context
	count uint8
	first []sqltype.Value
}

func (*dblinkInsertSource) NestedCommand(parser.Statement) (bool, error) { return false, nil }
func (*dblinkInsertSource) NestedAnalyzed(parser.Statement) error        { return nil }
func (c *dblinkInsertSource) NestedRows(_ parser.Statement, rows exec.RowResult) (err error) {
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

// out already owns the pre-source INSERT/relation prefix; never copy its builder.
func dblinkInsertSQL(ctx context.Context, out *strings.Builder, target *catalog.Relation, keys sqltype.Value, count int, remote sqltype.Value, row []sqltype.Value, output sqltype.OutputEnvironment) (string, error) {
	identifier := catalog.DependencyIdentifier
	if output.QuoteAllIdentifiers {
		identifier = catalog.QuoteIdentifier
	}
	out.WriteByte('(')
	for i, column := range target.Columns {
		if i != 0 {
			out.WriteByte(',')
		}
		out.WriteString(identifier(column.Name))
	}
	out.WriteString(") VALUES(")
	physical, slot := int32(1), 0
	for i := range target.Columns {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			out.WriteByte(',')
		}
		attribute := target.Attributes[i]
		for physical < attribute {
			if _, dropped := target.ColumnMeta.Dropped[physical]; dropped {
				slot++
			}
			physical++
		}
		if slot >= len(row) {
			return "", fmt.Errorf("stuntgres: invalid dblink source tuple width")
		}
		value := row[slot]
		for j := range count {
			number, _ := keys.ArrayElement(j).Integer()
			if number == int64(i+1) {
				value = remote.ArrayElement(j)
				break
			}
		}
		if err := functions.AppendSQLLiteral(ctx, out, value, output); err != nil {
			return "", err
		}
		slot++
		physical = attribute + 1
	}
	out.WriteByte(')')
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}
