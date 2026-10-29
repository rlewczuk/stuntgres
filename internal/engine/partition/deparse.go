package partition

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// Deparse presents a partition definition using only the executing inquiry's
// captured catalog and presentation environment. It acquires no locks and
// retains neither the catalog nor the rendered value across executions.
func Deparse(c *exec.Context, in table.DeparseInputs, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := c.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if result == nil || result.ID() != sqltype.Text || (signature != functions.DeparsePartitionKey && signature != functions.DeparsePartitionConstraint) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid partition deparse signature")
	}
	if values[0].IsNull() {
		return sqltype.Null(result), nil
	}
	id, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	if in.Catalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing partition deparse catalog")
	}
	relation, ok := in.Catalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)})
	if !ok {
		return sqltype.Null(result), nil
	}
	r := stored.Deparser{View: in.Catalog, Relation: relation, Path: in.Path, Output: in.Output, NonstandardStrings: in.NonstandardStrings}
	var text string
	if signature == functions.DeparsePartitionKey {
		text, err = deparseKey(c.Context, r, relation)
	} else {
		text, err = deparseConstraint(c.Context, r, relation)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if text == "" {
		return sqltype.Null(result), nil
	}
	return sqltype.String(result, text), nil
}

// qualification owns only presentation topology. Leaves are already rendered
// through the ordinary typed stored renderer. AND/OR lists deliberately remain
// flat: partition quals are concatenated, not algebraically simplified.
type qualification struct {
	text string
	op   string
	args []qualification
}

func qualGroup(op string, args []qualification) qualification {
	if len(args) == 1 {
		return args[0]
	}
	return qualification{op: op, args: args}
}

func (q qualification) appendSQL(ctx context.Context, out *strings.Builder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.op == "" {
		out.WriteString(q.text)
		return nil
	}
	out.WriteByte('(')
	if q.op == "NOT" {
		out.WriteString("NOT ")
	}
	for i, arg := range q.args {
		if i != 0 {
			out.WriteString(" " + q.op + " ")
		}
		if err := arg.appendSQL(ctx, out); err != nil {
			return err
		}
	}
	out.WriteByte(')')
	return nil
}

// Keep typed descriptor failures distinct from a relation with no definition.
func invalidDeparse() error { return fmt.Errorf("stuntgres: invalid partition deparse descriptor") }
