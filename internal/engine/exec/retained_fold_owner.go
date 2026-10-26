package exec

import "stuntgres/internal/engine/ids"

// FoldDefinition owns publication rights and typed resources for one prepared
// definition. Its caller serializes mutations with statement admission. It owns
// no analysis, execution graph, environment or authority.
type FoldDefinition struct {
	live         bool
	valid        bool
	all          bool
	generation   uint64
	entries      []FoldConstant
	dependencies []FoldDependency
}

// FoldAttempt borrows the definition's immutable backing until planning changes
// its Candidate. Only the exact live definition and captured generation may
// accept it. The caller submits it only after complete successful planning and
// current environment, authority and cancellation validation.
type FoldAttempt struct {
	Candidate  FoldCaptureCandidate
	owner      *FoldDefinition
	generation uint64
}

// AcceptedFolds is the actual immutable planning input retained by a definition
// or binding. Entries, their Inputs and Dependencies must not be mutated. Saving
// this value preserves both its backing and generation after later retirement.
type AcceptedFolds struct {
	Entries      []FoldConstant
	Dependencies []FoldDependency
	Generation   uint64
}

func NewFoldDefinition() *FoldDefinition { return &FoldDefinition{live: true} }

func (d *FoldDefinition) Live() bool { return d != nil && d.live }

// Current borrows the real retained resources without allocating or copying.
// Previously accepted bindings keep their independently saved AcceptedFolds
// regardless of the definition's current reuse eligibility.
func (d *FoldDefinition) Current() AcceptedFolds {
	return AcceptedFolds{Entries: d.entries, Dependencies: d.dependencies, Generation: d.generation}
}

// Candidate borrows this accepted generation for portal planning. It has no
// definition publication rights and fixed misses reject rather than recompute.
func (a AcceptedFolds) Candidate() FoldCaptureCandidate {
	return FoldCaptureCandidate{Entries: a.Entries, Dependencies: a.Dependencies, Fixed: true}
}

// Begin retires ineligible root syntax in the operation-local candidate, leaving
// immutable accepted groups untouched. Stored origins have their own exact
// defining lifetime and survive root-only invalidation until the stored owner
// validates/prunes them. An unchanged hit never allocates or copies.
func (d *FoldDefinition) Begin() FoldAttempt {
	a := FoldAttempt{owner: d, generation: d.generation}
	a.Candidate = FoldCaptureCandidate{Entries: d.entries, Dependencies: d.dependencies}
	if !d.valid {
		if d.all {
			a.Candidate.Entries, a.Candidate.Private = nil, len(d.entries) != 0
			a.Candidate.Dependencies, a.Candidate.DependenciesPrivate = nil, len(d.dependencies) != 0
		} else {
			for i, entry := range d.entries {
				if entry.Origin.Owner != (ids.ObjectRef{}) {
					continue
				}
				next := make([]FoldConstant, 0, len(d.entries)-1)
				next = append(next, d.entries[:i]...)
				for _, remaining := range d.entries[i+1:] {
					if remaining.Origin.Owner != (ids.ObjectRef{}) {
						next = append(next, remaining)
					}
				}
				a.Candidate.Entries, a.Candidate.Private = next, true
				break
			}
		}
	}
	return a
}

// Invalidate monotonically retires reuse eligibility, including escalation from
// root-only to all origins. Repeated identical invalidations do not advance the
// generation; restoring an environment cannot resurrect retired root syntax.
func (d *FoldDefinition) Invalidate(all bool) {
	if d.valid || all && !d.all {
		d.generation++
	}
	d.valid = false
	d.all = d.all || all
}

// Accept installs one complete candidate. All semantic/current-authority checks
// belong to the prepared runtime owner and precede this transition. COW flags
// identify changed backing; unchanged hits preserve the exact generation.
func (d *FoldDefinition) Accept(a *FoldAttempt) (AcceptedFolds, error) {
	if !d.Live() || a == nil || a.owner != d || a.generation != d.generation || a.Candidate.Sealed || a.Candidate.Fixed {
		return AcceptedFolds{}, ids.ErrCatalogReplaced
	}
	PruneFoldDependencies(&a.Candidate)
	if a.Candidate.Private || a.Candidate.DependenciesPrivate {
		d.entries, d.dependencies = a.Candidate.Entries, a.Candidate.Dependencies
		d.generation++
	}
	d.valid, d.all = true, false
	a.Candidate.Sealed = true
	return d.Current(), nil
}

// Retire ends definition publication rights, not accepted binding lifetimes.
func (d *FoldDefinition) Retire() {
	if d == nil {
		return
	}
	d.live, d.valid = false, false
	d.generation++
}
