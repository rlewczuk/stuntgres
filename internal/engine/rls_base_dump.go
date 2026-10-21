package engine

import (
	"context"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

// Replay rows before these groups: an ordinary forced owner otherwise cannot
// insert exported data. Disabled+forced is a distinct nondefault state.
func rowSecurityDumpGroups(ctx context.Context, view catalog.Read, roles authorization.Read) ([]dump.Group, error) {
	var groups []dump.Group
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || !d.Security.RLSEnabled && !d.Security.RLSForced {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ns := view.MustNamespace(d.Namespace())
		if ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		table := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name())
		var sql strings.Builder
		if d.Security.RLSEnabled {
			sql.WriteString("ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY;\n")
		}
		if d.Security.RLSForced {
			sql.WriteString("ALTER TABLE " + table + " FORCE ROW LEVEL SECURITY;\n")
		}
		groups = append(groups, dump.Group{Owner: owner.Name, Table: table, SQL: sql.String()})
	}
	slices.SortFunc(groups, func(a, b dump.Group) int { return strings.Compare(a.Table, b.Table) })
	return groups, nil
}
