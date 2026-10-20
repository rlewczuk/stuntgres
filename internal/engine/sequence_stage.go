package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// sequenceStage is CatalogStage's sequence slot: the generator replacements a
// stage publishes with its descriptors, and the identity-sequence captures
// publication validates against. The stage owns it privately; a nil slot is
// a stage without sequence work, and every read method accepts nil.
type sequenceStage struct {
	generators map[ids.ObjectAddress]*storage.SequenceGenerator
	// identityOriginals hold the live descriptor an identity change started
	// from; publication compares against it instead of the grant catalog.
	identityOriginals    map[ids.ObjectAddress]catalog.Descriptor
	identityDependencies []session.DependencyObservation
}

// sequenceState returns the stage's sequence slot, allocating it on first use.
func (s *CatalogStage) sequenceState() *sequenceStage {
	if s.sequence == nil {
		s.sequence = &sequenceStage{}
	}
	return s.sequence
}

// generator returns the staged generator of one sequence, or nil.
func (s *sequenceStage) generator(address ids.ObjectAddress) *storage.SequenceGenerator {
	if s == nil {
		return nil
	}
	return s.generators[address]
}

// stagedGenerators returns the staged generators for reading; may be nil.
func (s *sequenceStage) stagedGenerators() map[ids.ObjectAddress]*storage.SequenceGenerator {
	if s == nil {
		return nil
	}
	return s.generators
}

func (s *sequenceStage) setGenerator(address ids.ObjectAddress, generator *storage.SequenceGenerator) {
	if s.generators == nil {
		s.generators = make(map[ids.ObjectAddress]*storage.SequenceGenerator)
	}
	s.generators[address] = generator
}

func (s *sequenceStage) deleteGenerator(address ids.ObjectAddress) {
	if s != nil {
		delete(s.generators, address)
	}
}

// resetGenerators drops every staged generator, sized for capacity more.
func (s *sequenceStage) resetGenerators(capacity int) {
	s.generators = make(map[ids.ObjectAddress]*storage.SequenceGenerator, capacity)
}

// identityOriginal returns the captured live descriptor of an identity sequence.
func (s *sequenceStage) identityOriginal(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if s == nil {
		return nil, false
	}
	d, ok := s.identityOriginals[address]
	return d, ok
}

// identityOriginalMap returns the captures for reading; may be nil.
func (s *sequenceStage) identityOriginalMap() map[ids.ObjectAddress]catalog.Descriptor {
	if s == nil {
		return nil
	}
	return s.identityOriginals
}

// captureIdentityOriginal records original unless the sequence already has a
// capture: the first capture is the one publication validates.
func (s *sequenceStage) captureIdentityOriginal(address ids.ObjectAddress, original catalog.Descriptor) {
	if s.identityOriginals == nil {
		s.identityOriginals = make(map[ids.ObjectAddress]catalog.Descriptor)
	}
	if _, captured := s.identityOriginals[address]; !captured {
		s.identityOriginals[address] = original
	}
}

func (s *sequenceStage) observeIdentityDependencies(observation session.DependencyObservation) {
	s.identityDependencies = append(s.identityDependencies, observation)
}

// identityDependencyList returns the observed identity dependencies; may be nil.
func (s *sequenceStage) identityDependencyList() []session.DependencyObservation {
	if s == nil {
		return nil
	}
	return s.identityDependencies
}
