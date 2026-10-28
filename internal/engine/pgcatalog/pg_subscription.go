package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const SubscriptionOID ids.OID = 6100

func capturePGSubscription(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: SubscriptionOID}
	if in.Context == nil || in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: invalid subscription source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, SubscriptionOID); err != nil {
		return nil, err
	}
	// Execute's used-column manifest owns admission, including hidden and
	// whole-row references. Checking every column here would deny PUBLIC reads.
	// Subscription commands own population; catalog reads never contact a publisher.
	rows := make([][]sqltype.Value, 0)
	for _, d := range in.Catalog.Subscriptions() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.SubscriptionProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(in.Roles, d); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 18)
		for _, field := range []struct {
			slot int
			oid  uint32
		}{{0, uint32(d.Address().ObjectID)}, {1, uint32(d.Database.ObjectID)}, {4, uint32(d.Owner.ID())}} {
			var err error
			row[field.slot], err = sqltype.OIDValue(in.Source.Columns[field.slot].Usage.Type, field.oid)
			if err != nil {
				return nil, err
			}
		}
		var err error
		row[2], err = sqltype.PGLSNValue(in.Source.Columns[2].Usage.Type, d.SkipLSN)
		if err != nil {
			return nil, err
		}
		for _, field := range []struct {
			slot  int
			value string
		}{{3, d.Name()}, {7, string(d.Streaming)}, {8, string(d.TwoPhase)}, {13, d.Connection}, {15, d.SynchronousCommit}, {17, d.Origin}} {
			row[field.slot] = sqltype.String(in.Source.Columns[field.slot].Usage.Type, field.value)
		}
		for _, field := range []struct {
			slot  int
			value bool
		}{{5, d.Enabled}, {6, d.Binary}, {9, d.DisableOnError}, {10, d.PasswordRequired}, {11, d.RunAsOwner}, {12, d.Failover}} {
			row[field.slot] = sqltype.Boolean(in.Source.Columns[field.slot].Usage.Type, field.value)
		}
		row[14] = sqltype.Null(in.Source.Columns[14].Usage.Type)
		if d.HasSlot {
			row[14] = sqltype.String(in.Source.Columns[14].Usage.Type, d.SlotName)
		}
		array := in.Source.Columns[16].Usage.Type
		names := d.Publications()
		values := make([]sqltype.Value, len(names))
		for i, name := range names {
			values[i] = sqltype.String(array.Element(), name)
		}
		row[16], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Length: int32(len(values)), Lower: 1}}, values)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
