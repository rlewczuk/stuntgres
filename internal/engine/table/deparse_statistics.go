package table

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// deparseStatistics borrows only the inquiry's captured authority. Public
// presentation deliberately differs from the fully qualified dump recipe.
func deparseStatistics(c *exec.Context, in DeparseInputs, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := c.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if result == nil || result.ID() != sqltype.Text || values[0].Type() == nil || values[0].Type().ID() != sqltype.Oid {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid statistics deparse signature")
	}
	if values[0].IsNull() {
		return sqltype.Null(result), nil
	}
	id, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	view := in.Catalog
	if view.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statistics deparse catalog")
	}
	definition, ok := view.Statistics(ids.ObjectAddress{ClassID: ids.StatisticExtClassID, ObjectID: ids.OID(id)})
	if !ok {
		return sqltype.Null(result), nil
	}
	if err := (catalog.StatisticsProvider{}).Validate(view, definition); err != nil {
		return sqltype.Value{}, err
	}
	namespace, _ := view.Namespace(definition.Namespace())
	relation, _ := view.Relation(definition.Relation.Address)
	r := stored.Deparser{View: view, Relation: relation, Pretty: true, Path: in.Path, Output: in.Output,
		NonstandardStrings: in.NonstandardStrings, TemporaryOwner: in.TemporaryOwner}
	var text strings.Builder
	text.WriteString("CREATE STATISTICS ")
	namespaceName := namespace.Name()
	if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary == in.TemporaryOwner {
		namespaceName = "pg_temp"
	}
	text.WriteString(r.Identifier(namespaceName))
	text.WriteByte('.')
	text.WriteString(r.Identifier(definition.Name()))
	kinds := definition.Selection.Kinds()
	if strings.TrimSuffix(kinds, "e") != "dfm" && definition.Selection.DimensionLen() > 1 {
		text.WriteString(" (")
		separator := ""
		for _, kind := range []struct {
			flag byte
			name string
		}{{'d', "ndistinct"}, {'f', "dependencies"}, {'m', "mcv"}} {
			if strings.IndexByte(kinds, kind.flag) >= 0 {
				text.WriteString(separator)
				text.WriteString(kind.name)
				separator = ", "
			}
		}
		text.WriteByte(')')
	}
	text.WriteString(" ON ")
	for i := range definition.Selection.Len() {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		name, err := r.Attribute(relation, definition.Selection.Key(i))
		if err != nil {
			return sqltype.Value{}, err
		}
		if i != 0 {
			text.WriteString(", ")
		}
		text.WriteString(name)
	}
	for i := range definition.Expressions.Len() {
		part, err := r.StatisticsElement(c.Context, definition.Expressions.At(i).Definition())
		if err != nil {
			return sqltype.Value{}, err
		}
		if definition.Selection.Len()+i != 0 {
			text.WriteString(", ")
		}
		text.WriteString(part)
	}
	name, err := r.RelationName(relation, false)
	if err != nil {
		return sqltype.Value{}, err
	}
	text.WriteString(" FROM ")
	text.WriteString(name)
	if err := c.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, text.String()), nil
}
