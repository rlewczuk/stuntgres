package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
)

func recordCopyInsertion(state *dml.CopyInsertionState, h dml.Host, b analyze.Binding, candidate *index.WriteCandidate, position int) {
	line, size := state.Cursor.InsertionOrigin()
	state.Lines = append(state.Lines, line)
	state.Bytes += size
	command := h.Command()
	if command.PartitionInsert == nil {
		return
	}
	group := command.PartitionInsertionGroup(b.Ref)
	// Freeze the already-validated physical destination before the next input
	// overwrites the current partition projection. No second generation Eval.
	candidate.Values = command.PartitionStoredRow(candidate.Values)
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

func flushCopyInsertion(state *dml.CopyInsertionState, h dml.Host, b analyze.Binding, rows *index.WriteBatch, evaluation *exec.Context) error {
	command := h.Command()
	insert := func(position int, group *partition.InsertGroup) error {
		candidate := rows.Candidate(position)
		var err error
		if group == nil {
			_, err = AdmitInsertProducer(h, b, &candidate, rows.Prefix(position), evaluation)
		} else {
			command.UniqueBinding = group.Unique
			err = dml.CheckUniqueRow(h, group.Ref, &candidate, nil, group.Rows)
			group.Unique = command.UniqueBinding
			if err == nil {
				group.Rows.Append(candidate)
			}
		}
		if err != nil {
			return dml.CopyInsertionDiagnostic(h.CopyCommandError(err), b.Relation.Name(), state.Lines[position-state.First])
		}
		rows.Set(position, candidate)
		return nil
	}
	if command.PartitionInsert == nil {
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

func (c *CopyCursor) InsertionOrigin() (int64, int)       { return c.in.Number(), len(c.in.Line()) }
func (c *CopyBinaryCursor) InsertionOrigin() (int64, int) { return c.in.Number(), 0 }

var (
	_ dml.CopyInsertionCursor = (*CopyCursor)(nil)
	_ dml.CopyInsertionCursor = (*CopyBinaryCursor)(nil)
)
