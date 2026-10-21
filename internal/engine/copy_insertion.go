package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
)

func recordCopyInsertion(state *copyInsertionState, s *StatementScope, b analyze.Binding, candidate *index.WriteCandidate, position int) {
	line, size := state.cursor.insertionOrigin()
	state.lines = append(state.lines, line)
	state.bytes += size
	if s.partitionInsert == nil {
		return
	}
	group := s.partitionInsertionGroup(b.Ref)
	// Freeze the already-validated physical destination before the next input
	// overwrites the current partition projection. No second generation Eval.
	candidate.Values = s.partitionStoredRow(candidate.Values)
	if state.groups == nil {
		state.groups = make(map[*partition.InsertGroup]int)
	}
	at, exists := state.groups[group]
	if !exists {
		at = len(state.buffers)
		state.groups[group] = at
		state.buffers = append(state.buffers, copyInsertionBuffer{group: group})
	}
	state.buffers[at].positions = append(state.buffers[at].positions, position)
}

func flushCopyInsertion(state *copyInsertionState, s *StatementScope, b analyze.Binding, rows *index.WriteBatch, evaluation *exec.Context) error {
	insert := func(position int, group *partition.InsertGroup) error {
		candidate := rows.Candidate(position)
		var err error
		if group == nil {
			_, err = s.admitInsertProducer(b, &candidate, rows.Prefix(position), evaluation)
		} else {
			s.uniqueBinding = group.Unique
			err = s.checkUniqueRow(group.Ref, &candidate, nil, group.Rows)
			group.Unique = s.uniqueBinding
			if err == nil {
				group.Rows.Append(candidate)
			}
		}
		if err != nil {
			return copyInsertionDiagnostic(s.copyCommandError(err), b.Relation.Name(), state.lines[position-state.first])
		}
		rows.Set(position, candidate)
		return nil
	}
	if s.partitionInsert == nil {
		for i := state.first; i < len(rows.Values); i++ {
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
	state.first, state.bytes = len(rows.Values), 0
	state.lines = state.lines[:0]
	return nil
}

func (c *copyCursor) insertionOrigin() (int64, int)       { return c.in.Number(), len(c.in.Line()) }
func (c *copyBinaryCursor) insertionOrigin() (int64, int) { return c.in.Number(), 0 }
