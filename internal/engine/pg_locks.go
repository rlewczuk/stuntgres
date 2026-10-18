package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// Existing builtin view allocation, grounded by the pinned PostgreSQL inventory.
// Its row/array are 12075/12074; these are separate catalog object classes.
const pgLocksOID ids.OID = 12073

var pgLockModes = [...]struct {
	mode lock.Mode
	name string
}{
	{lock.AccessShare, "AccessShareLock"},
	{lock.RowShare, "RowShareLock"},
	{lock.RowExclusive, "RowExclusiveLock"},
	{lock.ShareUpdateExclusive, "ShareUpdateExclusiveLock"},
	{lock.Share, "ShareLock"},
	{lock.ShareRowExclusive, "ShareRowExclusiveLock"},
	{lock.Exclusive, "ExclusiveLock"},
	{lock.AccessExclusive, "AccessExclusiveLock"},
	{lock.TypeExclusive, "ExclusiveLock"},
	{lock.AdvisoryShare, "ShareLock"},
	{lock.AdvisoryExclusive, "ExclusiveLock"},
}

// virtualTransaction renders owner as a pg_locks virtualxid.
func virtualTransaction(owner lock.RecordOwner) string {
	return strconv.FormatUint(uint64(owner.Session), 10) + "/" + strconv.FormatUint(uint64(owner.Local), 10)
}

// Engine.mu is held. The retained node's exact ref supplies original backend
// association even after commit; no session lookup or live ticket is needed.
func (e *Engine) capturePGPredicateLocksLocked() ([]lock.Record, error) {
	var records []lock.Record
	for ref, node := range e.serializable.nodes {
		if ref.Epoch() != e.catalog.state.Epoch() || node.ref != ref || !serializableRelevant(node) {
			continue
		}
		owner, visible, err := lock.PublicOwner(e.catalog.Domain, ref, node.prepared)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		for address := range node.reads {
			records = append(records, lock.Record{Kind: lock.PredicateRecord, Owner: owner, Object: uint32(address.ObjectID)})
		}
	}
	return records, nil
}

// Consumes Engine.mu. Only scalar SSI/database facts are captured before the
// manager is entered. Captured sources own values; prepared handles own recipes.
func (e *Engine) capturePGLocksLocked(ctx context.Context, ref ids.ObjectRef, d *relationDescriptor) (*querySource, error) {
	database, ok := e.catalog.state.Database(catalog.CurrentDatabaseAddress)
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	databaseOID, epoch := uint32(database.Address().ObjectID), e.catalog.state.Epoch()
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
	current := e.catalog.state.Epoch() == epoch
	e.mu.Unlock()
	if !current {
		return nil, ids.ErrCatalogReplaced
	}
	rows, err := pgLockRows(ctx, d, databaseOID, records, predicates)
	if err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

func pgLockRows(ctx context.Context, d *relationDescriptor, database uint32, ordinary, predicates []lock.Record) ([][]sqltype.Value, error) {
	count := len(predicates)
	for _, record := range ordinary {
		if record.Kind == lock.VirtualRecord || record.Kind == lock.TransactionRecord {
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
	for _, records := range [2][]lock.Record{ordinary, predicates} {
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ownerText := virtualTransaction(record.Owner)
			switch record.Kind {
			case lock.VirtualRecord, lock.TransactionRecord:
				mode := "ExclusiveLock"
				if record.Waiting {
					mode = "ShareLock"
				}
				row, err := pgLockRow(d, database, record, mode, ownerText, values[len(rows)*16:(len(rows)+1)*16:(len(rows)+1)*16])
				if err != nil {
					return nil, err
				}
				rows = append(rows, row)
			case lock.PredicateRecord:
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

func pgLockRow(d *relationDescriptor, database uint32, record lock.Record, mode, ownerText string, row []sqltype.Value) ([]sqltype.Value, error) {
	for i := range row {
		row[i] = sqltype.Null(d.Columns[i].Usage.Type)
	}
	var failure error
	checked := func(v sqltype.Value, err error) sqltype.Value {
		if failure == nil {
			failure = err
		}
		return v
	}
	oid := func(i int, n uint32) { row[i] = checked(sqltype.OIDValue(d.Columns[i].Usage.Type, n)) }
	integer := func(i int, n int64) { row[i] = checked(sqltype.Integer(d.Columns[i].Usage.Type, n)) }
	text := func(i int, value string) { row[i] = sqltype.String(d.Columns[i].Usage.Type, value) }
	kind := ""
	switch record.Kind {
	case lock.RelationRecord, lock.PredicateRecord:
		kind = "relation"
		oid(1, database)
		oid(2, record.Object)
	case lock.ObjectRecord, lock.AdvisoryRecord:
		kind = "object"
		if record.Kind == lock.AdvisoryRecord {
			kind = "advisory"
		}
		oid(1, database)
		oid(7, record.Class)
		oid(8, record.Object)
		integer(9, int64(record.Subobject))
	case lock.VirtualRecord:
		kind = "virtualxid"
		targetText := ownerText
		if record.Target != record.Owner {
			targetText = virtualTransaction(record.Target)
		}
		text(5, targetText)
	case lock.TransactionRecord:
		kind = "transactionid"
		row[6] = checked(sqltype.XIDValue(d.Columns[6].Usage.Type, record.XID))
	default:
		return nil, fmt.Errorf("stuntgres: unknown public lock kind")
	}
	text(0, kind)
	text(10, ownerText)
	if !record.Owner.Prepared {
		integer(11, int64(record.Owner.Session))
	}
	text(12, mode)
	row[13] = sqltype.Boolean(d.Columns[13].Usage.Type, !record.Waiting)
	row[14] = sqltype.Boolean(d.Columns[14].Usage.Type, false)
	if record.Waiting {
		row[15] = checked(sqltype.TimestampFromTime(d.Columns[15].Usage.Type, record.WaitStart.UTC()))
	}
	return row, failure
}
