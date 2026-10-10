package pgencoding18

// REL_18_0 euc_jp_and_sjis.c: NEC/IBM, UDC and replacement choices are
// conversion-specific and not equivalent to Unicode composition.
func flag(b bool) int {
	if b {
		return 1
	}
	return 0
}
func jisToSJIS(c1, c2 int, plane2 bool) uint32 {
	k := c1<<8 | c2
	extra := 0
	if k >= 0xf5a1 {
		c1 -= 0x54
		if plane2 {
			extra = 0x74
		} else {
			extra = 0x6f
		}
	} else if plane2 {
		for _, row := range ibmkanji {
			if row[2]&0xffff == 0xffff {
				break
			}
			if int(row[2]&0xffff) == k {
				return row[1]
			}
		}
		return 0x81ac
	}
	head := (c1 - 0xa1) >> 1
	if c1 < 0xdf {
		head += 0x81
	} else {
		head += 0xc1
	}
	head += extra
	tail := c2
	if c1&1 != 0 {
		if c2 < 0xe0 {
			tail -= 0x61
		} else {
			tail -= 0x60
		}
	} else {
		tail -= 2
	}
	return uint32(byte(head))<<8 | uint32(byte(tail))
}
func eucToSJIS(s string, n int) uint32 {
	if s[0] == 0x8e {
		return uint32(s[1])
	}
	if s[0] == 0x8f {
		return jisToSJIS(int(s[1]), int(s[2]), true)
	}
	return jisToSJIS(int(s[0]), int(s[1]), false)
}
func sjisToEUC(s string, n int) uint32 {
	c1 := int(s[0])
	if n == 1 {
		return 0x8e00 | uint32(c1)
	}
	c2 := int(s[1])
	k := c1<<8 | c2
	if k >= 0xed40 && k < 0xf040 {
		for _, row := range ibmkanji {
			if row[0] == 0xffff {
				break
			}
			if int(row[0]) == k {
				k = int(row[1])
				c1 = k >> 8
				c2 = k & 255
			}
		}
	}
	base := 0x9f
	prefix := uint32(0)
	switch {
	case k < 0xeb3f:
	case k >= 0xeb40 && k < 0xf040 || k >= 0xfc4c && k <= 0xfcfc:
		return 0xa2ae
	case k >= 0xf040 && k < 0xf540:
		c1 -= 0x6f
		base = 0xf3
	case k >= 0xf540 && k < 0xfa40:
		c1 -= 0x74
		base = 0xf3
		prefix = 0x8f0000
	case k >= 0xfa40:
		for _, row := range ibmkanji {
			if row[1] == 0xffff {
				break
			}
			if int(row[1]) == k {
				return row[2] | 0x8080
			}
		}
		// The release consumes unknown IBM positions without emitting a character.
		return 0
	default:
		return 0
	}
	head := ((c1 & 0x3f) << 1) + base + flag(c2 > 0x9e)
	tail := c2 + flag(c2 < 0x80)
	if c2 > 0x9e {
		tail += 2
	} else {
		tail += 0x60
	}
	return prefix | uint32(byte(head))<<8 | uint32(byte(tail))
}

// REL_18_0 euc2004_sjis2004.c, without legacy SJIS replacement rules.
func jis2004(src int, s string, n int) (uint32, bool) {
	if src == 5 {
		if s[0] == 0x8e {
			return uint32(s[1]), false
		}
		ku, ten := int(s[0])-0xa0, int(s[1])-0xa0
		head := 0
		if s[0] == 0x8f {
			ku, ten = int(s[1])-0xa0, int(s[2])-0xa0
			switch ku {
			case 1, 3, 4, 5, 8, 12, 13, 14, 15:
				head = ((ku + 0x1df) >> 1) - (ku>>3)*3
			default:
				if ku < 78 || ku > 94 {
					return 0, true
				}
				head = (ku + 0x19b) >> 1
			}
		} else {
			if ku <= 62 {
				head = (ku + 0x101) >> 1
			} else {
				head = (ku + 0x181) >> 1
			}
		}
		tail := ten + 0x9e
		if ku%2 != 0 {
			tail = ten + 0x3f
			if ten >= 64 {
				tail++
			}
		}
		return uint32(head)<<8 | uint32(tail), false
	}
	c1 := int(s[0])
	if n == 1 {
		return 0x8e00 | uint32(c1), false
	}
	c2 := int(s[1])
	odd, ten := 0, c2-0x9e
	if c2 <= 0x7e {
		odd, ten = 1, c2-0x3f
	} else if c2 <= 0x9e {
		odd, ten = 1, c2-0x40
	}
	ku := 0
	plane2 := false
	switch {
	case c1 >= 0x81 && c1 <= 0x9f:
		ku = (c1 << 1) - 0x100 - odd
	case c1 >= 0xe0 && c1 <= 0xef:
		ku = (c1 << 1) - 0x180 - odd
	case c1 >= 0xf0 && c1 <= 0xf3:
		plane2 = true
		rows := [4][2]int{{8, 1}, {4, 3}, {12, 5}, {14, 13}}
		ku = rows[c1-0xf0][odd]
	case c1 >= 0xf4 && c1 <= 0xfc:
		plane2 = true
		if c1 == 0xf4 && odd == 1 {
			ku = 15
		} else {
			ku = (c1 << 1) - 0x19a - odd
		}
	default:
		return 0, true
	}
	prefix := uint32(0)
	if plane2 {
		prefix = 0x8f0000
	}
	return prefix | uint32(ku+0xa0)<<8 | uint32(ten+0xa0), false
}
