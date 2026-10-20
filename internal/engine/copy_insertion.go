package engine

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
)

// Physical input keeps its immediate codec/default/constraint events. Eligible
// COPY buffers then own a distinct insertion flush, unlike ordinary INSERT.
type copyInsertionCursor interface {
	insertionOrigin() (line int64, textBytes int)
}

type copyInsertionBuffer struct {
	group     *partitionInsertGroup
	positions []int
}

type copyInsertionState struct {
	cursor       copyInsertionCursor
	single       bool
	first, bytes int
	lines        []int64
	buffers      []copyInsertionBuffer
	groups       map[*partitionInsertGroup]int
}

func newCopyInsertionState(cursor copyInsertionCursor, b binding) *copyInsertionState {
	state := &copyInsertionState{cursor: cursor}
	for _, expression := range b.insert.defaults {
		if PlannedPhysicalVolatility(expression, true) == expr.Volatile {
			state.single = true
			break
		}
	}
	return state
}

func (state *copyInsertionState) record(s *StatementScope, b binding, candidate *indexWriteCandidate, position int) {
	line, size := state.cursor.insertionOrigin()
	state.lines = append(state.lines, line)
	state.bytes += size
	if s.partitionInsert == nil {
		return
	}
	group := s.partitionInsertionGroup(b.ref)
	// Freeze the already-validated physical destination before the next input
	// overwrites the current partition projection. No second generation Eval.
	candidate.values = s.partitionStoredRow(candidate.values)
	if state.groups == nil {
		state.groups = make(map[*partitionInsertGroup]int)
	}
	at, exists := state.groups[group]
	if !exists {
		at = len(state.buffers)
		state.groups[group] = at
		state.buffers = append(state.buffers, copyInsertionBuffer{group: group})
	}
	state.buffers[at].positions = append(state.buffers[at].positions, position)
}

func (state *copyInsertionState) full() bool {
	return state.single || len(state.lines) >= 1000 || state.bytes >= 65535
}

func (state *copyInsertionState) flush(s *StatementScope, b binding, rows *indexWriteBatch, evaluation *EvalContext) error {
	insert := func(position int, group *partitionInsertGroup) error {
		candidate := rows.candidate(position)
		var err error
		if group == nil {
			_, err = s.admitInsertProducer(b, &candidate, rows.prefix(position), evaluation)
		} else {
			s.uniqueBinding = group.unique
			err = s.checkUniqueRow(group.ref, &candidate, nil, group.rows)
			group.unique = s.uniqueBinding
			if err == nil {
				group.rows.append(candidate)
			}
		}
		if err != nil {
			return copyInsertionDiagnostic(s.copyCommandError(err), b.relation.Name(), state.lines[position-state.first])
		}
		rows.set(position, candidate)
		return nil
	}
	if s.partitionInsert == nil {
		for i := state.first; i < len(rows.values); i++ {
			if err := insert(i, nil); err != nil {
				return err
			}
		}
	} else {
		// REL18 flushes relation buffers in first-use order; each buffer owns
		// row-outer, ObjectID-ordered index insertion, not a global row sort.
		for i := range state.buffers {
			buffer := &state.buffers[i]
			for _, position := range buffer.positions {
				if err := insert(position, buffer.group); err != nil {
					return err
				}
			}
			buffer.positions = buffer.positions[:0]
		}
	}
	state.first, state.bytes = len(rows.values), 0
	state.lines = state.lines[:0]
	return nil
}

func copyInsertionDiagnostic(err error, relation string, line int64) error {
	var record *sqlstate.Error
	if !errors.As(err, &record) {
		return err
	}
	result := *record
	result.Context = fmt.Sprintf("COPY %s, line %d", relation, line)
	if record.Context != "" {
		result.Context = record.Context + "\n" + result.Context
	}
	return &result
}

func (c *copyCursor) insertionOrigin() (int64, int)       { return c.number, len(c.line) }
func (c *copyBinaryCursor) insertionOrigin() (int64, int) { return c.number, 0 }
