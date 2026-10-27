package dml

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

// CompleteCTEMutation records the finished data-modifying CTE definition in
// its owner invocation o: count affected rows and, for RETURNING, the typed
// output rows, whose buffer the producer takes over without copying. A pending
// invocation-owned modifier adopts that buffer into its existing source so
// already-open readers retain source identity. It closes output on every error
// and rejects duplicate completion or a mismatched RETURNING shape.
func CompleteCTEMutation(o *exec.CTEInvocation, definition *plan.CTEDefinition, count int64, output exec.RowResult) error {
	producer := o.Producers[definition]
	if o.Closed || producer != nil && !producer.MutationPending {
		if output != nil {
			output.Close()
		}
		return fmt.Errorf("stuntgres: CTE modifier completed more than once")
	}
	if producer == nil {
		producer = &exec.CTEProducer{}
	}
	producer.Done, producer.Closed, producer.Affected = true, true, count
	producer.MutationPending = false
	if mutation, _ := analyze.MutationBinding(definition); mutation.Returning != nil {
		rows, ok := output.(*exec.FinalRows)
		if !ok {
			if output != nil {
				output.Close()
			}
			return fmt.Errorf("stuntgres: CTE modifier lacks typed output")
		}
		if producer.Source == nil {
			producer.Source = &exec.Source{Materialized: true, Authority: o.State.Source.Authority, Host: o.State.Source.Host, Locks: o.State.Source.Locks}
		}
		producer.Source.Flat, producer.Source.FlatWidth, producer.Source.FlatCount = rows.Values, len(rows.Columns), rows.Count
		rows.Values = nil // Transfer the typed buffer, never Scan/reimport/copy.
		if err := rows.Close(); err != nil {
			return err
		}
	} else if output != nil {
		output.Close()
		return fmt.Errorf("stuntgres: no-RETURNING CTE produced a relation")
	}
	if o.Producers == nil {
		o.Producers = make(map[*plan.CTEDefinition]*exec.CTEProducer)
	}
	if o.Producers[definition] == nil {
		o.Producers[definition] = producer
		o.Order = append(o.Order, producer)
	}
	return nil
}
