package typedef

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func rangeDumpSQL(ctx context.Context, p dump.Presenter, view catalog.Read, d *catalog.Type, qualified string) (string, error) {
	definition, ok := d.SQLType.RangeDefinition()
	if !ok {
		return "", fmt.Errorf("stuntgres: missing range dump definition")
	}
	subtype, err := p.TypeDeclaration(view, nil, definition.Subtype)
	if err != nil {
		return "", err
	}
	class, ok := view.OperatorClass(ids.OID(definition.Class.OID()))
	if !ok {
		return "", fmt.Errorf("stuntgres: missing range dump class")
	}
	multi, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.MultirangeType().ID())})
	if !ok {
		return "", fmt.Errorf("stuntgres: missing range dump companion")
	}
	name := func(d catalog.Descriptor) string {
		return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
	}
	var out strings.Builder
	out.WriteString("CREATE TYPE " + qualified + " AS RANGE (SUBTYPE = " + subtype + ", SUBTYPE_OPCLASS = " + name(class))
	if definition.Subtype.Collation.ID != 0 {
		collation := CollationSQLName(view, definition.Subtype.Collation.ID)
		if collation == "" {
			return "", fmt.Errorf("stuntgres: missing range dump collation")
		}
		out.WriteString(", COLLATION = " + collation)
	}
	if definition.Diff != 0 {
		procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(definition.Diff)})
		if !ok {
			return "", fmt.Errorf("stuntgres: missing range dump diff")
		}
		out.WriteString(", SUBTYPE_DIFF = " + name(procedure))
	}
	out.WriteString(", MULTIRANGE_TYPE_NAME = " + name(multi) + ");\n")
	return out.String(), ctx.Err()
}
