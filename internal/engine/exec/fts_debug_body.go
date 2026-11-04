package exec

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// AdmitDebugCatalogBody consumes the original analyzed body's table references,
// never authority manufactured by a current OID/name lookup. The actual query
// initializes both permission RTEs even when parsing will produce no rows.
func AdmitDebugCatalogBody(c *Context, outer *catalog.Procedure) error {
	if outer == nil || outer.Builtin == nil || outer.Builtin.Identity.ID != 6183 {
		return fmt.Errorf("stuntgres: invalid debug query body owner")
	}
	auth, dependencies, err := RoutineInvocationAuthority(c)
	if err != nil || !auth.Live() {
		return err
	}
	for _, table := range [...]struct {
		oid        ids.OID
		count      int
		attributes [4]int32
		names      [4]string
	}{
		{3602, 2, [4]int32{1, 5}, [4]string{"oid", "cfgparser"}},
		{3603, 4, [4]int32{1, 2, 3, 4}, [4]string{"mapcfg", "maptokentype", "mapseqno", "mapdict"}},
	} {
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: table.oid}
		var captured catalog.ExpressionDependency
		found := false
		for _, dependency := range dependencies {
			if dependency.Ref.Address == address && dependency.Purpose == catalog.DefinitionDependency {
				captured = dependency
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("stuntgres: missing original debug catalog body manifest")
		}
		reference := captured.Ref
		if c.RoutineConsumer != nil {
			if !preparation.StoredReferenceValid(auth.Catalog, c.RoutineConsumer, reference) {
				return ids.ErrCatalogReplaced
			}
			reference.Epoch = auth.Catalog.Epoch()
		}
		descriptor, err := auth.Object(reference)
		if err != nil {
			return err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.CatalogRelation || relation.CatalogView || reference.Epoch != auth.Catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if c.RoutineConsumer == nil && relation.Revision() != captured.Revision {
			return catalog.Conflict{}
		}
		var indices [4]int
		for i, attribute := range table.attributes[:table.count] {
			index := relation.IndexForAttribute(attribute)
			if index < 0 || relation.Columns[index].Name != table.names[i] {
				return fmt.Errorf("stuntgres: invalid original debug catalog attributes")
			}
			indices[i] = index
		}
		if err = auth.Check(access.Requirement{Object: reference, Privileges: authorization.SelectPrivilege, ColumnDemand: true, Columns: indices[:table.count]}); err != nil {
			return err
		}
	}
	return nil
}
