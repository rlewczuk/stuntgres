package exec

// FinishSources closes reached nested producers at the actual capture-owner
// boundary. Borrowed invocations retain them on success or cooperative budget
// unwind; genuine failures abort them even outside Stage.Next. Data pins still
// belong to Source.Close, and cleanup never replaces a genuine primary error.
func (s *State) FinishSources(primary error) error {
	_, exhausted := primary.(*sampleBudget)
	if s.SourceMode == SourceBorrowed && (primary == nil || exhausted) {
		return primary
	}
	if exhausted {
		if err := s.Source.finishProducers(nil); err != nil {
			return err
		}
		return primary
	}
	return s.Source.finishProducers(primary)
}

func (s *State) finishInvocation(stage *Stage, primary error) error {
	if primary != nil {
		primary = s.FinishSources(primary)
	}
	closeErr := stage.Close()
	if _, exhausted := primary.(*sampleBudget); exhausted && closeErr != nil {
		return s.FinishSources(closeErr)
	}
	if primary != nil {
		return primary
	}
	if closeErr != nil {
		return s.FinishSources(closeErr)
	}
	return nil
}

func (s *Source) finishProducers(primary error) error {
	if s == nil {
		return primary
	}
	s.discardProjection()
	s.finishReadLeases()
	failure := primary
	if stage := s.NestedStage; stage != nil {
		if primary != nil {
			if !stage.Closed {
				_ = stage.fail(primary)
			}
		} else {
			if stage.Terminal != nil {
				failure = stage.Terminal
			}
			if err := stage.Close(); failure == nil {
				failure = err
			}
		}
	}
	// Follow only existing source-owning edges. Lineage/locks/RLS borrow these
	// captures or own separate programs; they are not a second cleanup registry.
	for _, child := range s.Sources {
		if child != s {
			if err := child.finishProducers(primary); failure == nil {
				failure = err
			}
		}
	}
	for _, child := range []*Source{s.NestedInputs, s.Input} {
		if err := child.finishProducers(primary); failure == nil {
			failure = err
		}
	}
	for _, segment := range s.Inheritance {
		if err := segment.Prefix.finishProducers(primary); failure == nil {
			failure = err
		}
	}
	return failure
}
