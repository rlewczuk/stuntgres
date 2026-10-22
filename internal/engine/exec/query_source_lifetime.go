package exec

// FinishSources closes reached nested producers at the actual capture-owner
// boundary. Successful borrowed invocations retain them; a primary failure
// aborts them even when it arose outside Stage.Next. Data pins still belong to
// Source.Close, and cleanup failure never replaces an existing primary error.
func (s *State) FinishSources(primary error) error {
	if primary == nil && s.SourceMode == SourceBorrowed {
		return nil
	}
	return s.Source.finishProducers(primary)
}

func (s *State) finishInvocation(stage *Stage, primary error) error {
	if primary != nil {
		_ = s.FinishSources(primary)
	}
	closeErr := stage.Close()
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
