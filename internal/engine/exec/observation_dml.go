package exec

import (
	"context"

	"stuntgres/internal/engine/plan"
)

// DMLRole names work owned by a mutation producer, not an OpenNode identity.
type DMLRole uint8

const (
	DMLInsertInput DMLRole = iota
	DMLMergeTarget
	DMLMergeCandidates
	DMLTargetScan
	DMLCandidateJoin
	DMLPredicate
	DMLCursorLocator
	dmlRoleCount
)

// DMLObservation belongs to one accepted physical command. Rewrites and CTEs
// may share an Observation, but never these records. Only serial DML owners
// write them; renderers read after completion. There is no row-loop registry.
type DMLObservation struct {
	roles      [dmlRoleCount]DMLMeasurement
	actual     Actual
	definition *plan.CTEDefinition
	next       *DMLObservation // Exact invocation owner's auxiliary records.
}

type dmlObservationKey struct{}

// ReserveDML registers known roles in this actual invocation, including lazy
// auxiliaries not entered yet. An exact CTE owner/definition reuses its record;
// ordinary accepted commands always get a fresh record. Capture/entry only.
func (o *Observation) ReserveDML(owner *CTEInvocation, definition *plan.CTEDefinition, roles ...DMLRole) *DMLObservation {
	if o == nil || o.presentationOnly {
		return nil
	}
	dml := owner.DMLFor(definition)
	if dml == nil {
		dml = &DMLObservation{definition: definition}
		for _, role := range roles {
			dml.roles[role].known = true
			dml.roles[role].record.timing = o.timing
		}
		if owner != nil {
			dml.next, owner.dml = owner.dml, dml
		}
	}
	if !o.hasDML(dml) {
		o.dml = append(o.dml, dml)
	}
	return dml
}

func (o *CTEInvocation) DMLFor(definition *plan.CTEDefinition) *DMLObservation {
	if o != nil {
		for dml := o.dml; dml != nil; dml = dml.next {
			if dml.definition == definition {
				return dml
			}
		}
	}
	return nil
}

func (o *Observation) hasDML(dml *DMLObservation) bool {
	if o != nil {
		for _, known := range o.dml {
			if known == dml {
				return true
			}
		}
	}
	return false
}

func (dml *DMLObservation) Context(ctx context.Context) context.Context {
	if dml == nil {
		return ctx
	}
	return context.WithValue(ctx, dmlObservationKey{}, dml)
}

// CommandActual is the existing command-root completion scalar. Reserving it
// does not enter a command; only its real accepted handoff sets loops/actuals.
func (dml *DMLObservation) CommandActual() *Actual {
	if dml == nil {
		return nil
	}
	return &dml.actual
}

func DMLObservationOf(ctx context.Context) *DMLObservation {
	dml, _ := ctx.Value(dmlObservationKey{}).(*DMLObservation)
	observation, _ := ctx.Value(observationKey{}).(*Observation)
	if dml != nil && !observation.hasDML(dml) {
		return nil
	}
	return dml
}

func (dml *DMLObservation) Role(role DMLRole) *DMLMeasurement {
	if dml == nil || !dml.roles[role].known {
		return nil
	}
	return &dml.roles[role]
}

// DMLActual aggregates only registered invocation roles after serial command
// completion. It does not turn missing measurements into known zero actuals.
func (o *Observation) DMLActual(role DMLRole) (Actual, bool) {
	var actual Actual
	known := false
	if o != nil {
		for _, dml := range o.dml {
			if measurement := dml.Role(role); measurement != nil {
				value := measurement.Actual()
				actual.Rows += value.Rows
				actual.Loops += value.Loops
				actual.Elapsed += value.Elapsed
				known = true
			}
		}
	}
	return actual, known
}

// DMLMeasurement reuses stage active-span accounting without claiming a
// synthetic presentation node was opened. Loop entry and emission are explicit
// producer events, independent of action admission and command affected count.
type DMLMeasurement struct {
	known  bool
	record stageObservation
}

func (m *DMLMeasurement) BeginLoop() {
	if m != nil {
		m.record.loops++
	}
}

func (m *DMLMeasurement) StartWork() {
	if m != nil && m.record.timing {
		m.record.startWork()
	}
}

func (m *DMLMeasurement) EndWork() {
	if m != nil && m.record.timing {
		m.record.end()
	}
}

func (m *DMLMeasurement) Emit() {
	if m != nil {
		m.record.rows++
	}
}

func (m *DMLMeasurement) Actual() Actual {
	return Actual{Rows: m.record.rows, Loops: m.record.loops, Elapsed: m.record.elapsed}
}
