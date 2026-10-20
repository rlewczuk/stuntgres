package typedef

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func CompositeOpenError(relation *catalog.Relation, pos parser.Position) error {
	d := expr.ErrorAt("42809", fmt.Sprintf(`cannot open relation "%s"`, relation.Name()), pos).(*sqlstate.Error)
	d.Detail = "This operation is not supported for composite types."
	return d
}

func ContainsRowType(typ *sqltype.Descriptor, id sqltype.ID) bool {
	if typ.ID() == id {
		return true
	}
	if typ.IsDomain() {
		return ContainsRowType(typ.DomainBase().Type, id)
	}
	if typ.IsArray() {
		return ContainsRowType(typ.Element(), id)
	}
	if typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if ContainsRowType(typ.CompositeMember(i).Usage.Type, id) {
				return true
			}
		}
	}
	return false
}

// PostgreSQL protects physical row codecs even when the proposed SQL type is
// unchanged. Find the real stored consumer, not an intermediate named wrapper.
func RowRewriteError(view catalog.Read, source *catalog.Relation) error {
	if source.RowType == 0 {
		return nil
	}
	var consumer *catalog.Relation
	var column string
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || !table.RelationKind.Stored() {
			continue
		}
		for _, c := range table.Columns {
			if ContainsRowType(c.Usage.Type, source.RowType) && (consumer == nil || table.Address().ObjectID < consumer.Address().ObjectID) {
				consumer, column = table, c.Name
				break
			}
		}
	}
	if consumer == nil {
		return nil
	}
	if source.RelationKind == catalog.CompositeRelation {
		return sqlError("0A000", fmt.Sprintf(`cannot alter type "%s" because column "%s.%s" uses it`, source.Name(), consumer.Name(), column))
	}
	return sqlError("0A000", fmt.Sprintf(`cannot alter table "%s" because column "%s.%s" uses its row type`, source.Name(), consumer.Name(), column))
}
