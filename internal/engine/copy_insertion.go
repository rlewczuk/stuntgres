package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
)

func recordCopyInsertion(state *dml.CopyInsertionState, s *StatementScope, b analyze.Binding, candidate *index.WriteCandidate, position int) {
	line, size := state.Cursor.InsertionOrigin()
	state.Lines = append(state.Lines, line)
	state.Bytes += size
	if s.partitionInsert == nil {
		return
	}
	group := s.partitionInsertionGroup(b.Ref)
	// Freeze the already-validated physical destination before the next input
	// overwrites the current partition projection. No second generation Eval.
	candidate.Values = s.partitionStoredRow(candidate.Values)
	if state.Groups == nil {
		state.Groups = make(map[*partition.InsertGroup]int)
	}
	at, exists := state.Groups[group]
	if !exists {
		at = len(state.Buffers)
		state.Groups[group] = at
		state.Buffers = append(state.Buffers, dml.CopyInsertionBuffer{Group: group})
	}
	state.Buffers[at].Positions = append(state.Buffers[at].Positions, position)
}

func flushCopyInsertion(state *dml.CopyInsertionState, s *StatementScope, b analyze.Binding, rows *index.WriteBatch, evaluation *exec.Context) error {
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
			return dml.CopyInsertionDiagnostic(s.copyCommandError(err), b.Relation.Name(), state.Lines[position-state.First])
		}
		rows.Set(position, candidate)
		return nil
	}
	if s.partitionInsert == nil {
		for i := state.First; i < len(rows.Values); i++ {
			if err := insert(i, nil); err != nil {
				return err
			}
		}
	} else {
		// REL18 flushes relation buffers in first-use order; each buffer owns
		// row-outer, ObjectID-ordered index insertion, not a global row sort.
		for i := range state.Buffers {
			buffer := &state.Buffers[i]
			for _, position := range buffer.Positions {
				if err := insert(position, buffer.Group); err != nil {
					return err
				}
			}
			buffer.Positions = buffer.Positions[:0]
		}
	}
	state.First, state.Bytes = len(rows.Values), 0
	state.Lines = state.Lines[:0]
	return nil
}

func (c *copyCursor) InsertionOrigin() (int64, int)       { return c.in.Number(), len(c.in.Line()) }
func (c *copyBinaryCursor) InsertionOrigin() (int64, int) { return c.in.Number(), 0 }

var (
	_ dml.CopyInsertionCursor = (*copyCursor)(nil)
	_ dml.CopyInsertionCursor = (*copyBinaryCursor)(nil)
)
