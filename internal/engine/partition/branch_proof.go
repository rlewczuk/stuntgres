package partition

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// BranchProof owns one bounded conservative search over captured metadata. Both
// execution and EXPLAIN use it; only execution translates decisions to ordinals.
// Callers enforce row-security/row-lock safety and supply the lexical frame.
// Unknown predicates/providers remain possible, never an exclusion.
type BranchProof struct {
	proof             pruneProof
	query, queryNodes int
	mode              string
	inherited         bool
}

func NewBranchProof(ctx context.Context, evaluation *exec.Context, view catalog.Read, named *catalog.Relation, ref ids.ObjectRef, predicate expr.Expr, mode string, inherited bool) (BranchProof, error) {
	p := BranchProof{proof: newPruneProof(ctx, evaluation, view, named, ref), mode: mode, inherited: inherited}
	var err error
	p.query, err = p.proof.normalize(predicate, false)
	p.queryNodes = len(p.proof.nodes)
	return p, err
}

func (p *BranchProof) Possible(physical *catalog.Relation) (bool, error) {
	p.proof.nodes = p.proof.nodes[:p.queryNodes]
	// An inheritance child is a branch of this scan, not simply any table
	// which happens to have an inheritance parent in catalog metadata.
	constraints := p.mode == "on" || p.mode == "partition" && p.inherited && physical.Address() != p.proof.named.Address()
	return p.proof.branch(p.query, physical, constraints)
}
