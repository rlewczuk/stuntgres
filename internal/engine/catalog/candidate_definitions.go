package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// CandidateDefinitions is the DefinitionReader over a candidate: its base with
// the candidate's removals hidden and its descriptors laid over. Unlike the
// candidate's own reads, it merges and scans the base.
type CandidateDefinitions struct{ candidate *Candidate }

var _ DefinitionReader = CandidateDefinitions{}

// Definitions returns the candidate's definition reader.
func (c *Candidate) Definitions() CandidateDefinitions { return CandidateDefinitions{c} }

func (r CandidateDefinitions) Descriptor(address ids.ObjectAddress) (Descriptor, bool) {
	if _, removed := r.candidate.Removal(address); removed {
		return nil, false
	}
	if descriptor, exists := r.candidate.Find(address); exists {
		return descriptor, true
	}
	return r.candidate.Base.Descriptor(address)
}
func (r CandidateDefinitions) Lookup(key ids.NameKey) ids.ObjectAddress {
	return r.candidate.LookupName(key)
}
func (r CandidateDefinitions) Descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address := range r.candidate.Base.Descriptors() {
			if descriptor, exists := r.Descriptor(address); exists {
				if !yield(address, descriptor) {
					return
				}
			}
		}
		for address, descriptor := range r.candidate.Descriptors() {
			if _, original := r.candidate.Base.Find(address); original {
				continue
			}
			if _, removed := r.candidate.Removal(address); removed {
				continue
			}
			if !yield(address, descriptor) {
				return
			}
		}
	}
}

func (r CandidateDefinitions) ProceduresNamed(namespace ids.ObjectAddress, name string) iter.Seq[*Procedure] {
	return func(yield func(*Procedure) bool) {
		for _, address := range r.candidate.Base.State().ProceduresNamed(namespace, name) {
			if _, overridden := r.candidate.Find(address); overridden {
				continue
			}
			descriptor, present := r.Descriptor(address)
			if d, ok := descriptor.(*Procedure); present && ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
		for address, descriptor := range r.candidate.Descriptors() {
			if _, removed := r.candidate.Removal(address); removed {
				continue
			}
			if d, ok := descriptor.(*Procedure); ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
	}
}

func (r CandidateDefinitions) HasDynamicProcedureName(name string) bool {
	count := 0
	if index := r.candidate.Base.State().ProcedureNames(); index != nil {
		count = index.DynamicNames[name]
	}
	if count == 0 {
		for address, descriptor := range r.candidate.Descriptors() {
			if d, ok := descriptor.(*Procedure); ok && d.DynamicCallable() && d.Name() == name {
				if _, removed := r.candidate.Removal(address); !removed {
					return true
				}
			}
		}
		return false
	}
	for address := range r.candidate.Removals() {
		if d, ok := r.candidate.Base.Procedure(address); ok && d.DynamicCallable() && d.Name() == name {
			count--
		}
	}
	for address, descriptor := range r.candidate.Descriptors() {
		if _, removed := r.candidate.Removal(address); removed {
			continue
		}
		if old, ok := r.candidate.Base.Procedure(address); ok && old.DynamicCallable() && old.Name() == name {
			count--
		}
		if d, ok := descriptor.(*Procedure); ok && d.DynamicCallable() && d.Name() == name {
			count++
		}
	}
	return count > 0
}
