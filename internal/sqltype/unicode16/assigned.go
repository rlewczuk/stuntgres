package unicode16

import "context"

// Assigned tests Unicode16 General_Category != Cn for every scalar. Controls,
// format characters and private-use ranges count as assigned. SQL text input
// owns UTF8/NUL validation; this does not make surrogate encodings legal.
func Assigned(ctx context.Context, source string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var work uint32
	for _, r := range source {
		work++
		if work&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if !caseProperty(r, assignedRanges[:]) {
			return false, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}
