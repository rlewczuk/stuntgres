package dml

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

// CompleteCTEMutation records the finished data-modifying CTE definition in
// its owner invocation o: count affected rows and, for RETURNING, the typed
// output rows, whose buffer the producer takes over without copying. It closes
// output on every error and fails when the definition completes twice or its
// output does not match its RETURNING shape.
func CompleteCTEMutation(o *exec.CTEInvocation, definition *plan.CTEDefinition, count int64, output exec.RowResult) error {
	if o.Closed || o.Producers[definition] != nil {
		if output != nil {
			output.Close()
		}
		return fmt.Errorf("stuntgres: CTE modifier completed more than once")
	}
	producer := &exec.CTEProducer{Done: true, Closed: true, Affected: count}
	if mutation, _ := analyze.MutationBinding(definition); mutation.Returning != nil {
		rows, ok := output.(*exec.FinalRows)
		if !ok {
			if output != nil {
				output.Close()
			}
			return fmt.Errorf("stuntgres: CTE modifier lacks typed output")
		}
		producer.Source = &exec.Source{Flat: rows.Values, FlatWidth: len(rows.Columns), FlatCount: rows.Count, Materialized: true, Authority: o.State.Source.Authority, Host: o.State.Source.Host, Locks: o.State.Source.Locks}
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
	o.Producers[definition] = producer
	o.Order = append(o.Order, producer)
	return nil
}
