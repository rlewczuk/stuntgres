package storage

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// HeapVerification borrows immutable captured roots, never a live Store. It
// intentionally includes ended and snapshot-invisible committed versions.
type HeapVerification struct {
	Columns []catalog.Column
	Attributes []int32
	Committed, Own []RowVersion
	Ends *DeletionRoot
}

// HeapFinding has no physical coordinates. Attribute zero denotes a tuple-wide
// finding; Row and Version retain the exact captured logical identities.
type HeapFinding struct {
	Row ids.RowID
	Version ids.VersionID
	Attribute int32
	Message string
}

// VerifyHeapam checks exact typed admission and bounded native version facts.
// stop finishes the first invalid version, not merely its first finding.
func VerifyHeapam(ctx context.Context, input HeapVerification, stop bool) ([]HeapFinding, error) {
	if len(input.Attributes) != len(input.Columns) { return nil, fmt.Errorf("stuntgres: invalid heap verification descriptor") }
	for i, column := range input.Columns {
		if column.Usage.Type == nil || input.Attributes[i] <= 0 { return nil, fmt.Errorf("stuntgres: invalid heap verification attribute") }
	}
	if err := ctx.Err(); err != nil { return nil, err }
	var findings []HeapFinding
	var seen map[ids.VersionID]struct{}
	if count := len(input.Committed)+len(input.Own); count != 0 { seen = make(map[ids.VersionID]struct{}, count) }
	for group, rows := range [2][]RowVersion{input.Committed, input.Own} {
		for _, row := range rows {
			if err := ctx.Err(); err != nil { return nil, err }
			before := len(findings)
			add := func(attribute int32, reason string) {
				findings = append(findings, HeapFinding{Row: row.Row, Version: row.Version, Attribute: attribute, Message: fmt.Sprintf("logical row %d version %d: %s", row.Row, row.Version, reason)})
			}
			if row.Row == 0 { add(0, "row identity is zero") }
			if row.Version == 0 { add(0, "version identity is zero") }
			if _, duplicate := seen[row.Version]; duplicate { add(0, "duplicate version identity") }
			seen[row.Version] = struct{}{}
			if group == 0 {
				if row.Born == 0 { add(0, "committed version has no birth sequence") }
				if row.Base != 0 { add(0, "committed version retains a private predecessor") }
			} else if row.Born != 0 { add(0, "private version has a committed birth sequence") }
			if len(row.Values) != len(input.Columns) { add(0, fmt.Sprintf("row has %d values but relation has %d columns", len(row.Values), len(input.Columns))) }
			for i := range min(len(row.Values), len(input.Columns)) {
				if !row.Values[i].ValidResult(input.Columns[i].Usage) { add(input.Attributes[i], fmt.Sprintf("attribute %d has an invalid typed value", input.Attributes[i])) }
			}
			if group == 0 && input.Ends != nil {
				if end, ok := input.Ends.Rows[row.Row]; ok && end.Version == row.Version && (end.End == 0 || end.End < row.Born) { add(0, "invalid committed end sequence") }
			}
			if stop && len(findings) != before { return findings, nil }
		}
	}
	return findings, nil
}
