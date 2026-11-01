package inquiry

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

func TransactionAge(h SessionHost, xid uint32) (int32, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	if h.TransactionRef().Epoch() != h.CatalogEpochLocked() {
		return 0, ids.ErrCatalogReplaced
	}
	return h.TransactionAgeLocked(xid)
}

func MultiTransactionAge(h SessionHost, xid uint32) (int32, error) {
	return h.LockManager().MultiAge(h.Ticket(), xid)
}

func CurrentTransactionID(h SessionHost, assign bool) (uint64, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	if h.TransactionRef().Epoch() != h.CatalogEpochLocked() {
		return 0, ids.ErrCatalogReplaced
	}
	if assign {
		if err := h.AssignTransactionXIDLocked(); err != nil {
			return 0, err
		}
	}
	return h.VisibilityLocked().FullXIDLocked(h.TransactionRef()), nil
}

func CurrentSnapshot(h SessionHost) (*sqltype.Snapshot, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	snapshot, err := h.CurrentSnapshotLocked()
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, sqlError("55000", "no active snapshot")
	}
	return snapshot, nil
}

func TransactionStatus(h SessionHost, full uint64) (string, bool, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	if h.TransactionRef().Epoch() != h.CatalogEpochLocked() {
		return "", false, ids.ErrCatalogReplaced
	}
	return h.VisibilityLocked().TransactionStatusLocked(full)
}

func TransactionMembers(h SessionHost, xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	members, err := h.LockManager().MultiXactMembers(h.CatalogDomain(), h.Ticket(), xid)
	if err != nil {
		return nil, err
	}
	// The manager's sole snapshot clone is released before SQL packing. No
	// engine mutex or manager mutex is held while constructing owned records.
	rows := make([]sqltype.Value, len(members))
	for i, m := range members {
		x, err := sqltype.XIDValue(result.CompositeMember(0).Usage.Type, m.XID)
		if err != nil {
			return nil, err
		}
		mode := sqltype.String(result.CompositeMember(1).Usage.Type, m.Mode.Name())
		rows[i], err = sqltype.OwnedCompositeValue(result, []sqltype.Value{x, mode})
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
func commitTimestampDisabled() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "could not get commit timestamp data", Hint: `Make sure the configuration parameter "track_commit_timestamp" is set.`}
}

func CommitTimestamp(h SessionHost, xid uint32) (time.Time, uint32, bool, error) {
	h.ReacquireEngine()
	registry := h.VisibilityLocked()
	if !registry.TracksCommitTimestamps() {
		h.ReleaseEngineLocked()
		return time.Time{}, 0, false, commitTimestampDisabled()
	}
	entry := registry.LookupCommitTimestampLocked(xid)
	h.ReleaseEngineLocked()
	return entry.Stamp, entry.Origin, entry.Present, nil
}

func LastCommittedTransaction(h SessionHost) (uint32, time.Time, uint32, bool, error) {
	h.ReacquireEngine()
	registry := h.VisibilityLocked()
	if !registry.TracksCommitTimestamps() {
		h.ReleaseEngineLocked()
		return 0, time.Time{}, 0, false, commitTimestampDisabled()
	}
	xid, entry := registry.LastCommittedXID(), registry.LastCommitTimestamp()
	h.ReleaseEngineLocked()
	return xid, entry.Stamp, entry.Origin, entry.Present, nil
}
