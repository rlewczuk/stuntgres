// Package publication owns database-local logical publication definitions and
// effective membership. It does not implement a replication transport.
package publication

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
)

// EffectiveTable borrows immutable definitions from one catalog read. Source is
// the binding layout; Relation is the identity presented by this publication.
type EffectiveTable struct {
	Publication      *catalog.Publication
	Relation, Source *catalog.Relation
	Mapping          *catalog.PublicationRelation
	ColumnsSpecified bool
	Columns          []int32
	Filter           catalog.PolicyPayload
}

func IsPublishable(v catalog.Read, r *catalog.Relation) bool {
	if r == nil || r.Address().ObjectID < 16384 || (r.RelationKind != catalog.TableRelation && r.RelationKind != catalog.PartitionedRelation) || table.EffectivePersistence(v, r) != "logged" {
		return false
	}
	ns, ok := v.Namespace(r.Namespace())
	return ok && ns.Address().ObjectID != ids.PGCatalogNamespaceOID && ns.Address().ObjectID != ids.PGToastNamespaceOID && !ns.TemporaryToast
}

func coverage(v catalog.Read, p *catalog.Publication, r *catalog.Relation) (*catalog.PublicationRelation, bool, bool) {
	unrestricted := p.AllTables
	for _, n := range v.PublicationNamespaces() {
		if n.Publication == p.Address() && n.Schema == r.Namespace() {
			unrestricted = true
			break
		}
	}
	for _, m := range v.PublicationRelations() {
		if m.Publication == p.Address() && m.Relation == r.Address() {
			return m, true, unrestricted
		}
	}
	return nil, unrestricted, unrestricted
}

func resolve(v catalog.Read, p *catalog.Publication, leaf *catalog.Relation) (EffectiveTable, bool, error) {
	result := EffectiveTable{Publication: p}
	unrestricted := false
	for node := leaf; node != nil; {
		m, covered, broad := coverage(v, p, node)
		if covered && (result.Source == nil || p.ViaRoot) {
			result.Source, result.Mapping = node, m
		}
		unrestricted = unrestricted || broad
		if node.Membership == nil {
			break
		}
		parent, ok := v.Relation(node.Membership.Parent)
		if !ok {
			return result, false, ids.ErrCatalogReplaced
		}
		node = parent
	}
	if result.Source == nil {
		return result, false, nil
	}
	result.Relation = leaf
	if p.ViaRoot {
		result.Relation = result.Source
	}
	if result.Mapping != nil && !unrestricted {
		result.ColumnsSpecified = result.Mapping.ColumnsSpecified
		result.Columns, result.Filter = result.Mapping.Columns, result.Mapping.Filter
	}
	if !p.ViaRoot {
		result.Source = leaf
	}
	return result, true, nil
}

// VisitRelation examines current explicit/schema/all facts along the leaf's
// real partition ancestry. It never enumerates all database tables for DML.
func VisitRelation(v catalog.Read, leaf *catalog.Relation, yield func(EffectiveTable) bool) error {
	if leaf != nil && v.Get(leaf.Address()) != leaf {
		return ids.ErrCatalogReplaced
	}
	if !IsPublishable(v, leaf) {
		return nil
	}
	for _, p := range v.Publications() {
		item, found, err := resolve(v, p, leaf)
		if err != nil {
			return err
		}
		if found && !yield(item) {
			break
		}
	}
	return nil
}

// VisitTables enumerates deduplicated presented relations, not DML-effective
// metadata. Ordinary inheritance was expanded by the defining command;
// partition ancestry remains dynamic, including future cross-schema leaves.
func VisitTables(v catalog.Read, p *catalog.Publication, yield func(*catalog.Relation) bool) error {
	if p == nil || v.Get(p.Address()) != p {
		return ids.ErrCatalogReplaced
	}
	seen := make(map[ids.ObjectAddress]bool)
	for _, r := range v.Relations() {
		if !IsPublishable(v, r) || r.Partition != nil && !p.ViaRoot {
			continue
		}
		item, found, err := resolve(v, p, r)
		if err != nil {
			return err
		}
		if !found || seen[item.Relation.Address()] {
			continue
		}
		seen[item.Relation.Address()] = true
		if !yield(item.Relation) {
			break
		}
	}
	return nil
}

// FilterSQL deparses the normal retained expression using current stable names.
func FilterSQL(ctx context.Context, v catalog.Read, m *catalog.PublicationRelation) (string, error) {
	if m == nil || m.Filter == nil {
		return "", nil
	}
	p := stored.PolicyOf(m.Filter)
	if p == nil {
		return "", fmt.Errorf("stuntgres: invalid publication filter payload")
	}
	return stored.PublicationFilterSQL(ctx, v, p)
}
