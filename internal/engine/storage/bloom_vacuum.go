package storage

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Vacuum does not re-evaluate SQL or hash unchanged keys. Immutable signatures
// move with the exact version/position membership they describe.
func remapVacuumBloom(ctx context.Context, root *IndexRoot, old []RowVersion, c VacuumCandidate, store *Relation, generation uint64) (*IndexRoot, error) {
	bloom := *root.Bloom
	if len(bloom.Entries) != len(root.Entries) {
		return nil, fmt.Errorf("stuntgres: invalid vacuum bloom membership")
	}
	if bloom.Prefix < 0 || bloom.Prefix > len(old) {
		return nil, fmt.Errorf("stuntgres: invalid vacuum bloom prefix")
	}
	next := *root
	bloom.Storage, bloom.Generation = store, generation
	bloom.Ends = c.Ends
	next.Bloom = &bloom
	if c.Positions == nil {
		return &next, ctx.Err()
	}
	bloom.Prefix = 0
	for _, position := range c.Positions[:root.Bloom.Prefix] {
		if position >= 0 {
			bloom.Prefix++
		}
	}
	next.Entries = make([]IndexEntry, 0, len(root.Entries))
	bloom.Entries = make([]BloomEntry, 0, len(root.Entries))
	for i, entry := range root.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Position < 0 || entry.Position >= len(old) || old[entry.Position].Row != entry.Row || old[entry.Position].Version != entry.Version {
			return nil, fmt.Errorf("stuntgres: invalid vacuum bloom position")
		}
		position := c.Positions[entry.Position]
		if position < 0 {
			continue
		}
		entry.Position = position
		next.Entries = append(next.Entries, entry)
		bloom.Entries = append(bloom.Entries, root.Bloom.Entries[i])
	}
	if root.Membership != nil {
		next.Membership = make(map[ids.RowID]ids.VersionID, len(next.Entries))
		next.MembershipVersions = nil
		for _, entry := range next.Entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			next.SetMember(entry.Row, entry.Version)
		}
	}
	return &next, ctx.Err()
}
