package dml

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
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
}

type BeforeResult struct {
	Row     []sqltype.Value
	Proceed bool
}

type BeforeKey struct {
	Relation ids.ObjectRef
	Event    BeforeEvent
}

// BeforeCapture belongs to one command activation, never a prepared statement.
// Its immutable list is captured once for a physical relation/event.
type BeforeCapture struct {
	Relation    *catalog.Relation
	Catalog     catalog.Read
	Definitions []stored.Trigger
}

// Before invokes the physical relation's admitted chain exactly once at the
// semantic row boundary, before generation and proposal checks. It never holds
// Engine.mu. Relations without triggers allocate nothing and call no host.
func Before(h Host, relation *catalog.Relation, request BeforeRow) (BeforeResult, error) {
	if !relation.HasTriggers() {
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
