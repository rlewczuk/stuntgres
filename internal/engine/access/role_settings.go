package access

import (
	"context"
	"iter"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// RoleSetting is canonical parameter spelling and its extracted persisted input.
type RoleSetting struct {
	Name  string
	Value string
}

// RoleSettingKey names a cluster scope. Zero members mean ALL.
type RoleSettingKey struct {
	Database ids.ObjectAddress
	Role     authorization.PrincipalRef
}

// RoleSettings owns immutable rows. Lookup and Rows lend read-only slices;
// WithRow takes a copy so callers retain no writable publication backing.
type RoleSettings struct {
	rows util.PersistentMap[RoleSettingKey, []RoleSetting]
}

func (s *RoleSettings) Lookup(key RoleSettingKey) []RoleSetting {
	if s == nil {
		return nil
	}
	row, _ := s.rows.Get(key)
	return row
}

func (s *RoleSettings) Rows() iter.Seq2[RoleSettingKey, []RoleSetting] {
	return func(yield func(RoleSettingKey, []RoleSetting) bool) {
		if s == nil {
			return
		}
		keys := make([]RoleSettingKey, 0, s.rows.Len())
		for key := range s.rows.All() {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, func(a, b RoleSettingKey) int {
			if a.Database.ClassID != b.Database.ClassID {
				if a.Database.ClassID < b.Database.ClassID {
					return -1
				}
				return 1
			}
			if a.Database.ObjectID != b.Database.ObjectID {
				if a.Database.ObjectID < b.Database.ObjectID {
					return -1
				}
				return 1
			}
			if a.Database.SubID != b.Database.SubID {
				if a.Database.SubID < b.Database.SubID {
					return -1
				}
				return 1
			}
			if a.Role.ID() < b.Role.ID() {
				return -1
			}
			if a.Role.ID() > b.Role.ID() {
				return 1
			}
			return 0
		})
		for _, key := range keys {
			if !yield(key, s.Lookup(key)) {
				return
			}
		}
	}
}

func (s *RoleSettings) WithRow(key RoleSettingKey, row []RoleSetting) *RoleSettings {
	if slices.Equal(s.Lookup(key), row) {
		return s
	}
	var next RoleSettings
	if s != nil {
		next = *s
	}
	if len(row) == 0 {
		next.rows = next.rows.Delete(key)
	} else {
		next.rows = next.rows.Set(key, slices.Clone(row))
	}
	return &next
}

func (s *RoleSettings) WithoutRoles(refs []authorization.PrincipalRef) *RoleSettings {
	next := s
	if s == nil || len(refs) == 0 {
		return next
	}
	for key := range s.rows.All() {
		if slices.Contains(refs, key.Role) {
			next = next.WithRow(key, nil)
		}
	}
	return next
}

func sameSettingRow(a, b []RoleSetting) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// MergeRoleSettings applies only candidate's changed exact scope predecessors.
// Concurrent disjoint rows survive; a changed predecessor is a conflict even
// when its contents have subsequently returned to the same values.
func MergeRoleSettings(ctx context.Context, base, candidate, current *RoleSettings) (*RoleSettings, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if candidate == base {
		return current, nil
	}
	if current == base {
		return candidate, nil
	}
	next := current
	merge := func(key RoleSettingKey, row []RoleSetting) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		before := base.Lookup(key)
		if sameSettingRow(before, row) {
			return nil
		}
		if !sameSettingRow(before, current.Lookup(key)) {
			return catalog.Conflict{}
		}
		// Rows are already owned immutable storage: share them without another copy.
		var result RoleSettings
		if next != nil {
			result = *next
		}
		if len(row) == 0 {
			result.rows = result.rows.Delete(key)
		} else {
			result.rows = result.rows.Set(key, row)
		}
		next = &result
		return nil
	}
	if candidate != nil {
		for key, row := range candidate.rows.All() {
			if err := merge(key, row); err != nil {
				return nil, err
			}
		}
	}
	if base != nil {
		for key := range base.rows.All() {
			if len(candidate.Lookup(key)) == 0 {
				if err := merge(key, nil); err != nil {
					return nil, err
				}
			}
		}
	}
	return next, nil
}
