package engine

import (
	"context"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// Replay rows before these groups: an ordinary forced owner otherwise cannot
// insert exported data. Disabled+forced is a distinct nondefault state.
func rowSecurityDumpGroups(ctx context.Context, view CatalogRead, roles authorization.Read) ([]dumpPolicyGroup, error) {
	var groups []dumpPolicyGroup
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*relationDescriptor)
		if !ok || !d.RLSEnabled && !d.RLSForced {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ns := view.MustNamespace(d.namespace)
		if ns.temporary != (ids.SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		table := quoteIdentifier(ns.name) + "." + quoteIdentifier(d.name)
		var sql strings.Builder
		if d.RLSEnabled {
			sql.WriteString("ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY;\n")
		}
		if d.RLSForced {
			sql.WriteString("ALTER TABLE " + table + " FORCE ROW LEVEL SECURITY;\n")
		}
		groups = append(groups, dumpPolicyGroup{owner: owner.Name, table: table, sql: sql.String()})
	}
	slices.SortFunc(groups, func(a, b dumpPolicyGroup) int { return strings.Compare(a.table, b.table) })
	return groups, nil
}
