package engine

import (
	"context"
	"slices"
	"strings"
)

// Replay rows before these groups: an ordinary forced owner otherwise cannot
// insert exported data. Disabled+forced is a distinct nondefault state.
func rowSecurityDumpGroups(ctx context.Context, view CatalogRead, roles RoleRead) ([]dumpPolicyGroup, error) {
	var groups []dumpPolicyGroup
	for _, descriptor := range view.state.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok || !d.rlsEnabled && !d.rlsForced {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ns := view.state.descriptors[d.namespace].(*namespaceDescriptor)
		if ns.temporary != (SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(d.owner)
		if err != nil {
			return nil, err
		}
		table := quoteIdentifier(ns.name) + "." + quoteIdentifier(d.name)
		var sql strings.Builder
		if d.rlsEnabled {
			sql.WriteString("ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY;\n")
		}
		if d.rlsForced {
			sql.WriteString("ALTER TABLE " + table + " FORCE ROW LEVEL SECURITY;\n")
		}
		groups = append(groups, dumpPolicyGroup{owner: owner.Name, table: table, sql: sql.String()})
	}
	slices.SortFunc(groups, func(a, b dumpPolicyGroup) int { return strings.Compare(a.table, b.table) })
	return groups, nil
}
