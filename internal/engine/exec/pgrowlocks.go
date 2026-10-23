package exec

import (
	"context"
	"fmt"
	"math"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PgrowlocksLogicalTID maps a logical row version to its (block, offset) tid.
func PgrowlocksLogicalTID(version ids.VersionID) (uint32, uint16, error) {
	if version == 0 || uint64(version) > uint64(math.MaxUint32)*65535 {
		return 0, 0, fmt.Errorf("stuntgres: logical row version exceeds tid range")
	}
	n := uint64(version) - 1
	return uint32(n / 65535), uint16(n%65535 + 1), nil
}

// CheckPgrowlocksTable checks that d is an ordinary table the current user may
// inspect: pg_stat_scan_tables membership or SELECT on the table.
func CheckPgrowlocksTable(view access.View, env access.Environment, ref ids.ObjectRef, d *catalog.Relation) error {
	if d.RelationKind == catalog.PartitionedRelation {
		return &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809",
			Message: fmt.Sprintf(`"%s" is a partitioned table`, d.Name()),
			Detail:  "Partitioned tables do not contain rows.",
		}
	}
	if d.RelationKind != catalog.TableRelation {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a table`, d.Name()))
	}
	// Every represented native ordinary storage relation uses heap. There is no
	// table-AM descriptor or public native CREATE ACCESS METHOD authority.
	inherited, err := view.Roles.InheritsRole(view.State.CurrentUser, view.Owner.Principals.Principal(authorization.StatScanTablesRoleID))
	if err != nil {
		return err
	}
	if inherited {
		return nil
	}
	return view.Check(env, access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege})
}

// PgrowlocksPrefix lists the visible row versions of ref for the lock manager.
func PgrowlocksPrefix(ctx context.Context, view *visibility.View, ref ids.ObjectRef) ([]lock.PgrowlocksTuple, error) {
	source, err := CaptureSource(ctx, view, ref)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	tuples := make([]lock.PgrowlocksTuple, source.Count())
	for i := range tuples {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		version := source.VersionAt(source.RawIndex(i))
		tuples[i] = lock.PgrowlocksTuple{
			Tuple:            lock.MultiTuple{Resource: lock.Resource{ObjectRef: ref, Row: version.Row}, Version: version.Version},
			PrivateInsertion: version.Born == 0,
		}
	}
	return tuples, nil
}

func pgrowlocksMode(mode lock.MultiMode) string {
	switch mode {
	case lock.MultiKeyShare:
		return "For Key Share"
	case lock.MultiShare:
		return "For Share"
	case lock.MultiNoKeyLock:
		return "For No Key Update"
	case lock.MultiUpdateLock:
		return "For Update"
	case lock.MultiNoKeyAction:
		return "No Key Update"
	case lock.MultiUpdateAction:
		return "Update"
	}
	return ""
}

// PgrowlocksValues builds the pgrowlocks result rows from the lock records.
func PgrowlocksValues(ctx context.Context, result *sqltype.Descriptor, records []lock.PgrowlocksRecord) ([]sqltype.Value, error) {
	rows := make([]sqltype.Value, len(records))
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block, offset, err := PgrowlocksLogicalTID(record.Tuple.Version)
		if err != nil {
			return nil, err
		}
		fields := make([]sqltype.Value, 6)
		fields[0], err = sqltype.TIDValue(result.CompositeMember(0).Usage.Type, block, offset)
		if err != nil {
			return nil, err
		}
		fields[1], err = sqltype.XIDValue(result.CompositeMember(1).Usage.Type, record.Locker)
		if err != nil {
			return nil, err
		}
		fields[2] = sqltype.Boolean(result.CompositeMember(2).Usage.Type, record.Multi)
		for j := 3; j < 6; j++ {
			array := result.CompositeMember(j).Usage.Type
			values := make([]sqltype.Value, len(record.Members))
			for k, member := range record.Members {
				switch j {
				case 3:
					values[k], err = sqltype.XIDValue(array.Element(), member.XID)
				case 4:
					values[k] = sqltype.String(array.Element(), pgrowlocksMode(member.Mode))
				case 5:
					values[k], err = sqltype.Integer(array.Element(), int64(member.PID))
				}
				if err != nil {
					return nil, err
				}
			}
			fields[j], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Length: int32(len(values)), Lower: 1}}, values)
			if err != nil {
				return nil, err
			}
		}
		rows[i], err = sqltype.OwnedCompositeValue(result, fields)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
