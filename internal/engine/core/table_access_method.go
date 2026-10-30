package core

// DefaultTableAccessMethod consumes command admission rather than taking a
// settings lock or recapturing authorization while Engine.Mu is held. Internal
// initialization operations without command admission use the boot setting.
func (h DDLHost) DefaultTableAccessMethod() string {
	if h.Op != nil && h.Op.TableAccessMethod != "" { return h.Op.TableAccessMethod }
	return "heap"
}

// DefaultTablespace reads the admitted operation image, including an empty
// default. Internal initialization has the same empty boot default.
func (h DDLHost) DefaultTablespace() string {
	if h.Op != nil {
		return h.Op.DefaultTablespace
	}
	return ""
}
