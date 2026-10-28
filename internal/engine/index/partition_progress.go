package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// partitionBuildProgress belongs to the detached command stage. No callback
// counts metadata staging as a newly built leaf. Virtual parents finish only
// after their children, independently of the publication map's traversal order.
type partitionBuildProgress struct {
	nodes    map[ids.ObjectAddress]*partitionBuildNode
	observer interface{ Partitions(int, int) }
	done     int
}
type partitionBuildNode struct {
	parent                                  ids.ObjectAddress
	pending                                 int
	virtual, complete, attached, suppressed bool
}

func (b *ProgressBinding) PartitionTree(view, base catalog.Read, root ids.ObjectAddress) {
	observer, ok := b.Observer.(interface{ Partitions(int, int) })
	if !ok {
		return
	}
	children := make(map[ids.ObjectAddress][]*catalog.Relation)
	for _, idx := range view.Relations() {
		if idx.SQLIndex != nil && idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
			parent := idx.SQLIndex.PartitionParent
			children[parent] = append(children[parent], idx)
		}
	}
	state := &partitionBuildProgress{nodes: make(map[ids.ObjectAddress]*partitionBuildNode), observer: observer}
	b.Targets = make(map[ids.ObjectAddress]ids.ObjectAddress)
	var collect func(ids.ObjectAddress, bool)
	collect = func(parent ids.ObjectAddress, suppressed bool) {
		for _, idx := range children[parent] {
			table := view.MustRelation(idx.SQLIndex.Parent)
			attached := !suppressed && base.Get(idx.Address()) != nil
			state.nodes[idx.Address()] = &partitionBuildNode{parent: parent, pending: len(children[idx.Address()]), virtual: table.Partition != nil, attached: attached, suppressed: suppressed}
			if !suppressed && !attached {
				b.Targets[idx.Address()] = idx.SQLIndex.Parent
			}
			collect(idx.Address(), suppressed || attached)
		}
	}
	collect(root, false)
	b.partitions = state
	observer.Partitions(len(state.nodes), 0)
	// Existing leaf roots are attached, rather than rebuilt. Empty virtual
	// children also have genuinely finished their (empty) descendant operation.
	for address, node := range state.nodes {
		if node.attached || node.virtual && node.pending == 0 && !node.suppressed {
			state.complete(address)
		}
	}
}

func (b ProgressBinding) Complete(address ids.ObjectAddress) {
	if b.partitions != nil {
		b.partitions.complete(address)
	}
}

func (p *partitionBuildProgress) complete(address ids.ObjectAddress) {
	node := p.nodes[address]
	if node == nil || node.complete || node.suppressed {
		return
	}
	node.complete = true
	p.done++
	p.observer.Partitions(len(p.nodes), p.done)
	if parent := p.nodes[node.parent]; parent != nil {
		parent.pending--
		if parent.virtual && parent.pending == 0 {
			p.complete(node.parent)
		}
	}
}
