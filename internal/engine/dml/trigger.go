package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type BeforeEvent uint8

const (
	BeforeInsert BeforeEvent = 1 << iota
	BeforeUpdate
	BeforeDelete
)

type BeforeRow struct {
	Relation ids.ObjectRef
	Event    BeforeEvent
	Old, New []sqltype.Value
	Targets  []int32
	Instead  bool
}

type BeforeResult struct {
	Row     []sqltype.Value
	Proceed bool
}

type BeforeKey struct {
	Relation ids.ObjectRef
	Event    BeforeEvent
	Instead  bool
}

// BeforeCapture belongs to one command activation, never a prepared statement.
// Its immutable list is captured once for an admitted relation/event/timing.
type BeforeCapture struct {
	Relation    *catalog.Relation
	Catalog     catalog.Read
	Definitions []stored.Trigger
}

// Before invokes the physical relation's admitted chain exactly once at the
// semantic row boundary, before generation and proposal checks. It never holds
// Engine.mu. Relations without triggers allocate nothing and call no host.
func Before(h Host, relation *catalog.Relation, request BeforeRow) (BeforeResult, error) {
	if !HasRowTriggers(relation, stored.TriggerEvent(request.Event), stored.TriggerBefore) {
		return BeforeResult{Row: request.New, Proceed: true}, nil
	}
	if request.Event != BeforeDelete {
		for attribute := range relation.ColumnMeta.Generations {
			for slot, a := range relation.Attributes {
				if a == attribute {
					request.New[slot] = sqltype.Null(relation.Columns[slot].Usage.Type)
					break
				}
			}
		}
	}
	return h.BeforeRow(request)
}

func HasRowTriggers(relation *catalog.Relation, event stored.TriggerEvent, timing stored.TriggerTiming) bool {
	for _, definition := range stored.TriggersOf(relation) {
		p := definition.Parts()
		if p.Timing == timing && p.Level == stored.TriggerRow && p.Events&event != 0 {
			return true
		}
	}
	return false
}

// BeforeStatement is gated only by metadata relevance. Mode, WHEN and UPDATE OF
// belong to the host after it marks this query's first relation/event producer.
// Trigger-free and irrelevant metadata paths call no host and allocate nothing.
func BeforeStatement(h Host, b analyze.Binding, event stored.TriggerEvent) error {
	if !HasStatementTriggers(b.Relation, event, stored.TriggerBefore) {
		return nil
	}
	var targets []int32
	if event == stored.TriggerUpdate {
		targets = UpdateTargetAttributes(h, b.Ref, b.Relation, b.Targets, nil)
	}
	return h.BeforeStatement(b.Ref, event, targets)
}

func HasStatementTriggers(relation *catalog.Relation, event stored.TriggerEvent, timing stored.TriggerTiming) bool {
	if relation == nil {
		return false
	}
	for _, definition := range stored.TriggersOf(relation) {
		p := definition.Parts()
		if p.Timing == timing && p.Level == stored.TriggerStatement && p.Events&event != 0 {
			return true
		}
	}
	return false
}

// StatementEvents is the logical event set of an accepted mutation, independent
// of affected rows or chosen conflict/MERGE actions.
func StatementEvents(b analyze.Binding, statement parser.Statement) stored.TriggerEvent {
	if b.Merge != nil {
		var events stored.TriggerEvent
		for _, arm := range b.Merge.Arms {
			switch arm.Action {
			case parser.MergeInsertAction:
				events |= stored.TriggerInsert
			case parser.MergeUpdateAction:
				events |= stored.TriggerUpdate
			case parser.MergeDeleteAction:
				events |= stored.TriggerDelete
			}
		}
		return events
	}
	switch StatementCTERoot(statement).(type) {
	case *parser.Insert:
		events := stored.TriggerInsert
		if b.Insert.Conflict != nil && b.Insert.Conflict.Update != nil {
			events |= stored.TriggerUpdate
		}
		return events
	case *parser.Update:
		return stored.TriggerUpdate
	case *parser.Delete:
		return stored.TriggerDelete
	}
	return 0
}

// RegisterAfterStatement is execution-only and independent of row production.
func RegisterAfterStatement(h Host, b analyze.Binding, event stored.TriggerEvent) error {
	for _, definition := range stored.TriggersOf(b.Relation) {
		p := definition.Parts()
		if p.Timing == stored.TriggerAfter && p.Level == stored.TriggerStatement && p.Events&event != 0 {
			var targets []int32
			if event == stored.TriggerUpdate {
				targets = UpdateTargetAttributes(h, b.Ref, b.Relation, b.Targets, nil)
			}
			return h.AfterStatement(b.Ref, event, targets)
		}
	}
	return nil
}

// CompleteAfterStatement qualifies statement conditions at producer exhaustion,
// without draining the shared query's row or statement bodies.
func CompleteAfterStatement(h Host, b analyze.Binding, event stored.TriggerEvent, outcome *Outcome, failure *error) {
	if *failure != nil {
		return
	}
	for _, definition := range stored.TriggersOf(b.Relation) {
		p := definition.Parts()
		if p.Timing != stored.TriggerAfter || p.Level != stored.TriggerStatement || p.Events&event == 0 {
			continue
		}
		if err := h.CompleteAfterStatement(b.Ref, event); err != nil {
			if outcome.Rows != nil {
				outcome.Rows.Close()
			}
			*outcome, *failure = Outcome{}, err
		}
		return
	}
}
