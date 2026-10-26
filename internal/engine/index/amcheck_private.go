package index

import (
	"context"

	"stuntgres/internal/engine/ids"
)

// Private successor and delete links are checked against the actual captured
// heap version authority, never an overlay ordinal or a freshly built root.
func (c *AmcheckCapture) checkPrivateAssociations(ctx context.Context) error {
	if len(c.Writes) == 0 && (c.Deletes == nil || len(c.Deletes.Rows) == 0) {
		return nil
	}
	baseline := make(map[ids.RowID]ids.VersionID, len(c.Store.Versions))
	versions := make(map[ids.VersionID]ids.RowID, len(c.Writes))
	for _, row := range c.Store.Versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.Row == 0 || row.Version == 0 {
			return c.corrupt("item order invariant violated")
		}
		if _, exists := baseline[row.Row]; exists {
			return c.corrupt("item order invariant violated")
		}
		baseline[row.Row] = row.Version
	}
	for _, row := range c.Writes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.Row == 0 || row.Version == 0 || row.Born != 0 {
			return c.corrupt("item order invariant violated")
		}
		if _, exists := versions[row.Version]; exists {
			return c.corrupt("item order invariant violated")
		}
		if base, exists := baseline[row.Row]; row.Base != 0 && (!exists || base != row.Base) || exists && row.Version == base {
			return c.corrupt("item order invariant violated")
		}
		versions[row.Version] = row.Row
	}
	if c.Deletes != nil {
		for row, deletion := range c.Deletes.Rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if deletion.Version == 0 || deletion.Row != row || deletion.Ref != c.ParentRef || deletion.Storage == nil || deletion.Storage.IndexIdentity() != c.Store.IndexSource {
				return c.corrupt("item order invariant violated")
			}
			own, ownExists := versions[deletion.Version]
			if ownExists && own != row {
				return c.corrupt("item order invariant violated")
			}
			base, baseExists := baseline[row]
			if !ownExists && (!baseExists || base != deletion.Version) {
				return c.corrupt("item order invariant violated")
			}
			if deletion.Base != 0 && (!baseExists || base != deletion.Base) {
				return c.corrupt("item order invariant violated")
			}
		}
	}
	return nil
}
