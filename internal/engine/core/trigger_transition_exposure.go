package core

// RI commands share the caller's AFTER frame. A ROW transition consumer at an
// exhausted RI producer exposes its complete segment before the next action can
// append more images. Other groups and statement-only sets remain pending.
func (s *StatementScope) exposeAfterTransitions(key afterStatementKey) error {
	if s.afterTriggers == nil {
		return nil
	}
	f := s.afterTriggers.frames[s.effectCommand]
	if f == nil || f.draining {
		return nil
	}
	segment := f.segments[key]
	if segment == nil || segment.frozen {
		return nil
	}
	exposes := false
	for _, event := range f.rows[f.row:] {
		p := event.definition.Parts()
		if !event.skip && event.segment == segment && (p.OldTable != "" || p.NewTable != "") {
			exposes = true
			break
		}
	}
	if !exposes {
		return nil
	}
	segment.frozen = true
	f.draining = true
	defer func() { f.draining = false }()
	for i := f.row; i < len(f.rows); i++ {
		event := f.rows[i]
		if event.skip || event.segment != segment {
			continue
		}
		f.rows[i].skip = true
		if err := s.fireTrigger(event); err != nil {
			return err
		}
	}
	for i := f.statement; i < len(f.statements); i++ {
		event := f.statements[i]
		if event.skip || event.segment != segment {
			continue
		}
		if !event.qualified {
			pass, err := s.qualifyAfterStatement(event)
			if err != nil {
				return err
			}
			f.statements[i].qualified = true
			if !pass {
				f.statements[i].skip = true
				continue
			}
		}
		f.statements[i].skip = true
		if err := s.fireTrigger(event); err != nil {
			return err
		}
	}
	return nil
}
