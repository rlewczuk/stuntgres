package visibility

// RetireLocked releases the drained incarnation's live/history backing without
// rewinding any identity frontier. Detached query sources own their own cuts.
func (reg *Registry) RetireLocked() {
	reg.FenceLocked()
	reg.Entries = nil
	reg.History = nil
	reg.children = nil
	reg.LowIDs = nil
	reg.CommitTimestamps = nil
	reg.Serializable.Nodes = nil
	reg.Serializable.Creators = nil
}
