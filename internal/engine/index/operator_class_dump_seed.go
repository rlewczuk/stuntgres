package index

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

// Stage every moving original before final declarations, including name swaps
// and a user class occupying a vacated canonical seed name.
func operatorClassSeedDumpStatements(ctx context.Context, view catalog.Read, roles authorization.Read, descriptors []catalog.Descriptor) ([]string, map[ids.OID]dump.Statement, error) {
	namespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	if namespace == nil {
		return nil, nil, fmt.Errorf("stuntgres: missing bootstrap class namespace")
	}
	initial := catalog.QuoteIdentifier(namespace.Name())
	var classes []*catalog.OperatorClass
	for _, d := range descriptors {
		if class, ok := d.(*catalog.OperatorClass); ok && catalog.OperatorClassSeeded(class) {
			classes = append(classes, class)
		}
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].Address().ObjectID < classes[j].Address().ObjectID })
	occupied := make(map[ids.NameKey]bool)
	for _, class := range view.OperatorClasses() {
		occupied[catalog.DescriptorNameKey(class)] = true
		if fixed, seeded := catalog.OperatorClassSeedName(class.Address().ObjectID); seeded {
			occupied[catalog.ClassNameKey(namespace.Address(), class.Method, fixed)] = true
		}
	}
	var stages []string
	finals := make(map[ids.OID]dump.Statement, len(classes))
	for _, class := range classes {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		fixed, _ := catalog.OperatorClassSeedName(class.Address().ObjectID)
		from := initial + "." + catalog.QuoteIdentifier(fixed)
		using := " USING " + catalog.Identifier(familyMethodName(view, class.Method))
		var final string
		if class.Name() != fixed || class.Namespace() != namespace.Address() {
			base := "__stuntgres_class_seed_" + strconv.FormatUint(uint64(class.Address().ObjectID), 10)
			temporary := base
			for n := 0; occupied[catalog.ClassNameKey(namespace.Address(), class.Method, temporary)]; n++ {
				temporary = base + "_" + strconv.Itoa(n)
			}
			occupied[catalog.ClassNameKey(namespace.Address(), class.Method, temporary)] = true
			stages = append(stages, "ALTER OPERATOR CLASS "+from+using+" RENAME TO "+catalog.QuoteIdentifier(temporary)+";\n")
			from = initial + "." + catalog.QuoteIdentifier(temporary)
			if class.Namespace() != namespace.Address() {
				target := view.Get(class.Namespace())
				if target == nil {
					return nil, nil, fmt.Errorf("stuntgres: missing class destination namespace")
				}
				final += "ALTER OPERATOR CLASS " + from + using + " SET SCHEMA " + catalog.QuoteIdentifier(target.Name()) + ";\n"
				from = catalog.QuoteIdentifier(target.Name()) + "." + catalog.QuoteIdentifier(temporary)
			}
			final += "ALTER OPERATOR CLASS " + from + using + " RENAME TO " + catalog.QuoteIdentifier(class.Name()) + ";\n"
		}
		if class.Owner.ID() != 10 {
			owner, err := roles.Descriptor(class.Owner)
			if err != nil {
				return nil, nil, err
			}
			final += "ALTER OPERATOR CLASS " + classSQLName(view, class) + using + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n"
		}
		finals[class.Address().ObjectID] = dump.Statement{Owner: "postgres", SQL: final}
	}
	return stages, finals, nil
}
