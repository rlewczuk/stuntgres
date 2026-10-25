package engine

import "stuntgres/internal/engine/typedef"

// DomainAlterState returns the stage's ALTER DOMAIN slot, allocating it on
// first use.
func (s *CatalogStage) DomainAlterState() *typedef.DomainAlterStage {
	if s.domainAlter == nil {
		s.domainAlter = new(typedef.DomainAlterStage)
	}
	return s.domainAlter
}
