package contrib

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgsurgeryAuthority[A Authority](auth A, n plan.InstalledPgsurgeryExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidatePgsurgeryIdentity(view.Catalog, current, n.Ref); err != nil {
		return err
	}
	call := n.Pgsurgery
	call.RoutineReference = current
	if err := extension.ValidatePgsurgeryCall(view.Catalog, current, call); err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}

func pgsurgeryTarget[A Authority](auth A, target *catalog.Relation) error {
	if err := pgvisibilityTargetCheckable(auth.Access().Catalog, target); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok {
			copy := *diagnostic
			copy.Message = fmt.Sprintf("cannot operate on relation %q", target.Name())
			return &copy
		}
		return err
	}
	if target.TableMethod != 0 && target.TableMethod != 2 {
		return sqlError("0A000", "only heap AM is supported")
	}
	return auth.Check(access.Requirement{Object: auth.Access().Ref(target.Address()), OwnerOnly: true, OwnerNoun: "table"})
}

type surgeryTID struct {
	block  uint32
	offset uint16
}

// Pgsurgery publishes physical headers outside the ordinary statement journal.
// Completed blocks survive subsequent statement failure or owner rollback.
func Pgsurgery[A Authority](h Host[A], n plan.InstalledPgsurgeryExpr, oid uint32, input sqltype.Value) error {
	if err := h.Active(); err != nil {
		return err
	}
	if input.IsNull() || input.Type() == nil || input.Type().BaseType().ID() != sqltype.ID(1010) {
		return fmt.Errorf("stuntgres: invalid surgery tid array")
	}
	for i := range input.ArrayLen() {
		if input.ArrayElement(i).IsNull() {
			return sqlError("22004", "array must not contain nulls")
		}
	}
	if input.ArrayRank() > 1 {
		return sqlError("22000", "argument must be empty or one-dimensional array")
	}
	tids := make([]surgeryTID, input.ArrayLen())
	for i := range tids {
		block, offset, err := input.ArrayElement(i).TID()
		if err != nil {
			return err
		}
		tids[i] = surgeryTID{block, offset}
	}
	slices.SortFunc(tids, func(a, b surgeryTID) int {
		if a.block < b.block {
			return -1
		}
		if a.block > b.block {
			return 1
		}
		return int(a.offset) - int(b.offset)
	})
	h.ReacquireEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgsurgeryAuthority(auth, n)
	}
	var target *catalog.Relation
	var store *storage.Relation
	if err == nil {
		target, _, err = amcheckTargetIdentity(auth.Access().Catalog, oid)
	}
	if err == nil {
		store = h.PrivateStorageLocked(target.Address())
	}
	epoch := h.CatalogEpochLocked()
	h.ReleaseEngineLocked()
	if err != nil {
		return err
	}
	ref := h.CatalogDomain().Ref(epoch, target.Address())
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.RowExclusive, &contribution); err != nil {
		return err
	}
	// Empty arrays still perform target, method and owner admission.
	first := true
	for at := 0; first || at < len(tids); {
		first = false
		if err := h.Operation().Context().Err(); err != nil {
			return err
		}
		end := at
		if at < len(tids) {
			for end < len(tids) && tids[end].block == tids[at].block {
				end++
			}
		}
		writes, deletes := h.ColumnProjectionRoots(target.Address())
		owners, err := h.LockHeapStatisticsCohort()
		if err != nil {
			return err
		}
		auth, err = h.RoutineAuthorizationLocked()
		if err == nil {
			err = pgsurgeryAuthority(auth, n)
		}
		if err == nil && (auth.Access().Catalog.Epoch() != epoch || auth.Access().Catalog.Get(target.Address()) != target || h.PrivateStorageLocked(target.Address()) != store) {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			h.MarkTemporaryTargetLocked(ref)
			err = pgsurgeryTarget(auth, target)
		}
		var notices []sqlstate.Error
		if err == nil && at < end {
			if store == nil {
				err = ids.ErrCatalogReplaced
			} else {
				facts := h.HeapStatisticsFactsLocked(owners, store, target.Address(), writes, deletes)
				rows := make(map[ids.VersionID]storage.RowVersion, min(len(facts), end-at, 65535))
				var maxBlock uint32
				var anySlot bool
				maxOffset := uint16(0)
				coordinate := func(version ids.VersionID) (uint32, uint16) {
					block, offset, failure := storage.LogicalTID(version)
					if failure != nil {
						err = failure
						return 0, 0
					}
					anySlot = true
					maxBlock = max(maxBlock, block)
					if block == tids[at].block {
						maxOffset = max(maxOffset, offset)
					}
					return block, offset
				}
				for _, fact := range facts {
					if block, _ := coordinate(fact.Version.Version); block == tids[at].block {
						rows[fact.Version.Version] = fact.Version
					}
				}
				if store.Headers != nil {
					for version := range store.Headers.Versions.All() {
						coordinate(version)
					}
				}
				notice := func(code, message string) {
					notices = append(notices, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: code, Message: message})
				}
				var selected []storage.RowVersion
				if !anySlot || tids[at].block > maxBlock {
					notice("22023", fmt.Sprintf("skipping block %d for relation %q because the block number is out of range", tids[at].block, target.Name()))
				} else {
					var last ids.VersionID
					for _, tid := range tids[at:end] {
						version, valid := storage.VersionFromTID(tid.block, tid.offset)
						if valid && version == last {
							continue
						}
						row, exists := rows[version]
						skip := func(reason string) {
							notice("00000", fmt.Sprintf("skipping tid (%d, %d) for relation %q because %s", tid.block, tid.offset, target.Name(), reason))
						}
						if !valid || tid.offset > maxOffset {
							skip("the item number is out of range")
							continue
						}
						header := store.Headers.Header(version)
						switch header.Slot {
						case storage.HeapSlotRedirect:
							_, offset, _ := storage.LogicalTID(header.Redirect)
							skip(fmt.Sprintf("it redirects to item %d", offset))
							continue
						case storage.HeapSlotDead:
							skip("it is marked dead")
							continue
						case storage.HeapSlotUnused:
							skip("it is marked unused")
							continue
						}
						if !exists {
							skip("the item number is out of range")
							continue
						}
						if version != last {
							selected = append(selected, row)
							last = version
						}
					}
				}
				if err == nil {
					err = h.Operation().Context().Err()
				}
				if err == nil && len(selected) != 0 {
					var next *storage.HeapHeaderRoot
					if store.Generation == ^uint64(0) {
						err = fmt.Errorf("stuntgres: heap generation exhausted")
					} else {
						next, err = storage.RepairHeapHeaders(store.Headers, selected)
					}
					if err == nil {
						store.Headers = next
						store.Generation++
					}
				}
			}
		}
		h.UnlockHeapStatisticsCohortLocked(owners)
		if err != nil {
			return err
		}
		for _, notice := range notices {
			h.Operation().EmitNotice(session.LevelNotice, notice)
		}
		at = end
	}
	return nil
}
