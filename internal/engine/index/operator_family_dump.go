package index

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func OperatorFamilyDumpEvents(r dump.Resolver, view catalog.Read, descriptors []catalog.Descriptor, graph dump.Graph) error {
	emittedFamilies := make(map[ids.OID]bool)
	for _, d := range descriptors {
		if _, ok := d.(*catalog.OperatorFamily); ok {
			emittedFamilies[d.Address().ObjectID] = true
		}
	}
	for _, d := range descriptors {
		event := dump.Event{OID: d.Address().ObjectID}
		graph[event] = nil
		for _, dependency := range catalog.OperatorFamilyDependencies(view, d) {
			if dependency.Address.ClassID == ids.NamespaceClassID {
				continue
			}
			if dependency.Address.ClassID == ids.OperatorFamilyClassID {
				family, ok := view.OperatorFamily(dependency.Address)
				if !ok {
					return fmt.Errorf("stuntgres: missing family dump dependency")
				}
				if emittedFamilies[family.Address().ObjectID] {
					graph[event] = append(graph[event], dump.Event{OID: family.Address().ObjectID})
				}
				continue
			}
			predecessor, err := r.Predecessor(view, dependency.Address, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				graph[event] = append(graph[event], dump.Event{OID: predecessor})
			}
		}
	}
	return nil
}
func familySQLName(view catalog.Read, f *catalog.OperatorFamily) string {
	return catalog.QuoteIdentifier(view.Get(f.Namespace()).Name()) + "." + catalog.QuoteIdentifier(f.Name())
}
func familyDumpTypes(p dump.Presenter, view catalog.Read, args []ids.OID) (string, error) {
	names := make([]string, len(args))
	for i, id := range args {
		t, ok := view.LookupType(sqltype.ID(id))
		if !ok {
			return "", fmt.Errorf("stuntgres: missing family dump type")
		}
		text, err := p.TypeDeclaration(view, nil, t.Usage())
		if err != nil {
			return "", err
		}
		names[i] = text
	}
	return strings.Join(names, ", "), nil
}
func OperatorFamilyDumpStatements(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, descriptors []catalog.Descriptor, declarations map[ids.OID]dump.Statement) error {
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return err
		}
		var text string
		switch d := descriptor.(type) {
		case *catalog.OperatorFamily:
			if catalog.OperatorFamilySeeded(d) {
				continue
			}
			owner, err := roles.Descriptor(d.Owner)
			if err != nil {
				return err
			}
			name := familySQLName(view, d) + " USING " + familyMethodName(d.Method)
			text = "CREATE OPERATOR FAMILY " + name + ";\nALTER OPERATOR FAMILY " + name + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n"
		case *catalog.OperatorFamilyOperator:
			f, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family})
			if !ok {
				return fmt.Errorf("stuntgres: missing family dump owner")
			}
			operator, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: d.Operator})
			if !ok {
				return fmt.Errorf("stuntgres: missing family dump operator")
			}
			args, err := familyDumpTypes(p, view, []ids.OID{operator.Left, operator.Right})
			if err != nil {
				return err
			}
			name := catalog.QuoteIdentifier(view.Get(operator.Namespace()).Name()) + "." + operator.Name()
			text = "ALTER OPERATOR FAMILY " + familySQLName(view, f) + " USING " + familyMethodName(f.Method) + " ADD OPERATOR " + strconv.Itoa(int(d.Number)) + " " + name + " (" + args + ")"
			if d.Purpose == 'o' {
				sort, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.SortFamily})
				if !ok {
					return fmt.Errorf("stuntgres: missing sort family dump dependency")
				}
				text += " FOR ORDER BY " + familySQLName(view, sort)
			}
			text += ";\n"
		case *catalog.OperatorFamilySupport:
			f, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family})
			if !ok {
				return fmt.Errorf("stuntgres: missing family dump owner")
			}
			identity, ok := p.RoutineIdentity(view, d.Function)
			if !ok {
				return fmt.Errorf("stuntgres: missing support routine dump identity")
			}
			var formals []ids.OID
			if identity.Signature != "" {
				for _, part := range strings.Split(string(identity.Signature), ",") {
					n, err := strconv.ParseUint(part, 10, 32)
					if err != nil {
						return err
					}
					formals = append(formals, ids.OID(n))
				}
			}
			args, err := familyDumpTypes(p, view, formals)
			if err != nil {
				return err
			}
			associated, err := familyDumpTypes(p, view, []ids.OID{d.Left, d.Right})
			if err != nil {
				return err
			}
			ns := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: identity.Namespace})
			if ns == nil {
				return fmt.Errorf("stuntgres: missing support routine dump namespace")
			}
			name := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(identity.Name)
			text = "ALTER OPERATOR FAMILY " + familySQLName(view, f) + " USING " + familyMethodName(f.Method) + " ADD FUNCTION " + strconv.Itoa(int(d.Number)) + " (" + associated + ") " + name + " (" + args + ");\n"
		default:
			return fmt.Errorf("stuntgres: invalid family dump descriptor")
		}
		declarations[descriptor.Address().ObjectID] = dump.Statement{Owner: "postgres", SQL: text}
	}
	return nil
}
