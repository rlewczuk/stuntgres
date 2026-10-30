package lock

import (
	"strconv"
	"strings"
)

// Cast and transform names are namespace-free canonical decimal endpoint pairs:
// source/target types for casts, SQL type/language for transforms.
func validCatalogPairName(name string) bool {
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
