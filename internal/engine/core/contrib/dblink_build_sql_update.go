package contrib

import (
	"context"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// DblinkBuildSQLUpdate admits a runtime-named relation, then uses the ordinary
// SQL executor for conversion, equality, RLS, inheritance and tuple visibility.
// The temporary admission lock outlives both source execution and formatting.
func DblinkBuildSQLUpdate[A Authority](h Host[A], n plan.InstalledDblinkBuildSQLUpdateExpr, values [5]sqltype.Value) (string, error) {
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
	effective, err := dblinkSourceKeys(ctx, target, keys, count, source, remote)
	if err != nil {
		return "", err
	}
	_, output := h.Environments()
	query, err := dblinkSourceSQL(ctx, target, schema, qualified, keys, effective, source, output)
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
	out.WriteString("UPDATE ")
	dblinkRelationSQL(&out, target, schema, qualified, identifier)
	consumer, err := dblinkSourceTuple(h, n.Procedure, functions.DblinkUpdateName, query)
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
	return dblinkUpdateSQL(ctx, &out, target, keys, effective, remote, consumer.first, output)
}

// out owns the pre-source relation spelling; columns and values use post-source output.
func dblinkUpdateSQL(ctx context.Context, out *strings.Builder, target *catalog.Relation, keys sqltype.Value, count int, remote sqltype.Value, row []sqltype.Value, output sqltype.OutputEnvironment) (string, error) {
	identifier := catalog.DependencyIdentifier
	if output.QuoteAllIdentifiers {
		identifier = catalog.QuoteIdentifier
	}
	out.WriteString(" SET ")
	physical, slot := int32(1), 0
	for i, column := range target.Columns {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			out.WriteString(", ")
		}
		out.WriteString(identifier(column.Name))
		out.WriteString(" = ")
		value, err := dblinkSourceValue(target, row, i, &physical, &slot)
		if err != nil {
			return "", err
		}
		value = dblinkTargetValue(keys, count, remote, i, value)
		if err := functions.AppendSQLLiteral(ctx, out, value, output); err != nil {
			return "", err
		}
	}
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
		value := remote.ArrayElement(i)
		if value.IsNull() {
			out.WriteString(" IS NULL")
		} else {
			out.WriteString(" = ")
			if err := functions.AppendSQLLiteral(ctx, out, value, output); err != nil {
				return "", err
			}
		}
	}
	return out.String(), ctx.Err()
}
