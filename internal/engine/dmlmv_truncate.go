package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// A granted identity owns both the immutable descriptor and its physical store.
// Virtual partition roots have no store but participate in locks and closure.
type truncateRelation struct {
	ref       ids.ObjectRef
	relation  *catalog.Relation
	storage   *storage.Relation
	privilege bool
}

// Descendant admission and its final graph fence share the inherited-query
// consumer's namespace/session-token authority, not child ACL or USAGE checks.
func truncateOtherTemporary(catalog catalog.Read, relation *catalog.Relation, token ids.SessionToken) (bool, error) {
	namespace, ok := catalog.Namespace(relation.Namespace())
	if !ok {
		return false, ids.ErrCatalogReplaced
	}
	return namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != token, nil
}

type truncateReferencer struct{ relation, parent *catalog.Relation }

func compareTruncateAddress(a, b ids.ObjectAddress) int {
	if a.ObjectID < b.ObjectID {
		return -1
	}
	if a.ObjectID > b.ObjectID {
		return 1
	}
	return 0
}

// FK definitions alone decide closure, including empty referencing tables.
func truncateReferencers(view catalog.Read, selected map[ids.ObjectAddress]int) []truncateReferencer {
	var result []truncateReferencer
	for address, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || selected[address] != 0 {
			continue
		}
		for _, fk := range d.Constraints.ForeignKeys {
			if selected[fk.Target] != 0 {
				parent, _ := view.Relation(fk.Target)
				if parent != nil {
					result = append(result, truncateReferencer{d, parent})
				}
				break
			}
		}
	}
	slices.SortFunc(result, func(a, b truncateReferencer) int {
		return compareTruncateAddress(a.relation.Address(), b.relation.Address())
	})
	return result
}

func truncateForeignKeyError(parent, child string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot truncate a table referenced in a foreign key constraint", Detail: fmt.Sprintf("Table %q references %q.", child, parent), Hint: fmt.Sprintf("Truncate table %q at the same time, or use TRUNCATE ... CASCADE.", child)}
}

// truncateScope is the statement's TRUNCATE slot: the selected relations and
// sequences, RESTART IDENTITY and the foreign-table CASCADE request.
type truncateScope struct {
	relations      []truncateRelation
	sequences      []truncateSequence
	restart        bool
	names          []truncateName
	selected       map[ids.ObjectAddress]int
	foreignCascade bool
}

// Counters remain mutable and nontransactional within one generator binding.
// These observations fence the binding/descriptor, never a snapshot of counters.
type truncateSequence struct {
	ref       ids.ObjectRef
	relation  *catalog.Relation
	generator *storage.SequenceGenerator
	committed *storage.SequenceGenerator
}

func truncateOwnsSequence(d *catalog.Relation, selected map[ids.ObjectAddress]int) bool {
	if d.RelationKind != catalog.SequenceRelation || d.Sequence.Owned == (ids.ObjectAddress{}) {
		return false
	}
	a := d.Sequence.Owned
	a.SubID = 0
	return selected[a] != 0
}

type truncateName struct {
	name      parser.ObjectName
	ref       ids.ObjectRef
	namespace ids.ObjectAddress
}
