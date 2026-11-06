package storage

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

// AuxiliaryForks is an incarnation-owned publication value. Presence is not
// inferred from allocation bytes or from a visibility observer's lazy root.
type AuxiliaryForks struct {
	FSM, VM bool
	FSMBlocks, VMBlocks int64
}
func (f AuxiliaryForks) Valid() bool {
	return f.FSMBlocks >= 0 && f.VMBlocks >= 0 && (f.FSM || f.FSMBlocks == 0) && (f.VM || f.VMBlocks == 0)
}

// VacuumForks derives only from the successful pass's actual tuple/freeze work.
// The returned scalar value is published after the pass's final currentness fence.
func VacuumForks(ctx context.Context, previous AuxiliaryForks, rows []RowVersion) (AuxiliaryForks, error) {
	var payload int64
	frozen := false
	for _, row := range rows {
		if err := ctx.Err(); err != nil { return AuxiliaryForks{}, err }
		weight, err := RowAllocation(ctx, row.Values)
		if err != nil { return AuxiliaryForks{}, err }
		weight, err = AddSize(weight, 4)
		if err != nil { return AuxiliaryForks{}, err }
		payload, err = AddSize(payload, weight)
		if err != nil { return AuxiliaryForks{}, err }
		frozen = frozen || row.Frozen || row.ImportFrozen
	}
	layout, err := FreespacePayload(ctx, payload)
	if err != nil { return AuxiliaryForks{}, err }
	if layout.Blocks > 0 { previous.FSM = true }
	if previous.FSM { previous.FSMBlocks = layout.Blocks/8168; if layout.Blocks%8168 != 0 { previous.FSMBlocks++ } }
	if frozen && layout.Blocks > 0 { previous.VM = true }
	if previous.VM && frozen { previous.VMBlocks = layout.Blocks/32672; if layout.Blocks%32672 != 0 { previous.VMBlocks++ } }
	if previous.VM && layout.Blocks == 0 { previous.VMBlocks = 0 }
	return previous, nil
}

// PrewarmRequest carries typed scalar bounds, not SQL syntax or cache authority.
type PrewarmRequest struct {
	Fork int16
	First, Last int64
	DefaultFirst, DefaultLast bool
}
func (r PrewarmRequest) Bounds(blocks int64) (int64, int64, error) {
	if blocks < 0 { return 0, 0, fmt.Errorf("stuntgres: negative prewarm extent") }
	first, last := r.First, r.Last
	if r.DefaultFirst { first = 0 } else if first < 0 || first >= blocks { return 0, 0, prewarmBoundError("starting", blocks) }
	if r.DefaultLast { last = blocks-1 } else if last < 0 || last >= blocks { return 0, 0, prewarmBoundError("ending", blocks) }
	return first, last, nil
}
func prewarmBoundError(which string, blocks int64) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("%s block number must be between 0 and %d", which, blocks-1)}
}
func (f AuxiliaryForks) Fork(number int16) (int64, bool) {
	switch number { case 1: return f.FSMBlocks, f.FSM; case 2: return f.VMBlocks, f.VM }
	return 0, false
}
