package contrib

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// DblinkBuildSQLDelete captures only the invocation's catalog metadata. It never
// opens relation storage or retains authority, locks, hosts or physical handles.
func DblinkBuildSQLDelete[A Authority](h Host[A], n plan.InstalledDblinkBuildSQLDeleteExpr, values [4]sqltype.Value) (string, error) {
	text, err := values[0].Text()
	if err != nil {
		return "", err
	}
	target, schema, qualified, release, err := dblinkRelation(h, n.Procedure, n.Ref, text)
	if err != nil {
		return "", err
	}
	defer release()
	_, output := h.Environments()
	return DblinkDeleteSQL(h.Operation().Context(), target, schema, qualified, values[1], values[2], values[3], output)
}

// DblinkDeleteSQL consumes detached immutable metadata and typed flat payloads.
// It is deliberately independent of storage, source tuples and statement hosts.
func DblinkDeleteSQL(ctx context.Context, target *catalog.Relation, schema string, qualified bool, keys, count, values sqltype.Value, output sqltype.OutputEnvironment) (string, error) {
	requested, err := count.Integer()
	if err != nil {
		return "", err
	}
	effective := min(requested, int64(keys.ArrayLen()))
	if effective <= 0 {
		return "", sqlError("22023", "number of key attributes must be > 0")
	}
	// Validate the used logical prefix before target cardinality or allocation.
	for i := range int(effective) {
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
	if values.ArrayLen() != int(effective) {
		return "", sqlError("2202E", "target key array length must match number of key attributes")
	}
	identifier := catalog.DependencyIdentifier
	if output.QuoteAllIdentifiers {
		identifier = catalog.QuoteIdentifier
	}
	var out strings.Builder
	out.WriteString("DELETE FROM ")
	if qualified {
		out.WriteString(identifier(schema))
		out.WriteByte('.')
	}
	out.WriteString(identifier(target.Name()))
	out.WriteString(" WHERE ")
	for i := range int(effective) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			out.WriteString(" AND ")
		}
		number, _ := keys.ArrayElement(i).Integer() // used prefix validated above
		out.WriteString(identifier(target.Columns[number-1].Name))
		value := values.ArrayElement(i)
		if value.IsNull() {
			out.WriteString(" IS NULL")
		} else {
			out.WriteString(" = ")
			if err := functions.AppendSQLLiteral(ctx, &out, value, output); err != nil {
				return "", err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}
