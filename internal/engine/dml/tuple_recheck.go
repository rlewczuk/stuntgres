package dml

import (
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// ErrTupleRecheck is an internal, unaccepted-candidate control outcome. It is
// consumed by the producing executor's EPQ boundary, never exposed as a SQL
// error and never confused with index/catalog binding revalidation.
var ErrTupleRecheck = errors.New("stuntgres: tuple producer recheck required")

// Trigger prelocking uses the key-column assignment mode, before callbacks can
// change NEW. Action classification still uses the final actual key change.
func updatePrelockMode(h Host, ref ids.ObjectRef, targets, layout []int) (lock.Mode, error) {
	unique, err := BindUniqueWrite(h, ref)
	if err != nil {
		return 0, err
	}
	for _, target := range targets {
		if layout != nil {
			target = layout[target]
		}
		for _, key := range unique.KeySlots {
			if target == key {
				return lock.ForUpdate, nil
			}
		}
	}
	return lock.ForNoKeyUpdate, nil
}
