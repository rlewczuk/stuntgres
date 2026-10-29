package control

import (
	"fmt"
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Rebuild before replacing: cycles and swaps must not overwrite source keys.
func remapInitialPrivilegeAddresses(state *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	rekey := func(records iter.Seq2[ids.ObjectAddress, *catalog.InitialPrivilegeRecord], count int, live bool) (map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord, error) {
		if count == 0 {
			return nil, nil
		}
		next := make(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord, count)
		for address, record := range records {
			object := address
			object.SubID = 0
			if target, ok := mapping[object]; ok {
				if target.ClassID != object.ClassID || target.SubID != 0 {
					return nil, fmt.Errorf("stuntgres: invalid initial privilege address mapping")
				}
				target.SubID = address.SubID
				address = target
			}
			// A baseline address may deliberately have no live object: that
			// absence is precisely why complete metadata must be emitted.
			if next[address] != nil || live && !initialPrivilegeTarget(state, address) {
				return nil, fmt.Errorf("stuntgres: invalid or duplicate initial privilege destination")
			}
			next[address] = record
		}
		return next, nil
	}
	next, err := rekey(state.InitialPrivileges(), state.InitialPrivilegeCount(), true)
	if err != nil {
		return err
	}
	baseline, err := rekey(state.InitialPrivilegeBaseline(), state.InitialPrivilegeBaselineCount(), false)
	if err != nil {
		return err
	}
	state.SetInitialPrivileges(next)
	state.SetInitialPrivilegeBaseline(baseline)
	return nil
}

func initialPrivilegeTarget(state *catalog.State, address ids.ObjectAddress) bool {
	object := address
	object.SubID = 0
	d := state.Get(object)
	if d == nil || address.SubID < 0 {
		return false
	}
	if address.SubID == 0 {
		return true
	}
	r, ok := d.(*catalog.Relation)
	if !ok {
		return false
	}
	for _, sub := range r.Attributes {
		if sub == address.SubID {
			return true
		}
	}
	return false
}

// RetainTemporaryInitialPrivileges overlays destination-owned history only
// after imported addresses have moved out of the retained TEMP address space.
func RetainTemporaryInitialPrivileges(current catalog.Read, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor) error {
	if len(temporary) == 0 {
		return nil
	}
	records := nextInitialPrivileges(next)
	for address, record := range current.State().InitialPrivileges() {
		object := address
		object.SubID = 0
		if retained := temporary[object]; retained != nil {
			if next.Get(object) != retained || records[address] != nil || !initialPrivilegeTarget(next, address) {
				return fmt.Errorf("stuntgres: conflicting retained temporary initial privileges")
			}
			records[address] = record
		}
	}
	next.SetInitialPrivileges(records)
	return nil
}

func nextInitialPrivileges(state *catalog.State) map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord {
	records := make(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord, state.InitialPrivilegeCount())
	for address, record := range state.InitialPrivileges() {
		records[address] = record
	}
	return records
}
