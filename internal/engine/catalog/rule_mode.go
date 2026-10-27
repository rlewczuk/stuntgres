package catalog

// RuleMode is the checked pg_rewrite.ev_enabled value. Zero is not a mode.
type RuleMode byte

const (
	RuleOrigin   RuleMode = 'O'
	RuleDisabled RuleMode = 'D'
	RuleReplica  RuleMode = 'R'
	RuleAlways   RuleMode = 'A'
)

func (m RuleMode) Valid() bool {
	return m == RuleOrigin || m == RuleDisabled || m == RuleReplica || m == RuleAlways
}

// Fires selects ordinary rules for an operation's effective replication role.
// SELECT view expansion is independent of ordinary-rule enable modes.
func (m RuleMode) Fires(replica bool) bool {
	return m == RuleAlways || m == RuleOrigin && !replica || m == RuleReplica && replica
}

func (m RuleMode) String() string {
	switch m {
	case RuleOrigin:
		return "O"
	case RuleDisabled:
		return "D"
	case RuleReplica:
		return "R"
	case RuleAlways:
		return "A"
	}
	return ""
}
