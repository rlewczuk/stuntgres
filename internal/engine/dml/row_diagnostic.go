package dml

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// RowDiagnosticDetail renders the failing-row detail of row in b's target
// relation, disclosing only what v grants in env: the whole row with table
// SELECT, otherwise the supplied target columns plus the columns with column
// SELECT. With suppressEmpty and no visible column it returns "". The caller
// captures v under its authority window; this reads no engine state.
func RowDiagnosticDetail(v access.View, env access.Environment, b *analyze.Binding, row []sqltype.Value, output sqltype.OutputEnvironment, suppressEmpty bool) (string, error) {
	whole, err := v.Effective(env, b.Ref, false)
	if err != nil {
		return "", err
	}
	if whole&authorization.SelectPrivilege != 0 {
		return exec.FailingRowDetail(row, output, nil, nil, b.Relation)
	}
	// Preserve the small/wide supplied-column manifest convention. Assignment
	// order can differ from physical order, which governs diagnostic disclosure.
	var small uint64
	var modified []uint64
	var visibleColumns [64]int
	visible := visibleColumns[:0]
	if len(b.Relation.Columns) > 64 {
		modified = make([]uint64, (len(b.Relation.Columns)+63)/64)
		visible = make([]int, 0, len(b.Relation.Columns))
	}
	for _, index := range b.Targets {
		word := &small
		if modified != nil {
			word = &modified[index/64]
		}
		*word |= uint64(1) << uint(index%64)
	}
	for index, attribute := range b.Relation.Attributes {
		word := small
		if modified != nil {
			word = modified[index/64]
		}
		if word&(uint64(1)<<uint(index%64)) != 0 {
			visible = append(visible, index)
			continue
		}
		ref := b.Ref
		ref.Address.SubID = attribute
		rights, err := v.Effective(env, ref, false)
		if err != nil {
			return "", err
		}
		if rights&authorization.SelectPrivilege != 0 {
			visible = append(visible, index)
		}
	}
	if suppressEmpty && len(visible) == 0 {
		return "", nil
	}
	return exec.FailingRowDetail(row, output, b.Relation.Columns, visible, b.Relation)
}
