package control

import (
	"context"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

// Registered installations replay before all ordinary declarations. Keep their
// prerequisite events in the ordinary graph as identity barriers, not members.
func (s *dumpScript) orderExtensionTypes(ctx context.Context) error {
	extensions := make(dump.Graph)
	for event, predecessors := range s.Graph {
		if _, ok := s.CatalogView.Extension(ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: event.OID}); ok {
			extensions[event] = predecessors
		}
	}
	if len(extensions) == 0 {
		return nil
	}
	order, err := dump.Order(ctx, extensions, nil)
	if err != nil {
		return err
	}
	for i, event := range order {
		if i > 0 {
			s.Graph[event] = append(s.Graph[event], order[i-1])
		}
	}
	last := order[len(order)-1]
	for event := range s.Graph {
		if _, ok := extensions[event]; !ok {
			s.Graph[event] = append(s.Graph[event], last)
		}
	}
	return nil
}
