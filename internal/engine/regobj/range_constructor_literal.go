package regobj

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func rangeConstructorName(view catalog.Read, reader catalog.DefinitionReader, t *sqltype.Descriptor, form catalog.RangeConstructorForm) (string, error) {
	owner, ok := catalog.DescriptorAt(view, reader, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.RangeType().ID())})
	if !ok {
		return "", fmt.Errorf("missing range constructor namespace owner")
	}
	ns, ok := catalog.DescriptorAt(view, reader, owner.Namespace())
	if !ok {
		return "", fmt.Errorf("missing range constructor namespace")
	}
	for d := range catalog.DefinitionProceduresNamedAt(view, reader, owner.Namespace(), t.Name()) {
		if d.RangeConstructor != nil && d.RangeConstructor.Type == t && d.RangeConstructor.Form == form {
			return catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name()), nil
		}
	}
	return "", fmt.Errorf("missing actual generated range constructor")
}

func rangeConstructorLiteral(ctx context.Context, view catalog.Read, reader catalog.DefinitionReader, n plan.LiteralExpr, acl sqltype.ACLItemFormatter) (string, error) {
	t, v := n.Usage.Type, n.Value
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.IsMultirange() {
		form := catalog.MultirangeConstructorMany
		if v.MultirangeLen() == 0 {
			form = catalog.MultirangeConstructorZero
		} else if v.MultirangeLen() == 1 {
			form = catalog.MultirangeConstructorOne
		}
		name, err := rangeConstructorName(view, reader, t, form)
		if err != nil {
			return "", err
		}
		parts := make([]string, v.MultirangeLen())
		for i := range parts {
			parts[i], err = literalWithACLAt(ctx, view, reader, plan.LiteralExpr{Info: expr.Info{Usage: t.RangeType().Usage()}, Value: v.MultirangeMember(i)}, acl, false)
			if err != nil {
				return "", err
			}
		}
		return name + "(" + strings.Join(parts, ", ") + ")", nil
	}
	bounds, err := v.RangeBounds()
	if err != nil {
		return "", err
	}
	if bounds.Empty {
		declaration, err := TypeDeclarationAt(view, reader, n.Usage)
		return "CAST('empty' AS " + declaration + ")", err
	}
	definition, ok := t.RangeDefinition()
	if !ok {
		return "", fmt.Errorf("missing custom range construction authority")
	}
	name, err := rangeConstructorName(view, reader, t, catalog.RangeConstructorThree)
	if err != nil {
		return "", err
	}
	lower, err := literalWithACLAt(ctx, view, reader, plan.LiteralExpr{Info: expr.Info{Usage: definition.Subtype}, Value: bounds.Lower}, acl, false)
	if err != nil {
		return "", err
	}
	upper, err := literalWithACLAt(ctx, view, reader, plan.LiteralExpr{Info: expr.Info{Usage: definition.Subtype}, Value: bounds.Upper}, acl, false)
	if err != nil {
		return "", err
	}
	flags := [2]byte{'(', ')'}
	if bounds.LowerInclusive {
		flags[0] = '['
	}
	if bounds.UpperInclusive {
		flags[1] = ']'
	}
	return name + "(" + lower + ", " + upper + ", '" + string(flags[:]) + "')", nil
}
