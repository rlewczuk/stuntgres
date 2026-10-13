package parser

func makeWindowFrame(l *lexer, unit lexeme, start, end FrameBound, exclusion FrameExclusion, between bool) (*WindowFrame, bool) {
	message := ""
	pos := end.Pos
	switch {
	case start.Kind == FrameUnboundedFollowing:
		message = "frame start cannot be UNBOUNDED FOLLOWING"
		pos = start.Pos
	case end.Kind == FrameUnboundedPreceding:
		message = "frame end cannot be UNBOUNDED PRECEDING"
	case (unit.text == "groups" || unit.text == "rows") && !between && start.Kind == FrameOffsetFollowing:
		message = "frame starting from following row cannot end with current row"
		pos = start.Pos
	case start.Kind == FrameCurrentRow && end.Kind == FrameOffsetPreceding:
		message = "frame starting from current row cannot have preceding rows"
	case start.Kind == FrameOffsetFollowing && (end.Kind == FrameCurrentRow || end.Kind == FrameOffsetPreceding):
		message = "frame starting from following row cannot have preceding rows"
	}
	if message != "" {
		l.failAt("42P20", message, pos.Offset)
		return nil, false
	}
	if unit.text != "range" && unit.text != "groups" && unit.text != "rows" {
		l.failAt("0A000", "explicit window frames are unsupported", unit.pos.Offset)
		return nil, false
	}
	frameUnit := FrameRange
	if unit.text == "groups" {
		frameUnit = FrameGroups
	} else if unit.text == "rows" {
		frameUnit = FrameRows
	}
	return &WindowFrame{Unit: frameUnit, Exclusion: exclusion, Start: start, End: end, Pos: unit.pos}, true
}

// Offsets is a fixed-size borrowed expression view for every parser visitor.
func (w *WindowSpec) Offsets() [2]Expr {
	if w == nil || w.Frame == nil {
		return [2]Expr{}
	}
	return [2]Expr{w.Frame.Start.Offset, w.Frame.End.Offset}
}

func markWindowFrameQualification(w *WindowSpec) {
	if w.Frame == nil {
		return
	}
	frame := *w.Frame
	frame.Start.Offset, _ = markGroupedQualification(frame.Start.Offset)
	frame.End.Offset, _ = markGroupedQualification(frame.End.Offset)
	w.Frame = &frame
}
