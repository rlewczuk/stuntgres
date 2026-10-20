package engine

// typedState returns the stage's typed-table slot, allocating it on first use.
func (s *CatalogStage) typedState() *typedStage {
	if s.typed == nil {
		s.typed = new(typedStage)
	}
	return s.typed
}
