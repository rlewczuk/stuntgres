package regobj

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

// DomainInvalidDumpEvents adds the post-data events of NOT VALID domain
// CHECKs. Invalid domain CHECKs are post-data events. Their providers do not constrain
// CREATE DOMAIN, which would invent cycles through a later table/derived type.
func DomainInvalidDumpEvents(ctx context.Context, r dump.Resolver, view catalog.Read, graph dump.Graph, domains map[ids.OID]*catalog.Type) error {
	var data []dump.Event
	for event := range graph {
		if event.Phase == dump.TableCompletion {
			data = append(data, event)
		}
	}
	for oid, domain := range domains {
		event := dump.Event{OID: oid, Phase: dump.DomainChecks}
		for _, check := range domain.DomainChecks {
			if !check.NotValid {
				continue
			}
			if _, exists := graph[event]; !exists {
				graph[event] = append([]dump.Event{{OID: oid}}, data...)
			}
			add := func(address ids.ObjectAddress) error {
				predecessor, err := r.Predecessor(view, address, ids.ObjectAddress{})
				if err != nil {
					return err
				}
				if predecessor != 0 {
					graph.Add(event, dump.Event{OID: predecessor})
				}
				return nil
			}
			if err := ClassReplayReferences(view, check.Expression, add); err != nil {
				return err
			}
			for _, dependency := range check.Dependencies {
				if err := add(dependency.Ref.Address); err != nil {
					return err
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
