package ddl

import (
	"strconv"
	"unicode/utf8"
)

// GeneratedRelationName chooses the name PostgreSQL generates for an implicit
// relation: table and column truncated to fit 63 bytes on rune boundaries,
// then baseLabel with the first numeric suffix used does not report taken.
func GeneratedRelationName(table, column, baseLabel string, used func(string) bool) string {
	for suffix := 0; ; suffix++ {
		label := baseLabel
		if suffix != 0 {
			label += strconv.Itoa(suffix)
		}
		left, right := len(table), len(column)
		for left+right > 63-len(label)-2 {
			if left > right {
				left--
			} else {
				right--
			}
		}
		for left > 0 && left < len(table) && !utf8.RuneStart(table[left]) {
			left--
		}
		for right > 0 && right < len(column) && !utf8.RuneStart(column[right]) {
			right--
		}
		name := table[:left] + "_" + column[:right] + "_" + label
		if !used(name) {
			return name
		}
	}
}
