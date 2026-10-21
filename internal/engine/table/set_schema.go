package table

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// SetSchemaOwned lists the relations that move with old: its indexes and,
// for a table, the sequences its columns own, in OID order.
func SetSchemaOwned(view catalog.Read, old *catalog.Relation) []*catalog.Relation {
	owned := make([]*catalog.Relation, 0)
	for _, d := range view.Descriptors() {
		r, ok := d.(*catalog.Relation)
		if ok && r.SQLIndex != nil && r.SQLIndex.Parent == old.Address() {
			owned = append(owned, r)
			continue
		}
		if !ok || r.RelationKind != catalog.SequenceRelation {
			continue
		}
		a := r.Sequence.Owned
		if old.RelationKind == catalog.TableRelation && a.ClassID == old.Address().ClassID && a.ObjectID == old.Address().ObjectID && old.HasAttribute(a.SubID) {
			owned = append(owned, r)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Address().ObjectID < owned[j].Address().ObjectID })
	return owned
}

// SetSchemaGroup re-reads old's owned relations in view: each must still
// be an index of old or a sequence old owns, in old's namespace with old's
// owner. old comes first.
func SetSchemaGroup(view catalog.Read, old *catalog.Relation, owned []*catalog.Relation) ([]*catalog.Relation, error) {
	group := make([]*catalog.Relation, 0, len(owned)+1)
	group = append(group, old)
	for _, r := range owned {
		live, ok := view.Relation(r.Address())
		if ok && r.SQLIndex != nil {
			if live.SQLIndex != r.SQLIndex || live.Namespace() != old.Namespace() || live.Owner != old.Owner {
				return nil, ids.ErrCatalogReplaced
			}
			group = append(group, live)
			continue
		}
		if !ok || live.RelationKind != catalog.SequenceRelation || live.Sequence.Owned != r.Sequence.Owned || live.Namespace() != old.Namespace() || live.Owner != old.Owner {
			return nil, ids.ErrCatalogReplaced
		}
		group = append(group, live)
	}
	return group, nil
}

// SetSchemaCandidates builds the moved descriptors of group in namespace
// destination (schema names it in diagnostics), each at its next revision
// frontier, rejecting relation and row-type name collisions.
func SetSchemaCandidates(view catalog.Read, group []*catalog.Relation, destination ids.ObjectAddress, schema string) ([]catalog.Relation, error) {
	next := make([]catalog.Relation, len(group))
	for i, r := range group {
		if _, exists := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination, Name: r.Name()}); exists {
			return nil, setSchemaCollision(r, schema)
		}
		if r.RowType != 0 {
			if winner, exists := view.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: destination, Name: r.Name()}); exists && winner != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)}) {
				return nil, setSchemaTypeCollision(r, schema)
			}
		}
		frontier, terminal := catalog.AdvanceRevisionFrontier(0, r)
		if terminal {
			return nil, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next[i] = *r
		next[i].ObjectNamespace, next[i].ObjectRevision = destination, frontier
		if r.RelationKind == catalog.SequenceRelation {
			next[i].SequencePlanStamp = frontier
		}
	}
	return next, nil
}

// SetSchemaConflict explains a lost publication race: the relation or
// row-type name collision a concurrent winner now holds in destination, or
// nil.
func SetSchemaConflict(current catalog.Read, group []*catalog.Relation, destination ids.ObjectAddress, schema string) error {
	for _, r := range group {
		winner, exists := current.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination, Name: r.Name()})
		if exists && winner != r.Address() {
			return setSchemaCollision(r, schema)
		}
		if r.RowType != 0 {
			if winner, exists := current.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: destination, Name: r.Name()}); exists && winner != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)}) {
				return setSchemaTypeCollision(r, schema)
			}
		}
	}
	return nil
}

func setSchemaCollision(r *catalog.Relation, schema string) error {
	return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists in schema "%s"`, r.Name(), schema))
}

func setSchemaTypeCollision(r *catalog.Relation, schema string) error {
	return sqlError("42710", fmt.Sprintf(`type "%s" already exists in schema "%s"`, r.Name(), schema))
}
