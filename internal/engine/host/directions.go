package host

// DefaultConversion is used only while resolving a declaration against genuine
// current catalog directions. Invocation receives captured immutable tokens.
func (r *Runtime) DefaultConversion(t Type, from bool) *Conversion {
	for _, d := range r.definition.Defaults {
		if d.Type == t {
			if from {
				return r.conversions[d.FromID]
			}
			return r.conversions[d.ToID]
		}
	}
	return nil
}
