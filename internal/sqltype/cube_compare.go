package sqltype

// cubeBound deliberately uses the released C Min/Max expressions: unordered
// comparisons choose the second endpoint. math.Min/Max and total float order
// would change the public NaN contract.
func cubeBound(p *cubePayload, coordinate int, upper bool) float64 {
	a := p.coordinates[coordinate]
	if p.point {
		return a
	}
	b := p.coordinates[p.dimension+coordinate]
	if upper {
		if a > b {
			return a
		}
	} else if a < b {
		return a
	}
	return b
}

func compareCube(a, b *cubePayload) int {
	common := min(a.dimension, b.dimension)
	for _, upper := range [2]bool{false, true} {
		for i := range common {
			x, y := cubeBound(a, i, upper), cubeBound(b, i, upper)
			if x > y {
				return 1
			}
			if x < y {
				return -1
			}
		}
	}
	larger, sign := a, 1
	if a.dimension < b.dimension {
		larger, sign = b, -1
	}
	for _, upper := range [2]bool{false, true} {
		for i := common; i < larger.dimension; i++ {
			x := cubeBound(larger, i, upper)
			if x > 0 {
				return sign
			}
			if x < 0 {
				return -sign
			}
		}
	}
	if a.dimension > b.dimension {
		return 1
	}
	if a.dimension < b.dimension {
		return -1
	}
	return 0
}
