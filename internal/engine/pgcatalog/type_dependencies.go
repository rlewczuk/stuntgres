package pgcatalog

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// VisitTypeProcedureDependencies visits the distinct NORMAL I/O and support
// procedure references in authentic current type facts. Like PostgreSQL 18's
// GenerateTypeDependencies, this includes dependent types' own facts, not their
// element's codec references. The caller applies pinned-object filtering.
// Returning false stops visitation without allocating a dependency collection.
func VisitTypeProcedureDependencies(ctx context.Context, view catalog.Read, d *catalog.Type, visit func(ids.ObjectAddress) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if view.State() == nil || view.Owner() == nil || d == nil || d.SQLType == nil {
		return fmt.Errorf("stuntgres: missing type dependency authority")
	}
	current, ok := view.Type(d.Address())
	if !ok || current.SQLType != d.SQLType {
		return fmt.Errorf("stuntgres: foreign type dependency authority")
	}
	facts, err := TypeFacts(ctx, view, d, sqltype.OutputEnvironment{})
	if err != nil {
		return fmt.Errorf("stuntgres: type procedure dependencies for %s (%d): %w", d.SQLType.Name(), d.Address().ObjectID, err)
	}
	procedures := [...]uint32{facts.Input, facts.Output, facts.Receive, facts.Send, facts.ModIn, facts.ModOut, facts.Analyze, facts.Subscript}
	for i, oid := range procedures {
		if err := ctx.Err(); err != nil {
			return err
		}
		if oid == 0 {
			continue
		}
		duplicate := false
		for _, previous := range procedures[:i] {
			if previous == oid {
				duplicate = true
				break
			}
		}
		if !duplicate && !visit(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(oid)}) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}
