package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/table"
)

// A retirement receipt is a completed semantic decision, not an allow cache.
// It belongs to one real command and incarnation. Pure structural cleanup can
// finish that retirement after its parent/name/label has disappeared, without
// manufacturing authority from Base or replaying the decision under a returned
// caller. The command's ordinary MAC observations retain the actual phase and
// committed object/label predecessors through final publication.
type sepgsqlRetirementReceipt struct {
	reference         ids.ObjectRef
	stage             *CatalogStage
	predecessor       catalog.Descriptor
	source            catalog.Descriptor
	policy            *sepgsql.Runtime
	subject           string
	generation, frame uint64
	depth             int
	consumed          bool
	completion        uint64
}

func (p *DropProvider) sepgsqlRetirementOwner() *StatementScope {
	if p == nil || p.Stage == nil || p.Stage.dropCleanupOrigin != dropCleanupSQL || p.Stage.terminalCleanup || p.Owner.Sepgsql == nil || p.Stage.sepgsqlAuth.Sepgsql.Policy == nil {
		return nil
	}
	return p.Stage.statement
}

func (p *DropProvider) consumeSepgsqlRetirement(address ids.ObjectAddress) (bool, error) {
	scope := p.sepgsqlRetirementOwner()
	if scope == nil {
		if p != nil && p.Stage != nil && p.Stage.dropCleanupOrigin == dropCleanupSQL && !p.Stage.terminalCleanup && p.Stage.sepgsqlAuth.Sepgsql.Policy != nil {
			return false, ids.ErrCatalogReplaced
		}
		return false, nil
	}
	receipt, ok := scope.sepgsqlRetirements[address]
	if !ok {
		return false, nil
	}
	if scope.Finished || scope.Engine() != p.Owner || receipt.stage != p.Stage || p.Stage.statement != scope || receipt.policy != p.Owner.Sepgsql || receipt.reference != p.Owner.Catalog.Domain.Ref(p.Stage.Base.Epoch(), address) || receipt.reference.Epoch != scope.TxRef.Epoch() {
		return false, ids.ErrCatalogReplaced
	}
	if err := scope.Op.Context().Err(); err != nil {
		return false, err
	}
	parent := address
	parent.SubID = 0
	if p.Stage.Base.Get(parent) != receipt.predecessor {
		return false, ids.ErrCatalogReplaced
	}
	// Descriptor lookup is the real retirement owner's current/owned-removal
	// lineage, never a missing-name or unlabeled permission fallback.
	current := p.RetirementDescriptor(parent)
	if current == nil || current.Address() != receipt.source.Address() || current.Namespace() != receipt.source.Namespace() {
		return false, ids.ErrCatalogReplaced
	}
	if _, ok := current.(*catalog.Relation); ok {
		if _, same := receipt.source.(*catalog.Relation); !same {
			return false, ids.ErrCatalogReplaced
		}
	}
	if address.SubID == 0 && p.Removed(address) && !receipt.consumed {
		return false, ids.ErrCatalogReplaced
	}
	if receipt.consumed {
		completed := p.Removed(parent)
		if address.SubID != 0 {
			if relation, ok := p.Stage.Get(parent).(*catalog.Relation); ok {
				completed = completed || !relation.HasAttribute(address.SubID)
			}
		}
		if !completed {
			return false, ids.ErrCatalogReplaced
		}
	}
	receipt.consumed = true
	scope.sepgsqlRetirements[address] = receipt
	return true, nil
}

func sepgsqlRetirementTarget(source catalog.Descriptor, address ids.ObjectAddress) bool {
	if catalog.SepgsqlClass(source, address.SubID) != "" {
		return true
	}
	relation, ok := source.(*catalog.Relation)
	return address.SubID == 0 && ok && relation.RelationKind == catalog.IndexRelation
}

// PrepareSepgsqlRetirements consumes only the existing validated drop closure.
// Dependency discovery, grants, notices and the physical effect order stay with
// their existing owners. No SQL evaluation occurs here: the semantic callbacks
// are schema/target/attribute checks plus detached audit enqueueing. Selected
// children are consented before their owners, while all consulted labels live.
func (p *DropProvider) PrepareSepgsqlRetirements(targets []ids.ObjectAddress, edges []table.DomainDependency) error {
	return p.prepareSepgsqlRetirements(targets, edges, nil)
}
func (p *DropProvider) prepareSepgsqlRetirements(targets []ids.ObjectAddress, edges []table.DomainDependency, sources map[ids.ObjectAddress]catalog.Descriptor) (failure error) {
	scope := p.sepgsqlRetirementOwner()
	if scope == nil {
		if p.Stage != nil && p.Stage.dropCleanupOrigin == dropCleanupSQL && !p.Stage.terminalCleanup && p.Stage.sepgsqlAuth.Sepgsql.Policy != nil {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	if err := p.Ctx.Err(); err != nil {
		return err
	}
	auth, err := p.Stage.sepgsqlView()
	if err != nil {
		return err
	}
	type node struct {
		address  ids.ObjectAddress
		source   catalog.Descriptor
		children []int
		visited  uint8
	}
	var nodes []node
	var selected map[ids.ObjectAddress]int
	for _, address := range targets {
		switch address.ClassID {
		case ids.RelationClassID, ids.NamespaceClassID, ids.DatabaseClassID, ids.ProcedureClassID:
		default:
			continue
		}
		if _, exists := selected[address]; exists {
			continue
		}
		if receipt, exists := scope.sepgsqlRetirements[address]; exists {
			if receipt.stage != p.Stage || receipt.policy != p.Owner.Sepgsql {
				return ids.ErrCatalogReplaced
			}
			continue
		}
		parent := address
		parent.SubID = 0
		source := sources[address]
		if source == nil {
			source = auth.Catalog.Get(parent)
		}
		if source == nil {
			// A prior removal is not live authority. A class with no source MAC hook
			// needs no receipt; every supported already-gone object must have its own.
			previous := p.RetirementDescriptor(parent)
			if previous != nil && !sepgsqlRetirementTarget(previous, address) {
				continue
			}
			return ids.ErrCatalogReplaced
		}
		if !sepgsqlRetirementTarget(source, address) {
			continue
		}
		if nodes == nil {
			nodes = make([]node, 0, len(targets))
			selected = make(map[ids.ObjectAddress]int, len(targets))
		}
		selected[address] = len(nodes)
		nodes = append(nodes, node{address: address, source: source})
	}
	if len(nodes) == 0 {
		return nil
	}
	add := func(parent, child ids.ObjectAddress) {
		a, ok := selected[parent]
		if !ok {
			return
		}
		b, ok := selected[child]
		if !ok || a == b {
			return
		}
		nodes[a].children = append(nodes[a].children, b)
	}
	for _, edge := range edges {
		add(edge.Target, edge.Address)
	}
	for _, n := range nodes {
		if n.address.SubID != 0 {
			continue
		}
		add(n.source.Namespace(), n.address)
		if relation, ok := n.source.(*catalog.Relation); ok && relation.SQLIndex != nil {
			add(relation.SQLIndex.Parent, n.address)
		}
		if relation, ok := n.source.(*catalog.Relation); ok && relation.RelationKind == catalog.SequenceRelation && relation.Sequence.Owned != (ids.ObjectAddress{}) {
			add(relation.Sequence.Owned, n.address)
			parent := relation.Sequence.Owned
			parent.SubID = 0
			add(parent, n.address)
		}
	}
	mark := scope.MarkSepgsqlObservations()
	defer func() {
		if failure != nil {
			scope.RestoreSepgsqlObservations(mark)
		}
	}()
	var visit func(int) error
	visit = func(index int) error {
		n := &nodes[index]
		if n.visited != 0 {
			return nil
		}
		n.visited = 1
		for _, child := range n.children {
			if err := visit(child); err != nil {
				return err
			}
		}
		if err := p.Ctx.Err(); err != nil {
			return err
		}
		if n.address.SubID != 0 {
			relation, ok := n.source.(*catalog.Relation)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			if err := auth.SepgsqlAttributeDrop(relation, n.address); err != nil {
				return err
			}
		} else if err := p.sepgsqlDropConsent(n.source, false); err != nil {
			return err
		}
		subject, generation, frame, depth := scope.Op.Session.Sepgsql.PhaseSnapshot()
		completion, err := scope.completeSepgsqlRetirement()
		if err != nil {
			return err
		}
		if scope.sepgsqlRetirements == nil {
			scope.sepgsqlRetirements = make(map[ids.ObjectAddress]sepgsqlRetirementReceipt, len(nodes))
		}
		parent := n.address
		parent.SubID = 0
		scope.sepgsqlRetirements[n.address] = sepgsqlRetirementReceipt{reference: p.Owner.Catalog.Domain.Ref(p.Stage.Base.Epoch(), n.address), stage: p.Stage, predecessor: p.Stage.Base.Get(parent), source: n.source, policy: p.Owner.Sepgsql, subject: subject, generation: generation, frame: frame, depth: depth, completion: completion}
		n.visited = 2
		return nil
	}
	for index := range nodes {
		if err := visit(index); err != nil {
			return err
		}
	}
	return nil
}

// PrepareSepgsqlPreparedRetirements is called only after the genuine immutable
// preparation's lock grants and predecessor observations have been validated.
// It selects its actual removals/projections, not its larger discovery/read set.
// The preparation itself never retains a client decision or receipt.
func (p *DropProvider) PrepareSepgsqlPreparedRetirements(prepared *table.DropPreparation) error {
	if p.sepgsqlRetirementOwner() == nil {
		return nil
	}
	var targets []ids.ObjectAddress
	projected := make(map[ids.ObjectAddress]*catalog.Relation)
	sources := make(map[ids.ObjectAddress]catalog.Descriptor)
	for _, late := range prepared.LateConstraints {
		for _, idx := range late.Indexes {
			targets = append(targets, idx.Address())
		}
	}
	appendColumns := func(before, next *catalog.Relation) {
		for _, attribute := range before.Attributes {
			if !next.HasAttribute(attribute) {
				address := before.Address()
				address.SubID = attribute
				targets = append(targets, address)
				sources[address] = before
			}
		}
	}
	for _, effect := range prepared.Effects() {
		switch {
		case effect.Remove.Address != (ids.ObjectAddress{}):
			if !effect.Internal {
				targets = append(targets, effect.Remove.Address)
			}
		case effect.Before != nil:
			appendColumns(effect.Before, effect.After)
			projected[effect.After.Address()] = effect.After
		case effect.Descriptor != nil:
			if next, ok := effect.Descriptor.(*catalog.Relation); ok {
				before := projected[next.Address()]
				if before == nil {
					before, _ = p.Stage.Get(next.Address()).(*catalog.Relation)
				}
				if before != nil {
					appendColumns(before, next)
				}
				projected[next.Address()] = next
			}
		}
	}
	return p.prepareSepgsqlRetirements(targets, nil, sources)
}
