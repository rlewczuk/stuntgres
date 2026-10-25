package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) validateTemporaryCollationDump(ctx context.Context, view catalog.Read) error {
	var roots []ids.ObjectAddress
	for a, d := range view.Descriptors() {
		if _, ok := d.(*catalog.Collation); !ok {
			continue
		}
		if ns, ok := view.Namespace(d.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
			roots = append(roots, a)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	edges, err := DomainDependencyClosure(ctx, stage, roots)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		d := view.Get(ids.ObjectAddress{ClassID: edge.Address.ClassID, ObjectID: edge.Address.ObjectID})
		if edge.Relation != nil {
			d = edge.Relation
		}
		if d == nil {
			return fmt.Errorf("stuntgres: missing temporary collation dependent")
		}
		ns, ok := view.Namespace(d.Namespace())
		if !ok || ns.Temporary == (ids.SessionToken{}) {
			return fmt.Errorf("stuntgres: dump cannot serialize persistent temporary collation dependency")
		}
	}
	return nil
}
