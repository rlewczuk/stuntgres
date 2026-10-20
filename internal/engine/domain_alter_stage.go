package engine

import "stuntgres/internal/engine/typedef"

// domainAlterState returns the stage's ALTER DOMAIN slot, allocating it on
// first use.
func (s *CatalogStage) domainAlterState() *typedef.DomainAlterStage {
	if s.domainAlter == nil {
		s.domainAlter = new(typedef.DomainAlterStage)
	}
	return s.domainAlter
}
