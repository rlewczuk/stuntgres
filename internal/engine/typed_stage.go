package engine

// typedState returns the stage's typed-table slot, allocating it on first use.
func (s *CatalogStage) typedState() *TypedStage {
	if s.typed == nil {
		s.typed = new(TypedStage)
	}
	return s.typed
}
