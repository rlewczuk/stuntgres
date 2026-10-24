package index

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func classSQLName(view catalog.Read, class *catalog.OperatorClass) string {
	return catalog.QuoteIdentifier(view.Get(class.Namespace()).Name()) + "." + catalog.QuoteIdentifier(class.Name())
}

// operatorFamilyMemberDumpItem is shared by CREATE CLASS and loose FAMILY ADD.
// Only insertion-time INTERNAL members belong in the class declaration.
func operatorFamilyMemberDumpItem(p dump.Presenter, view catalog.Read, descriptor catalog.Descriptor) (string, error) {
	switch d := descriptor.(type) {
	case *catalog.OperatorFamilyOperator:
		operator, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: d.Operator})
		if !ok {
			return "", fmt.Errorf("stuntgres: missing family dump operator")
		}
		args, err := familyDumpTypes(p, view, []ids.OID{operator.Left, operator.Right})
		if err != nil {
			return "", err
		}
		name := catalog.QuoteIdentifier(view.Get(operator.Namespace()).Name()) + "." + operator.Name()
		text := "OPERATOR " + strconv.Itoa(int(d.Number)) + " " + name + " (" + args + ")"
		if d.Purpose == 'o' {
			sort, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.SortFamily})
			if !ok {
				return "", fmt.Errorf("stuntgres: missing sort family dump dependency")
			}
			text += " FOR ORDER BY " + familySQLName(view, sort)
		}
		return text, nil
	case *catalog.OperatorFamilySupport:
		identity, ok := p.RoutineIdentity(view, d.Function)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing support routine dump identity")
		}
		var formals []ids.OID
		if identity.Signature != "" {
			for _, part := range strings.Split(string(identity.Signature), ",") {
				n, err := strconv.ParseUint(part, 10, 32)
				if err != nil {
					return "", err
				}
				formals = append(formals, ids.OID(n))
			}
		}
		args, err := familyDumpTypes(p, view, formals)
		if err != nil {
			return "", err
		}
		associated, err := familyDumpTypes(p, view, []ids.OID{d.Left, d.Right})
		if err != nil {
			return "", err
		}
		ns := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: identity.Namespace})
		if ns == nil {
			return "", fmt.Errorf("stuntgres: missing support routine dump namespace")
		}
		name := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(identity.Name)
		return "FUNCTION " + strconv.Itoa(int(d.Number)) + " (" + associated + ") " + name + " (" + args + ")", nil
	}
	return "", fmt.Errorf("stuntgres: invalid family dump member")
}

func operatorClassDumpStatement(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, class *catalog.OperatorClass, members []catalog.Descriptor) (dump.Statement, error) {
	family, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: class.Family})
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing class dump family")
	}
	input, err := familyDumpTypes(p, view, []ids.OID{class.InputType})
	if err != nil {
		return dump.Statement{}, err
	}
	var items []string
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return dump.Statement{}, err
		}
		if catalog.OperatorFamilyMemberOwner(member) != class.Address() {
			continue
		}
		item, err := operatorFamilyMemberDumpItem(p, view, member)
		if err != nil {
			return dump.Statement{}, err
		}
		items = append(items, item)
	}
	slices.Sort(items)
	// An empty class is legal. STORAGE equal to input is a declaration item
	// that PostgreSQL normalizes to zero, without inventing a family member.
	if class.StorageType != 0 || len(items) == 0 {
		storage := class.StorageType
		if storage == 0 {
			storage = class.InputType
		}
		text, err := familyDumpTypes(p, view, []ids.OID{storage})
		if err != nil {
			return dump.Statement{}, err
		}
		items = append(items, "STORAGE "+text)
	}
	owner, err := roles.Descriptor(class.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	name := classSQLName(view, class)
	using := " USING " + catalog.Identifier(familyMethodName(view, class.Method))
	text := "CREATE OPERATOR CLASS " + name
	if class.Default {
		text += " DEFAULT"
	}
	text += " FOR TYPE " + input + using + " FAMILY " + familySQLName(view, family) + " AS\n    " + strings.Join(items, ",\n    ") + ";\n"
	text += "ALTER OPERATOR CLASS " + name + using + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n"
	return dump.Statement{Owner: "postgres", SQL: text}, nil
}
