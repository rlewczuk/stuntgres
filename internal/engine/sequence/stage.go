package sequence

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Stage is the sequence slot of an engine CatalogStage: the generator
// replacements a stage publishes with its descriptors, and the
// identity-sequence captures publication validates against. The stage owns
// it privately; a nil slot is a stage without sequence work, and every read
// method accepts nil. The caller holds Engine.mu, as for the stage itself.
type Stage struct {
	generators map[ids.ObjectAddress]*storage.SequenceGenerator
	// identityOriginals hold the live descriptor an identity change started
	// from; publication compares against it instead of the grant catalog.
	identityOriginals    map[ids.ObjectAddress]catalog.Descriptor
	identityDependencies []session.DependencyObservation
}

// Generator returns the staged generator of one sequence, or nil.
func (s *Stage) Generator(address ids.ObjectAddress) *storage.SequenceGenerator {
	if s == nil {
		return nil
	}
	return s.generators[address]
}

// StagedGenerators returns the staged generators for reading; may be nil.
func (s *Stage) StagedGenerators() map[ids.ObjectAddress]*storage.SequenceGenerator {
	if s == nil {
		return nil
	}
	return s.generators
}

func (s *Stage) SetGenerator(address ids.ObjectAddress, generator *storage.SequenceGenerator) {
	if s.generators == nil {
		s.generators = make(map[ids.ObjectAddress]*storage.SequenceGenerator)
	}
	s.generators[address] = generator
}

func (s *Stage) DeleteGenerator(address ids.ObjectAddress) {
	if s != nil {
		delete(s.generators, address)
	}
}

// ResetGenerators drops every staged generator, sized for capacity more.
func (s *Stage) ResetGenerators(capacity int) {
	s.generators = make(map[ids.ObjectAddress]*storage.SequenceGenerator, capacity)
}

// IdentityOriginal returns the captured live descriptor of an identity sequence.
func (s *Stage) IdentityOriginal(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if s == nil {
		return nil, false
	}
	d, ok := s.identityOriginals[address]
	return d, ok
}

// IdentityOriginalMap returns the captures for reading; may be nil.
func (s *Stage) IdentityOriginalMap() map[ids.ObjectAddress]catalog.Descriptor {
	if s == nil {
		return nil
	}
	return s.identityOriginals
}

// CaptureIdentityOriginal records original unless the sequence already has a
// capture: the first capture is the one publication validates.
func (s *Stage) CaptureIdentityOriginal(address ids.ObjectAddress, original catalog.Descriptor) {
	if s.identityOriginals == nil {
		s.identityOriginals = make(map[ids.ObjectAddress]catalog.Descriptor)
	}
	if _, captured := s.identityOriginals[address]; !captured {
		s.identityOriginals[address] = original
	}
}

func (s *Stage) ObserveIdentityDependencies(observation session.DependencyObservation) {
	s.identityDependencies = append(s.identityDependencies, observation)
}

// IdentityDependencyList returns the observed identity dependencies; may be nil.
func (s *Stage) IdentityDependencyList() []session.DependencyObservation {
	if s == nil {
		return nil
	}
	return s.identityDependencies
}
