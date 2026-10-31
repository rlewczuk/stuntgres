package core_test

import (
	"fmt"
	"strings"
)

func conflictUpdateBatchValues(size int, value int64) string {
	var rows strings.Builder
	for k := 1; k <= size; k++ {
		if k != 1 {
			rows.WriteByte(',')
		}
		fmt.Fprintf(&rows, "(%d,%d)", k, value)
	}
	return rows.String()
}
