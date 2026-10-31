package sqltype

// Selection returns the logical operand and equality selection retained by a
// grouping. It exposes no hash state, descriptor generation or execution data.
func (g Grouping) Selection() (Usage, Comparison) { return g.usage, g.comparison }

// Usages identifies a retained coercion's logical input and output. The
// expression owner retains the conversion itself; callers must not rebind it.
func (p CoercionPlan) Usages() (Usage, Usage) { return p.source, p.target }

// Selection identifies the released RANGE support chosen during analysis.
// Zero IDs describe an unbounded/no-support frame, not a fabricated declaration.
func (p RangeSupport) Selection() (ID, ID, uint32) {
	var key, offset ID
	if p.key != nil {
		key = p.key.ID()
	}
	if p.offset != nil {
		offset = p.offset.ID()
	}
	return key, offset, p.class
}
