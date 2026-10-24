package lock

import (
	"strconv"
	"strings"
)

// Cast names are the catalog's namespace-free, directed decimal endpoint pair.
func validCastName(name string) bool {
	source, target, ok := strings.Cut(name, ",")
	if !ok {
		return false
	}
	for _, part := range [2]string{source, target} {
		if part == "" || part[0] == '0' {
			return false
		}
		for i := range len(part) {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	return true
}
