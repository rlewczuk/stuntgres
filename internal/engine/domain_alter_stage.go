package engine

// domainAlterState returns the stage's ALTER DOMAIN slot, allocating it on
// first use.
func (s *CatalogStage) domainAlterState() *DomainAlterStage {
	if s.domainAlter == nil {
		s.domainAlter = new(DomainAlterStage)
	}
	return s.domainAlter
}
