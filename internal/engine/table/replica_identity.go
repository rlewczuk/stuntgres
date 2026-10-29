package table

import "stuntgres/internal/parser"

// ReplicaIdentityOnly admits root metadata without hierarchy or heap work.
func ReplicaIdentityOnly(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if action.Action != parser.SetReplicaIdentity {
			return false
		}
	}
	return true
}
