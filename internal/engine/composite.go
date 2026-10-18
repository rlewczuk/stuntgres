package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Declaration members, relation identity and reciprocal codecs publish in the
// same existing catalog stage. This relation never owns physical storage.
func (e *Engine) stageCreateCompositeLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateComposite) error {
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	trustedDestination := op != nil && op.restoreTableDestination
	if trustedDestination {
		bootstrap, guardErr := auth.roles.Descriptor(auth.state.Authenticated)
		if guardErr != nil || !bootstrap.Superuser || e.importTargets == nil {
			return fmt.Errorf("stuntgres: invalid trusted restore composite destination authority")
		}
	}
	if err := auth.CheckCreate(ns); err != nil {
		denied, ok := err.(*sqlstate.Error)
		if !trustedDestination || !ok || denied.Code != "42501" {
			return err
		}
	}
	if err := checkPersistentCreationNamespace(auth.catalog, ns.Address); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	available, err := e.displaceAssociatedArrayLocked(stage, ns.Address, name)
	if err != nil {
		return err
	}
	if !available || relationTypeNameOccupied(auth.catalog, ns.Address, name) {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
	}
	if stage.lookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}) != (ids.ObjectAddress{}) {
		return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	if len(s.Members) > 1600 {
		return sqlError("54011", "tables can have at most 1600 columns")
	}
	columns := make([]ColumnDescriptor, len(s.Members))
	index := make(map[string]int, len(columns))
	for i, member := range s.Members {
		if err := op.Context().Err(); err != nil {
			return err
		}
		if _, exists := index[member.Name]; exists {
			return sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, member.Name))
		}
		index[member.Name] = i
		if member.TypeName.Name == "record" && (member.TypeName.Schema == nil || member.TypeName.Schema.Text == "pg_catalog") {
			return sqlError("42P16", fmt.Sprintf(`column "%s" has pseudo-type record`, member.Name))
		}
		// CREATE TYPE member semantic errors have no SQL input location; syntax
		// errors remain parser-owned and retain their exact original position.
		raw := member.TypeName
		raw.Pos = parser.Position{}
		usage, err := e.resolveType(raw, op)
		if err != nil {
			return err
		}
		collation := member.Collation
		if collation != nil {
			copy := *collation
			copy.Parts = append([]parser.Identifier(nil), collation.Parts...)
			copy.Parts[0].Pos = parser.Position{}
			collation = &copy
		}
		usage, err = e.resolveColumnCollation(usage, collation, op)
		if err != nil {
			return err
		}
		ref := e.domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if err := auth.CanUse(ref); err != nil {
			return err
		}
		stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[ref.Address])
		columns[i] = ColumnDescriptor{Name: member.Name, Usage: usage, DeclaredDimensions: int16(len(raw.ArrayBounds)), Nullable: true, NullabilityKnown: true}
	}
	relation, err := e.newTableDescriptorLocked(ns.Address, name, auth.state.CurrentUser, columns, index)
	if err != nil {
		return err
	}
	relation.relationKind = compositeRelation
	if err := e.stageRowTypeLocked(stage, relation); err != nil {
		return err
	}
	return stage.Create(relation)
}

func compositeOpenError(relation *relationDescriptor, pos parser.Position) error {
	d := expressionError("42809", fmt.Sprintf(`cannot open relation "%s"`, relation.name), pos).(*sqlstate.Error)
	d.Detail = "This operation is not supported for composite types."
	return d
}

func containsRowType(typ *sqltype.Descriptor, id sqltype.ID) bool {
	if typ.ID() == id {
		return true
	}
	if typ.IsDomain() {
		return containsRowType(typ.DomainBase().Type, id)
	}
	if typ.IsArray() {
		return containsRowType(typ.Element(), id)
	}
	if typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if containsRowType(typ.CompositeMember(i).Usage.Type, id) {
				return true
			}
		}
	}
	return false
}

// PostgreSQL protects physical row codecs even when the proposed SQL type is
// unchanged. Find the real stored consumer, not an intermediate named wrapper.
func rowRewriteError(catalog CatalogRead, source *relationDescriptor) error {
	if source.rowType == 0 {
		return nil
	}
	var consumer *relationDescriptor
	var column string
	for _, descriptor := range catalog.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || !table.relationKind.stored() {
			continue
		}
		for _, c := range table.columns {
			if containsRowType(c.Usage.Type, source.rowType) && (consumer == nil || table.address.ObjectID < consumer.address.ObjectID) {
				consumer, column = table, c.Name
				break
			}
		}
	}
	if consumer == nil {
		return nil
	}
	if source.relationKind == compositeRelation {
		return sqlError("0A000", fmt.Sprintf(`cannot alter type "%s" because column "%s.%s" uses it`, source.name, consumer.name, column))
	}
	return sqlError("0A000", fmt.Sprintf(`cannot alter table "%s" because column "%s.%s" uses its row type`, source.name, consumer.name, column))
}
