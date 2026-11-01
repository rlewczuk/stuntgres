package stored

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Constraint CREATE initially makes conname equal tgname, including all derived
// clones. Construct each family under a fresh name, then use ordinary SQL to
// restore tgname and each independent conname. This also handles name swaps and
// a final tgname that is already occupied by an unrelated table constraint.
func triggerReplayNames(ctx context.Context, read catalog.Read) (map[ids.OID]string, error) {
	type root struct {
		oid         ids.OID
		table, name string
	}
	var roots []root
	for _, relation := range read.Relations() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if read.MustNamespace(relation.Namespace()).Temporary != (ids.SessionToken{}) {
			continue
		}
		for _, t := range TriggersOf(relation) {
			if p := t.Parts(); p.Constraint.OID != 0 && p.Parent.ObjectID == 0 {
				table, err := ruleRelationSQL(read, p.Relation)
				if err != nil {
					return nil, err
				}
				roots = append(roots, root{p.OID, table, p.Name})
			}
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	slices.SortFunc(roots, func(a, b root) int {
		if a.table != b.table {
			return strings.Compare(a.table, b.table)
		}
		return strings.Compare(a.name, b.name)
	})
	used := make(map[string]bool)
	namespaces := make(map[ids.ObjectAddress]bool)
	parts := make(map[ids.OID]TriggerParts)
	for _, relation := range read.Relations() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !namespaces[relation.Namespace()] {
			namespaces[relation.Namespace()] = true
			for name := range catalog.ConstraintNames(read, relation.Namespace(), nil) {
				used[name] = true
			}
		}
		for _, t := range TriggersOf(relation) {
			p := t.Parts()
			if p.Constraint.OID != 0 {
				parts[p.OID] = p
			}
			used[p.Name] = true
		}
	}
	names := make(map[ids.OID]string, len(parts))
	for _, r := range roots {
		name := catalog.GeneratedConstraintName("stuntgres", "", "trigger_restore", used)
		used[name], names[r.oid] = true, name
	}
	var family func(ids.OID) (string, error)
	visiting := make(map[ids.OID]bool)
	family = func(oid ids.OID) (string, error) {
		if name := names[oid]; name != "" {
			return name, nil
		}
		p, ok := parts[oid]
		if !ok || p.Constraint.OID == 0 || p.Parent.ObjectID == 0 || visiting[oid] {
			return "", fmt.Errorf("stuntgres: invalid constraint trigger replay lineage")
		}
		visiting[oid] = true
		name, err := family(p.Parent.ObjectID)
		delete(visiting, oid)
		if err != nil {
			return "", err
		}
		names[oid] = name
		return name, nil
	}
	for oid, p := range parts {
		if p.Constraint.OID != 0 {
			if ns := read.MustNamespace(read.MustRelation(p.Relation.Address).Namespace()); ns.Temporary != (ids.SessionToken{}) {
				continue
			}
			if _, err := family(oid); err != nil {
				return nil, err
			}
		}
	}
	return names, nil
}
