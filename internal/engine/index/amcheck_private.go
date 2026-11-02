package index

import (
	"context"

	"stuntgres/internal/engine/ids"
)

// Private successor and delete links are checked against the actual captured
// heap version authority, never an overlay ordinal or a freshly built root.
func (c *AmcheckCapture) checkPrivateAssociations(ctx context.Context) error {
	if len(c.Writes) == 0 && c.Deletes.Len() == 0 {
		return nil
	}
	baseline := make(map[ids.VersionID]ids.RowID, len(c.Store.Versions))
	versions := make(map[ids.VersionID]ids.RowID, len(c.Writes))
	for _, row := range c.Store.Versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.Row == 0 || row.Version == 0 {
			return c.corrupt("item order invariant violated")
		}
		if _, exists := baseline[row.Version]; exists {
			return c.corrupt("item order invariant violated")
		}
		baseline[row.Version] = row.Row
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
		if _, exists := baseline[row.Version]; exists {
			return c.corrupt("item order invariant violated")
		}
		if row.Base != 0 {
			base, _, exists := c.Store.PhysicalVersion(row.Base, -1)
			if !exists || base.Row != row.Row {
				return c.corrupt("item order invariant violated")
			}
		}
		versions[row.Version] = row.Row
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			if prior.Version.Born == 0 {
				versions[prior.Version.Version] = prior.Version.Row
			}
		}
	}
	if c.Deletes != nil {
		for row, deletion := range c.Deletes.All() {
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
			target, _, targetExists := c.Store.PhysicalVersion(deletion.Version, deletion.Ordinal)
			if !ownExists && (!targetExists || target.Row != row) {
				return c.corrupt("item order invariant violated")
			}
			base, _, baseExists := c.Store.PhysicalVersion(deletion.Base, -1)
			if deletion.Base != 0 && (!baseExists || base.Row != row) {
				return c.corrupt("item order invariant violated")
			}
		}
	}
	return nil
}
