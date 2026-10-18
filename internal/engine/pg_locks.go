package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Existing builtin view allocation, grounded by the pinned PostgreSQL inventory.
// Its row/array are 12075/12074; these are separate catalog object classes.
const pgLocksOID ids.OID = 12073

var pgLockModes = [...]struct {
	mode relationLockMode
	name string
}{
	{accessShare, "AccessShareLock"},
	{relationRowShare, "RowShareLock"},
	{rowExclusive, "RowExclusiveLock"},
	{shareUpdateExclusive, "ShareUpdateExclusiveLock"},
	{relationShare, "ShareLock"},
	{shareRowExclusive, "ShareRowExclusiveLock"},
	{relationExclusive, "ExclusiveLock"},
	{accessExclusive, "AccessExclusiveLock"},
	{typeExclusive, "ExclusiveLock"},
	{advisoryShared, "ShareLock"},
	{advisoryExclusive, "ExclusiveLock"},
}

func (owner pgLockOwner) virtualTransaction() string {
	return strconv.FormatUint(uint64(owner.Session), 10) + "/" + strconv.FormatUint(uint64(owner.Local), 10)
}

// Engine.mu is held. The retained node's exact ref supplies original backend
// association even after commit; no session lookup or live ticket is needed.
func (e *Engine) capturePGPredicateLocksLocked() ([]pgLockRecord, error) {
	var records []pgLockRecord
	for ref, node := range e.serializable.nodes {
		if ref.Epoch() != e.state.epoch || node.ref != ref || !serializableRelevant(node) {
			continue
		}
		owner, visible, err := publicLockOwner(e.domain, ref, node.prepared)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		for address := range node.reads {
			records = append(records, pgLockRecord{Kind: pgPredicateLock, Owner: owner, Object: uint32(address.ObjectID)})
		}
	}
	return records, nil
}

// Consumes Engine.mu. Only scalar SSI/database facts are captured before the
// manager is entered. Captured sources own values; prepared handles own recipes.
func (e *Engine) capturePGLocksLocked(ctx context.Context, ref ids.ObjectRef, d *relationDescriptor) (*querySource, error) {
	database, ok := e.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	databaseOID, epoch := uint32(database.address.ObjectID), e.state.epoch
	predicates, err := e.capturePGPredicateLocksLocked()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	records, err := e.locks.PGLocks(epoch)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	current := e.state.epoch == epoch
	e.mu.Unlock()
	if !current {
		return nil, ErrCatalogReplaced
	}
	rows, err := pgLockRows(ctx, d, databaseOID, records, predicates)
	if err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

func pgLockRows(ctx context.Context, d *relationDescriptor, database uint32, ordinary, predicates []pgLockRecord) ([][]sqltype.Value, error) {
	count := len(predicates)
	for _, record := range ordinary {
		if record.Kind == pgVirtualLock || record.Kind == pgTransactionLock {
			count++
			continue
		}
		for _, mode := range pgLockModes {
			if record.Mode&mode.mode != 0 {
				count++
			}
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	values := make([]sqltype.Value, count*16)
	for _, records := range [2][]pgLockRecord{ordinary, predicates} {
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ownerText := record.Owner.virtualTransaction()
			switch record.Kind {
			case pgVirtualLock, pgTransactionLock:
				mode := "ExclusiveLock"
				if record.Waiting {
					mode = "ShareLock"
				}
				row, err := pgLockRow(d, database, record, mode, ownerText, values[len(rows)*16:(len(rows)+1)*16:(len(rows)+1)*16])
				if err != nil {
					return nil, err
				}
				rows = append(rows, row)
			case pgPredicateLock:
				row, err := pgLockRow(d, database, record, "SIReadLock", ownerText, values[len(rows)*16:(len(rows)+1)*16:(len(rows)+1)*16])
				if err != nil {
					return nil, err
				}
				rows = append(rows, row)
			default:
				for _, mode := range pgLockModes {
					if record.Mode&mode.mode == 0 {
						continue
					}
					row, err := pgLockRow(d, database, record, mode.name, ownerText, values[len(rows)*16:(len(rows)+1)*16:(len(rows)+1)*16])
					if err != nil {
						return nil, err
					}
					rows = append(rows, row)
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func pgLockRow(d *relationDescriptor, database uint32, record pgLockRecord, mode, ownerText string, row []sqltype.Value) ([]sqltype.Value, error) {
	for i := range row {
		row[i] = sqltype.Null(d.columns[i].Usage.Type)
	}
	var failure error
	checked := func(v sqltype.Value, err error) sqltype.Value {
		if failure == nil {
			failure = err
		}
		return v
	}
	oid := func(i int, n uint32) { row[i] = checked(sqltype.OIDValue(d.columns[i].Usage.Type, n)) }
	integer := func(i int, n int64) { row[i] = checked(sqltype.Integer(d.columns[i].Usage.Type, n)) }
	text := func(i int, value string) { row[i] = sqltype.String(d.columns[i].Usage.Type, value) }
	kind := ""
	switch record.Kind {
	case pgRelationLock, pgPredicateLock:
		kind = "relation"
		oid(1, database)
		oid(2, record.Object)
	case pgObjectLock, pgAdvisoryLock:
		kind = "object"
		if record.Kind == pgAdvisoryLock {
			kind = "advisory"
		}
		oid(1, database)
		oid(7, record.Class)
		oid(8, record.Object)
		integer(9, int64(record.Subobject))
	case pgVirtualLock:
		kind = "virtualxid"
		targetText := ownerText
		if record.Target != record.Owner {
			targetText = record.Target.virtualTransaction()
		}
		text(5, targetText)
	case pgTransactionLock:
		kind = "transactionid"
		row[6] = checked(sqltype.XIDValue(d.columns[6].Usage.Type, record.XID))
	default:
		return nil, fmt.Errorf("stuntgres: unknown public lock kind")
	}
	text(0, kind)
	text(10, ownerText)
	if !record.Owner.Prepared {
		integer(11, int64(record.Owner.Session))
	}
	text(12, mode)
	row[13] = sqltype.Boolean(d.columns[13].Usage.Type, !record.Waiting)
	row[14] = sqltype.Boolean(d.columns[14].Usage.Type, false)
	if record.Waiting {
		row[15] = checked(sqltype.TimestampFromTime(d.columns[15].Usage.Type, record.WaitStart.UTC()))
	}
	return row, failure
}
