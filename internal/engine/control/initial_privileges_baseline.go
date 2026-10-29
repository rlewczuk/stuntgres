package control

import (
	"slices"

	"stuntgres/internal/engine/catalog"
)

// InitialPrivilegesMatchBaseline compares only dump-visible history with the
// immutable engine-local seed. Current ACLs and descriptor names are deliberately
// irrelevant. The baseline is a comparison recipe, not additional live history.
func InitialPrivilegesMatchBaseline(view catalog.Read) bool {
	state := view.State()
	for address, baseline := range state.InitialPrivilegeBaseline() {
		current := state.InitialPrivilege(address)
		if current == nil || baseline == nil || current.PrivType != baseline.PrivType || current.ACL.Explicit != baseline.ACL.Explicit || !slices.Equal(current.ACL.Entries, baseline.ACL.Entries) {
			return false
		}
		object := address
		object.SubID = 0
		if d := view.Get(object); d == nil || temporaryDumpObject(state, d) {
			return false
		}
	}
	visible := 0
	for address := range state.InitialPrivileges() {
		object := address
		object.SubID = 0
		d := view.Get(object)
		if d == nil {
			return false
		}
		if !temporaryDumpObject(state, d) {
			visible++
		}
	}
	return visible == state.InitialPrivilegeBaselineCount()
}
