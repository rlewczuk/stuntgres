package view

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
)

// FirstQueryRowMode returns the first row-lock mode that root or a query leaf
// requests, through the stored views under the leaves, or 0. A mark inherited
// into a view reports its diagnostic mode. read is the statement's private
// catalog; a stale view reference is ids.ErrCatalogReplaced.
func FirstQueryRowMode(read catalog.Read, root plan.Node, leaves []exec.Leaf) (lock.Mode, error) {
	if _, err := stored.ValidateViewReadGraph(read, leaves, 0); err != nil {
		return 0, err
	}
	if mode := analyze.QueryRootRowMode(root); mode != 0 {
		return mode, nil
	}
	var visit func(*catalog.Relation, plan.RowMark, lock.Mode) (lock.Mode, error)
	visit = func(d *catalog.Relation, inherited plan.RowMark, diagnostic lock.Mode) (lock.Mode, error) {
		if d.RelationKind != catalog.ViewRelation {
			if inherited.Mode == 0 {
				return 0, nil
			}
			if diagnostic != 0 {
				return diagnostic, nil
			}
			return inherited.Mode, nil
		}
		if inherited.Mode != 0 {
			return analyze.RowLockDiagnostic(inherited), nil
		}
		if diagnostic == 0 {
			diagnostic = analyze.QueryRootRowMode(stored.ViewOf(d).Query().Root)
		}
		for i, leaf := range stored.ViewOf(d).Sources() {
			child, ok := read.Relation(leaf.Ref.Address)
			if !ok || !stored.ViewReferenceValid(read, d, leaf.Ref) {
				return 0, ids.ErrCatalogReplaced
			}
			first, err := visit(child, stored.ViewLeafRowMark(stored.ViewOf(d), i, inherited), diagnostic)
			if err != nil || first != 0 {
				return first, err
			}
		}
		return 0, nil
	}
	for _, leaf := range leaves {
		first, err := visit(leaf.Relation, leaf.Mark, analyze.RowLockDiagnostic(leaf.Mark))
		if err != nil || first != 0 {
			return first, err
		}
	}
	return 0, nil
}

// QueryLockDemand is what QueryLocks found. Marked reports that root, a
// stored view under the leaves or an inherited mark locks rows, so the
// statement needs a query lock context. Visibility reports that a stored view
// query reads a visibility snapshot.
type QueryLockDemand struct {
	Marked, Visibility bool
}

// QueryLocks validates the stored view read graph under the query leaves and
// the row-lock legality of every view and relation they reach: set outputs
// end reference marks, materialized views and sequences reject reference
// marks, and virtual catalog relations reject row locks. read is the
// statement's private catalog. On a walk error the result keeps Visibility
// from the validated read graph.
//
// Called before any source expression is opened. Inherited rowmark legality,
// kind and authorization checks remain Execute-only.
func QueryLocks(read catalog.Read, root plan.Node, leaves []exec.Leaf) (QueryLockDemand, error) {
	var demand QueryLockDemand
	demand.Marked = analyze.QueryRootRowMode(root) != 0
	visibility, err := stored.ValidateViewReadGraph(read, leaves, 0)
	if err != nil {
		return QueryLockDemand{}, err
	}
	demand.Visibility = visibility
	var visit func(*catalog.Relation, plan.RowMark, bool) error
	visit = func(d *catalog.Relation, inherited plan.RowMark, referenceMarked bool) error {
		if d.RelationKind == catalog.ViewRelation {
			if analyze.QueryHasSetOutput(stored.ViewOf(d).Query().Root) {
				referenceMarked = false
			}
			referenceMarked = referenceMarked || analyze.QueryRootRowMode(stored.ViewOf(d).Query().Root) != 0
			demand.Marked = demand.Marked || analyze.QueryRootRowMode(stored.ViewOf(d).Query().Root) != 0
			if err := stored.ViewRowLockLegality(stored.ViewOf(d), analyze.RowLockDiagnostic(inherited)); err != nil {
				return err
			}
			for i, leaf := range stored.ViewOf(d).Sources() {
				child, ok := read.Relation(leaf.Ref.Address)
				if !ok || !stored.ViewReferenceValid(read, d, leaf.Ref) {
					return ids.ErrCatalogReplaced
				}
				if err := visit(child, stored.ViewLeafRowMark(stored.ViewOf(d), i, inherited), referenceMarked); err != nil {
					return err
				}
			}
			return nil
		}
		if exec.IsPGSequencesRelation(d) {
			return nil // Expanded catalog view: no represented physical tuple input.
		}
		if (referenceMarked || inherited.Mode != 0) && d.RelationKind == catalog.MaterializedViewRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in materialized view %q", d.Name()))
		}
		// PostgreSQL validates reference rowmark kinds even for an unselected
		// sequence sibling. This does not confer UPDATE ACLs or tuple grants.
		if referenceMarked && d.RelationKind == catalog.SequenceRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in sequence %q", d.Name()))
		}
		if inherited.Mode == 0 {
			return nil
		}
		demand.Marked = true
		if d.RelationKind == catalog.CatalogRelation {
			return sqlError("0A000", "row locking is not supported for virtual catalog relations")
		}
		return nil
	}
	for _, leaf := range leaves {
		if err := visit(leaf.Relation, leaf.Mark, leaf.DeleteReference || leaf.Query != nil && len(leaf.Query.Locking) != 0); err != nil {
			return demand, err
		}
	}
	return demand, nil
}

// CurrentQuerySource validates an admitted query leaf's exact identity
// against read without resolving SQL names again: the reference's catalog
// domain and epoch, the relation's address, namespace, name, kind and row
// shape, and either its storage (current is the leaf relation's storage now;
// a materialized view may have been refreshed within its lineage) or, for a
// sequence, its plan stamp. A mismatch is ids.ErrCatalogReplaced.
//
// Metadata revisions may refresh authority before binding, never an already
// bound layout.
func CurrentQuerySource(read catalog.Read, domain *ids.Domain, epoch ids.CatalogEpoch, current *storage.Relation, source exec.Leaf) (*catalog.Relation, error) {
	relation, ok := read.Relation(source.Ref.Address)
	if !ok || source.Relation == nil || source.Ref.Domain() != domain || source.Ref.Epoch != epoch || source.Ref.Address.SubID != 0 ||
		relation.Address() != source.Relation.Address() || relation.Namespace() != source.Relation.Namespace() || relation.Name() != source.Relation.Name() || relation.RelationKind != source.Relation.RelationKind ||
		!index.SameRelationRowShape(relation, source.Relation) {
		return nil, ids.ErrCatalogReplaced
	}
	if relation.RelationKind == catalog.SequenceRelation {
		if relation.Sequence == nil || source.Storage != nil || relation.SequencePlanStamp != source.Relation.SequencePlanStamp {
			return nil, ids.ErrCatalogReplaced
		}
	} else if current != source.Storage && !SameMaterializedLineage(source.Relation, relation, source.Storage, current) {
		return nil, ids.ErrCatalogReplaced
	}
	return relation, nil
}
