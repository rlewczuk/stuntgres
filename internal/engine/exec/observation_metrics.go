package exec

import (
	"context"
	"stuntgres/internal/engine/ids"
	"time"
)

// TriggerActual identifies the trigger, not its shared procedure. Values are
// read only after actual command/worker completion.
type TriggerActual struct {
	OID            ids.OID
	Name, Relation string
	Calls          int64
	Elapsed        time.Duration
}

func (o *Observation) SetLogicalMetrics(accesses, writes, triggers bool) {
	o.logicalAccesses, o.logicalWrites, o.triggerMetrics = accesses, writes, triggers
}
func (o *Observation) RecordWrite(count int64) {
	if o != nil && o.logicalWrites {
		o.writes += count
	}
}

// ObserveLogicalWrite is called only at real provider acceptance boundaries.
func ObserveLogicalWrite(ctx context.Context, count int64) {
	observation, _ := ctx.Value(observationKey{}).(*Observation)
	observation.RecordWrite(count)
}

// ObserveLogicalAccess counts a tuple fetched by a serial mutation target or
// identity scan, outside query stages. It never infers work from source capacity.
func ObserveLogicalAccess(ctx context.Context) {
	o, _ := ctx.Value(observationKey{}).(*Observation)
	if o != nil && o.logicalAccesses {
		o.accesses++
	}
}

// LogicalMetrics reads stage-private counters only after workers have joined.
// No inclusive parent totals or output-row estimates enter the sum.
func (o *Observation) LogicalMetrics() (accesses, writes int64) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, records := range o.records {
		for _, record := range records {
			accesses += record.accesses
		}
	}
	return accesses + o.accesses, o.writes
}
func (o *Observation) BeginTrigger(oid ids.OID, name, relation string) (int, time.Time) {
	if o == nil || !o.triggerMetrics {
		return -1, time.Time{}
	}
	index := -1
	for i := range o.triggers {
		if o.triggers[i].OID == oid {
			index = i
			break
		}
	}
	if index < 0 {
		index = len(o.triggers)
		o.triggers = append(o.triggers, TriggerActual{OID: oid, Name: name, Relation: relation})
	}
	o.triggers[index].Calls++
	if o.timing {
		return index, time.Now()
	}
	return index, time.Time{}
}
func (o *Observation) EndTrigger(index int, started time.Time) {
	if index >= 0 && !started.IsZero() {
		o.triggers[index].Elapsed += time.Since(started)
	}
}
func (o *Observation) Triggers() []TriggerActual {
	if o == nil {
		return nil
	}
	return o.triggers
}
