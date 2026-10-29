package fts

import "context"

// Statistics is invocation-owned. Counts use the released int4 arithmetic.
type Statistics map[string]WordStatistics
type WordStatistics struct{ Documents, Entries int32 }

func StatisticsWeights(text string) byte {
	var mask byte
	for i := range len(text) {
		c := text[i]
		if c >= 'a' && c <= 'd' {
			c -= 'a' - 'A'
		}
		if c >= 'A' && c <= 'D' {
			mask |= 1 << ('D' - c)
		}
	}
	return mask
}

// AccumulateStatistics reads retained canonical positions without copying or
// exposing their backing. A stripped entry counts once only without a mask.
func (v *Vector) AccumulateStatistics(ctx context.Context, mask byte, into Statistics) error {
	for _, entry := range v.lexemes {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := int32(0)
		if len(entry.positions) == 0 && mask == 0 {
			count = 1
		}
		for _, position := range entry.positions {
			if err := ctx.Err(); err != nil {
				return err
			}
			if mask == 0 || mask&(1<<position.weight) != 0 {
				count++
			}
		}
		if count != 0 {
			total := into[entry.word]
			total.Documents++
			total.Entries += count
			into[entry.word] = total
		}
	}
	return ctx.Err()
}
