package engine

// domainAlterState returns the stage's ALTER DOMAIN slot, allocating it on
// first use.
func (s *CatalogStage) domainAlterState() *domainAlterStage {
	if s.domainAlter == nil {
		s.domainAlter = new(domainAlterStage)
	}
	return s.domainAlter
}
