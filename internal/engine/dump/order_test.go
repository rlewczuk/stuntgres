package dump

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestOrderPhaseScheduler(t *testing.T) {
	definition := Event{OID: 90}
	membership := Event{OID: 20, Phase: TableAttachment}
	data := Event{OID: 20, Phase: Rows}
	check := Event{OID: 20, Phase: InvalidConstraints}
	graph := Graph{definition: {membership}, membership: nil, data: {definition}, check: {data}}
	order, err := Order(context.Background(), graph, map[ids.OID]ids.OID{90: 1})
	if err != nil || !slices.Equal(order, []Event{membership, definition, data, check}) {
		t.Fatal("source priority bypassed a membership/name/data/origin predecessor", order, err)
	}
	graph.Add(membership, check)
	if _, err := Order(context.Background(), graph, nil); err == nil {
		t.Fatal("cyclic phase dependencies accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Order(ctx, graph, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled phase scheduler", err)
	}
}
