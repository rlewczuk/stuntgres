package index

import (
	"context"
	"fmt"
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
		if memberOwningClass(d) != 0 {
			continue
		}
		event := dump.Event{OID: d.Address().ObjectID}
		graph[event] = nil
		dependencies := catalog.OperatorFamilyDependencies(view, d)
		if class, ok := d.(*catalog.OperatorClass); ok {
			for _, member := range descriptors {
				if memberOwningClass(member) == class.Address().ObjectID {
					for _, dependency := range catalog.OperatorFamilyDependencies(view, member) {
						if dependency.Address != class.Address() {
							dependencies = append(dependencies, dependency)
						}
					}
				}
			}
		}
		for _, dependency := range dependencies {
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
		// Family signatures and class inputs may name catalog pseudo-types
		// (for example anyenum or internal), not concrete column usages.
		if !t.Concrete() {
			descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
			if !ok || descriptor.SQLType != t {
				return "", fmt.Errorf("stuntgres: invalid family dump type authority")
			}
			namespace, ok := view.Namespace(descriptor.Namespace())
			if !ok {
				return "", fmt.Errorf("stuntgres: missing family dump type namespace")
			}
			names[i] = catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(descriptor.Name())
			continue
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
		case *catalog.OperatorClass:
			if catalog.OperatorClassSeeded(d) {
				continue
			}
			statement, err := operatorClassDumpStatement(ctx, p, view, roles, d, descriptors)
			if err != nil {
				return err
			}
			declarations[d.Address().ObjectID] = statement
			continue
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
		case *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
			if memberOwningClass(d) != 0 {
				continue
			}
			var family ids.OID
			switch member := d.(type) {
			case *catalog.OperatorFamilyOperator:
				family = member.Family
			case *catalog.OperatorFamilySupport:
				family = member.Family
			}
			f, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: family})
			if !ok {
				return fmt.Errorf("stuntgres: missing family dump owner")
			}
			item, err := operatorFamilyMemberDumpItem(p, view, d)
			if err != nil {
				return err
			}
			text = "ALTER OPERATOR FAMILY " + familySQLName(view, f) + " USING " + familyMethodName(f.Method) + " ADD " + item + ";\n"
		default:
			return fmt.Errorf("stuntgres: invalid family dump descriptor")
		}
		declarations[descriptor.Address().ObjectID] = dump.Statement{Owner: "postgres", SQL: text}
	}
	return nil
}
