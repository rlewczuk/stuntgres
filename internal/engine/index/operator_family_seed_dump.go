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

// OperatorFamilySeedDumpStatements uses the existing seed-staging section and
// ordinary declaration graph. Stage all moving originals first, then final
// family events restore names/schema/owner before their loose member events.
func OperatorFamilySeedDumpStatements(ctx context.Context, view catalog.Read, roles authorization.Read, descriptors []catalog.Descriptor) ([]string, map[ids.OID]dump.Statement, error) {
	namespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	if namespace == nil {
		return nil, nil, fmt.Errorf("stuntgres: missing bootstrap family namespace")
	}
	initial := catalog.QuoteIdentifier(namespace.Name())
	var families []*catalog.OperatorFamily
	for _, d := range descriptors {
		if f, ok := d.(*catalog.OperatorFamily); ok && catalog.OperatorFamilySeeded(f) {
			families = append(families, f)
		}
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Address().ObjectID < families[j].Address().ObjectID })
	var stages []string
	finals := make(map[ids.OID]dump.Statement, len(families))
	// Reserve all current names plus canonical seed names under their actual
	// method/namespace identity. Temporary names are bootstrap-namespace objects.
	occupied := make(map[ids.NameKey]bool)
	for _, d := range view.Descriptors() {
		if f, ok := d.(*catalog.OperatorFamily); ok {
			occupied[catalog.DescriptorNameKey(f)] = true
			if fixed, seeded := catalog.OperatorFamilySeedName(f.Address().ObjectID); seeded {
				occupied[catalog.FamilyNameKey(namespace.Address(), f.Method, fixed)] = true
			}
		}
	}
	for _, f := range families {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		fixed, _ := catalog.OperatorFamilySeedName(f.Address().ObjectID)
		from := initial + "." + catalog.QuoteIdentifier(fixed)
		using := " USING " + catalog.Identifier(familyMethodName(view, f.Method))
		var final string
		if f.Name() != fixed || f.Namespace() != namespace.Address() {
			base := "__stuntgres_family_seed_" + strconv.FormatUint(uint64(f.Address().ObjectID), 10)
			temporary := base
			for n := 0; occupied[catalog.FamilyNameKey(namespace.Address(), f.Method, temporary)]; n++ {
				temporary = base + "_" + strconv.Itoa(n)
			}
			occupied[catalog.FamilyNameKey(namespace.Address(), f.Method, temporary)] = true
			stages = append(stages, "ALTER OPERATOR FAMILY "+from+using+" RENAME TO "+catalog.QuoteIdentifier(temporary)+";\n")
			from = initial + "." + catalog.QuoteIdentifier(temporary)
			if f.Namespace() != namespace.Address() {
				target := view.Get(f.Namespace())
				if target == nil {
					return nil, nil, fmt.Errorf("stuntgres: missing family destination namespace")
				}
				final += "ALTER OPERATOR FAMILY " + from + using + " SET SCHEMA " + catalog.QuoteIdentifier(target.Name()) + ";\n"
				from = catalog.QuoteIdentifier(target.Name()) + "." + catalog.QuoteIdentifier(temporary)
			}
			final += "ALTER OPERATOR FAMILY " + from + using + " RENAME TO " + catalog.QuoteIdentifier(f.Name()) + ";\n"
		}
		if f.Owner.ID() != 10 {
			owner, err := roles.Descriptor(f.Owner)
			if err != nil {
				return nil, nil, err
			}
			final += "ALTER OPERATOR FAMILY " + familySQLName(view, f) + using + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n"
		}
		finals[f.Address().ObjectID] = dump.Statement{Owner: "postgres", SQL: final}
	}
	classStages, classFinals, err := operatorClassSeedDumpStatements(ctx, view, roles, descriptors)
	if err != nil {
		return nil, nil, err
	}
	stages = append(stages, classStages...)
	for oid, statement := range classFinals {
		finals[oid] = statement
	}
	return stages, finals, nil
}
