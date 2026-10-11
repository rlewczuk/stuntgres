package fixture

import (
	"math"
	"reflect"
)

func AnyValueRowsEqual(a, b [][]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i, row := range a {
		if len(row) != len(b[i]) {
			return false
		}
		for j, value := range row {
			if f, ok := value.(float64); ok {
				other, ok := b[i][j].(float64)
				if !ok || !(math.IsNaN(f) && math.IsNaN(other)) && math.Float64bits(f) != math.Float64bits(other) {
					return false
				}
			} else if !reflect.DeepEqual(value, b[i][j]) {
				return false
			}
		}
	}
	return true
}
