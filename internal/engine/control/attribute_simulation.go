package control

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type attributeIdentity struct {
	Name string
	Hole attributeSlot
}
type simulatedOrigin struct {
	Parent ids.ObjectAddress
	Number int32
}
type simulatedAttribute struct {
	Identity       attributeIdentity
	Local, Dropped bool
	Origins        []simulatedOrigin
	DroppedCount   int
}
type simulatedRelation struct {
	Created, Removed bool
	Attributes       []simulatedAttribute
}
type attributeOperation struct {
	Kind         byte // c CREATE, a ADD, d DROP, n detach helper, x helper teardown
	Relation     ids.ObjectAddress
	Helper       ids.ObjectAddress
	Number       int32
	Identity     attributeIdentity
	Locals       []int32 // captured local redeclarations merged by CREATE INHERITS
	Participants []attributeSlot
}
type attributeSimulation struct {
	Relations  map[ids.ObjectAddress]simulatedRelation
	Groups     map[attributeSlot]attributeSlot
	Added      map[attributeSlot]bool
	Dropped    map[attributeSlot]bool
	Detached   map[attributeEdge]bool
	Operations []attributeOperation
}
type attributeScheduler struct {
	Relations []*catalog.Relation
	ByAddress map[ids.ObjectAddress]*catalog.Relation
	Helpers   map[ids.ObjectAddress]ids.ObjectAddress
}

func (s *attributeSimulation) clone() *attributeSimulation {
	n := &attributeSimulation{Relations: make(map[ids.ObjectAddress]simulatedRelation, len(s.Relations)), Groups: make(map[attributeSlot]attributeSlot, len(s.Groups)), Added: make(map[attributeSlot]bool, len(s.Added)), Dropped: make(map[attributeSlot]bool, len(s.Dropped)), Operations: append([]attributeOperation(nil), s.Operations...)}
	n.Detached = make(map[attributeEdge]bool, len(s.Detached))
	for k, v := range s.Detached {
		n.Detached[k] = v
	}
	for address, r := range s.Relations {
		r.Attributes = append([]simulatedAttribute(nil), r.Attributes...)
		for i := range r.Attributes {
			r.Attributes[i].Origins = append([]simulatedOrigin(nil), r.Attributes[i].Origins...)
		}
		n.Relations[address] = r
	}
	for k, v := range s.Groups {
		n.Groups[k] = v
	}
	for k, v := range s.Added {
		n.Added[k] = v
	}
	for k, v := range s.Dropped {
		n.Dropped[k] = v
	}
	return n
}

func expectedAttribute(r *catalog.Relation, number int32) (name string, dropped, local bool, count int, exists bool) {
	if slot := r.IndexForAttribute(number); slot >= 0 {
		local = true
		if r.Inheritance != nil {
			p := r.Inheritance.Columns[number]
			local = p.Local
			count = len(p.Origins)
		}
		return r.Columns[slot].Name, false, local, count, true
	}
	if fact, ok := r.ColumnMeta.Dropped[number]; ok {
		return "", true, fact.Local, int(fact.Inheritance), true
	}
	return "", false, false, 0, false
}

func simulatedLive(r simulatedRelation, identity attributeIdentity) int {
	for i, a := range r.Attributes {
		if !a.Dropped && a.Identity == identity {
			return i
		}
	}
	return -1
}

func (p *attributeScheduler) inherit(s *attributeSimulation, address ids.ObjectAddress, parent ids.ObjectAddress, parentNumber int32, identity attributeIdentity, creating bool, locals *[]int32) bool {
	r := s.Relations[address]
	if !r.Created {
		return false
	}
	origin := simulatedOrigin{parent, parentNumber}
	if slot := simulatedLive(r, identity); slot >= 0 {
		a := &r.Attributes[slot]
		if !slices.Contains(a.Origins, origin) {
			a.Origins = append(a.Origins, origin)
		}
		s.Relations[address] = r
		return true
	}
	number := int32(len(r.Attributes) + 1)
	d := p.ByAddress[address]
	name, dropped, local, _, exists := expectedAttribute(d, number)
	if !exists || dropped != (identity.Hole.Number != 0) || !dropped && name != identity.Name || local && !creating {
		return false
	}
	key := attributeSlot{address, number}
	if dropped {
		if group, ok := s.Groups[key]; ok && group != identity.Hole {
			return false
		}
		s.Groups[key] = identity.Hole
	}
	if local {
		s.Added[key] = true
		*locals = append(*locals, number)
	}
	r.Attributes = append(r.Attributes, simulatedAttribute{Identity: identity, Local: local, Origins: []simulatedOrigin{origin}})
	s.Relations[address] = r
	return true
}

func (p *attributeScheduler) create(s *attributeSimulation, d *catalog.Relation) bool {
	if s.Relations[d.Address()].Created {
		return false
	}
	if d.Inheritance != nil {
		for _, edge := range d.Inheritance.Parents {
			if !s.Relations[edge.Relation].Created {
				return false
			}
		}
	}
	s.Relations[d.Address()] = simulatedRelation{Created: true}
	op := attributeOperation{Kind: 'c', Relation: d.Address()}
	if d.Inheritance != nil {
		for _, edge := range d.Inheritance.Parents {
			parent := s.Relations[edge.Relation]
			for i, a := range parent.Attributes {
				if a.Dropped {
					continue
				}
				if !p.inherit(s, d.Address(), edge.Relation, int32(i+1), a.Identity, true, &op.Locals) {
					return false
				}
			}
		}
	}
	s.Operations = append(s.Operations, op)
	return true
}

func (p *attributeScheduler) propagate(s *attributeSimulation, parent ids.ObjectAddress, number int32, identity attributeIdentity, seen map[simulatedOrigin]bool) bool {
	key := simulatedOrigin{parent, number}
	if seen[key] {
		return true
	}
	seen[key] = true
	for _, child := range p.Relations {
		if !s.Relations[child.Address()].Created || child.Inheritance == nil {
			continue
		}
		for _, edge := range child.Inheritance.Parents {
			if edge.Relation != parent || s.Detached[attributeEdge{child.Address(), parent}] {
				continue
			}
			var unused []int32
			if !p.inherit(s, child.Address(), parent, number, identity, false, &unused) {
				return false
			}
			slot := simulatedLive(s.Relations[child.Address()], identity)
			if !p.propagate(s, child.Address(), int32(slot+1), identity, seen) {
				return false
			}
		}
	}
	return true
}

func (p *attributeScheduler) add(s *attributeSimulation, d *catalog.Relation, number int32, group attributeSlot) bool {
	r := s.Relations[d.Address()]
	key := attributeSlot{d.Address(), number}
	name, dropped, local, _, exists := expectedAttribute(d, number)
	if !exists || !local || !r.Created || s.Added[key] || int32(len(r.Attributes)+1) != number {
		return false
	}
	identity := attributeIdentity{Name: name}
	if dropped {
		identity = attributeIdentity{Hole: group}
		s.Groups[key] = group
	}
	if simulatedLive(r, identity) >= 0 {
		return false
	}
	r.Attributes = append(r.Attributes, simulatedAttribute{Identity: identity, Local: true})
	s.Relations[d.Address()] = r
	s.Added[key] = true
	if !p.propagate(s, d.Address(), number, identity, make(map[simulatedOrigin]bool)) {
		return false
	}
	s.Operations = append(s.Operations, attributeOperation{Kind: 'a', Relation: d.Address(), Number: number, Identity: identity})
	return true
}

func (p *attributeScheduler) removeInherited(s *attributeSimulation, parent ids.ObjectAddress, parentNumber int32, identity attributeIdentity, participants *[]attributeSlot) bool {
	for _, child := range p.Relations {
		r := s.Relations[child.Address()]
		if !r.Created {
			continue
		}
		slot := simulatedLive(r, identity)
		if slot < 0 {
			continue
		}
		a := r.Attributes[slot]
		origin := simulatedOrigin{parent, parentNumber}
		index := slices.Index(a.Origins, origin)
		if index < 0 {
			continue
		}
		before := len(a.Origins)
		member := attributeSlot{child.Address(), int32(slot + 1)}
		if !slices.Contains(*participants, member) {
			*participants = append(*participants, member)
		}
		a.Origins = slices.Delete(a.Origins, index, index+1)
		if !a.Local && len(a.Origins) == 0 {
			a.Dropped = true
			a.DroppedCount = before
			_, dropped, local, count, exists := expectedAttribute(child, int32(slot+1))
			if !exists || !dropped || local != a.Local || count != before {
				return false
			}
			r.Attributes[slot] = a
			s.Relations[child.Address()] = r
			if !p.removeInherited(s, child.Address(), int32(slot+1), identity, participants) {
				return false
			}
		} else {
			r.Attributes[slot] = a
			s.Relations[child.Address()] = r
		}
	}
	return true
}

func (p *attributeScheduler) drop(s *attributeSimulation, d *catalog.Relation, number int32) bool {
	key := attributeSlot{d.Address(), number}
	r := s.Relations[d.Address()]
	if !r.Created || s.Dropped[key] || number <= 0 || int(number) > len(r.Attributes) {
		return false
	}
	a := r.Attributes[number-1]
	_, dropped, local, count, exists := expectedAttribute(d, number)
	if !exists || !dropped || !local || a.Dropped || !a.Local || len(a.Origins) != 0 || count != 0 {
		return false
	}
	a.Dropped = true
	a.DroppedCount = 0
	r.Attributes[number-1] = a
	s.Relations[d.Address()] = r
	s.Dropped[key] = true
	op := attributeOperation{Kind: 'd', Relation: d.Address(), Number: number, Identity: a.Identity, Participants: []attributeSlot{key}}
	if !p.removeInherited(s, d.Address(), number, a.Identity, &op.Participants) {
		return false
	}
	s.Operations = append(s.Operations, op)
	return true
}

func (p *attributeScheduler) complete(s *attributeSimulation) bool {
	for _, d := range p.Relations {
		r := s.Relations[d.Address()]
		if _, helper := p.Helpers[d.Address()]; helper {
			if !r.Removed {
				return false
			}
			continue
		}
		if !r.Created || len(r.Attributes) != int(d.ColumnMeta.HighWater) {
			return false
		}
		for i, a := range r.Attributes {
			name, dropped, local, count, exists := expectedAttribute(d, int32(i+1))
			if !exists || a.Dropped != dropped || a.Local != local {
				return false
			}
			if dropped {
				if a.DroppedCount != count {
					return false
				}
				continue
			}
			if a.Identity.Name != name || a.Identity.Hole.Number != 0 || len(a.Origins) != count {
				return false
			}
			if d.Inheritance != nil {
				for _, origin := range d.Inheritance.Columns[int32(i+1)].Origins {
					if !slices.Contains(a.Origins, simulatedOrigin{origin.Parent, origin.Attribute}) {
						return false
					}
				}
			}
		}
	}
	return true
}

func (p *attributeScheduler) search(ctx context.Context, s *attributeSimulation) (*attributeSimulation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.complete(s) {
		return s, nil
	}
	for _, d := range p.Relations {
		if !s.Relations[d.Address()].Created {
			n := s.clone()
			if p.create(n, d) {
				if result, err := p.search(ctx, n); result != nil || err != nil {
					return result, err
				}
			}
		}
	}
	for _, d := range p.Relations {
		r := s.Relations[d.Address()]
		if !r.Created {
			continue
		}
		number := int32(len(r.Attributes) + 1)
		_, dropped, local, _, exists := expectedAttribute(d, number)
		if !exists || !local {
			continue
		}
		groups := []attributeSlot{{d.Address(), number}}
		if dropped {
			groups = nil
			for _, owner := range p.Relations {
				for n := int32(1); n <= owner.ColumnMeta.HighWater; n++ {
					key := attributeSlot{owner.Address(), n}
					if group, ok := s.Groups[key]; ok && !p.related(d.Address(), group.Relation) && !slices.Contains(groups, group) {
						groups = append(groups, group)
					}
				}
			}
			own := attributeSlot{d.Address(), number}
			if !slices.Contains(groups, own) {
				groups = append(groups, own)
			}
		}
		for _, group := range groups {
			n := s.clone()
			if p.add(n, d, number, group) {
				if result, err := p.search(ctx, n); result != nil || err != nil {
					return result, err
				}
			}
		}
	}
	for _, d := range p.Relations {
		for number := int32(1); number <= d.ColumnMeta.HighWater; number++ {
			if _, dropped, local, _, exists := expectedAttribute(d, number); !exists || !dropped || !local {
				continue
			}
			n := s.clone()
			if p.drop(n, d, number) {
				if result, err := p.search(ctx, n); result != nil || err != nil {
					return result, err
				}
			}
		}
	}
	for _, helper := range p.Relations {
		child, generated := p.Helpers[helper.Address()]
		if !generated {
			continue
		}
		h := s.Relations[helper.Address()]
		if !h.Created || h.Removed || len(h.Attributes) != int(helper.ColumnMeta.HighWater) || !s.Relations[child].Created {
			continue
		}
		allDropped := true
		for _, a := range h.Attributes {
			allDropped = allDropped && a.Dropped
		}
		if !allDropped {
			continue
		}
		edge := attributeEdge{child, helper.Address()}
		n := s.clone()
		if !n.Detached[edge] {
			n.Detached[edge] = true
			n.Operations = append(n.Operations, attributeOperation{Kind: 'n', Relation: child, Helper: helper.Address()})
		} else {
			h.Removed = true
			n.Relations[helper.Address()] = h
			n.Operations = append(n.Operations, attributeOperation{Kind: 'x', Relation: helper.Address()})
		}
		if result, err := p.search(ctx, n); result != nil || err != nil {
			return result, err
		}
	}
	return nil, nil
}

func (p *attributeScheduler) ancestor(parent, child ids.ObjectAddress) bool {
	if parent == child {
		return true
	}
	d := p.ByAddress[child]
	if d == nil || d.Inheritance == nil {
		return false
	}
	for _, edge := range d.Inheritance.Parents {
		if p.ancestor(parent, edge.Relation) {
			return true
		}
	}
	return false
}
func (p *attributeScheduler) related(a, b ids.ObjectAddress) bool {
	return p.ancestor(a, b) || p.ancestor(b, a)
}

func scheduleAttributes(ctx context.Context, relations []*catalog.Relation, helpers map[ids.ObjectAddress]ids.ObjectAddress) (*attributeSimulation, error) {
	p := attributeScheduler{Relations: relations, Helpers: helpers, ByAddress: make(map[ids.ObjectAddress]*catalog.Relation, len(relations))}
	for _, d := range relations {
		p.ByAddress[d.Address()] = d
	}
	initial := &attributeSimulation{Relations: make(map[ids.ObjectAddress]simulatedRelation), Groups: make(map[attributeSlot]attributeSlot), Added: make(map[attributeSlot]bool), Dropped: make(map[attributeSlot]bool)}
	initial.Detached = make(map[attributeEdge]bool)
	result, err := p.search(ctx, initial)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("stuntgres: no source-shaped attribute construction schedule")
	}
	return result, nil
}
