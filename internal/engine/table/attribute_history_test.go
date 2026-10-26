package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAccessRelationsDroppedInheritanceHistory(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	if integer == nil {
		t.Fatal("missing builtin int4")
	}
	for _, count := range []int{0, 1, 3} {
		origins := make([]catalog.InheritanceOrigin, count)
		for i := range origins {
			origins[i] = catalog.InheritanceOrigin{Parent: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(53000 + i)}, Attribute: 1}
		}
		r := &catalog.Relation{RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "gone", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{3}, Inheritance: &catalog.TableInheritance{Columns: map[int32]catalog.InheritedColumn{3: {Origins: origins}}}}
		history := droppedAttributeMetadata(r, 0)
		if history.Local || history.Inheritance != 1 || !history.Dropped || history.Number != 3 || history.Name != "........pg.dropped.3........" || history.TypeID != 0 || history.NotNull || history.HasDefault || history.Generated != "" {
			t.Fatal("final nonlocal deletion", count, history)
		}
		if len(r.Inheritance.Columns[3].Origins) != count || r.Columns[0].Name != "gone" {
			t.Fatal("history capture mutated source")
		}
	}
	r := &catalog.Relation{RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "local", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1}}
	history := droppedAttributeMetadata(r, 0)
	if !history.Local || history.Inheritance != 0 {
		t.Fatal("local history was changed", history)
	}
}
