package pgencoding18

// Port of REL_18_0 big5.c. Its range-search order, including the unusual
// CNS plane-2 entries, is retained rather than sorted or inverted.
// big5.c is partially copied from lv, copyright NARITA Tomio; see LICENSE.
func searchRange(table [][2]uint32, high, code int) uint32 {
	low := 0
	for low <= high {
		mid := (low + high) >> 1
		start, peer := int(table[mid][0]), int(table[mid][1])
		if start <= code && int(table[mid+1][0]) > code {
			if peer == 0 {
				return 0
			}
			rows := (code >> 8) - (start >> 8)
			if code >= 0xa140 {
				hi, lo := code&255, start&255
				bias := 0
				if hi >= 0xa1 && lo < 0xa1 {
					bias = -0x22
				} else if hi < 0xa1 && lo >= 0xa1 {
					bias = 0x22
				}
				distance := rows*0x9d + hi - lo + bias
				tmp := (peer & 255) + distance - 0x21
				return uint32((peer & 0xff00) + (tmp / 0x5e << 8) + 0x21 + tmp%0x5e)
			}
			distance := rows*0x5e + (code & 255) - (start & 255)
			lo := peer & 255
			bias := 0x40
			if lo >= 0xa1 {
				bias = 0x62
			}
			tmp := lo + distance - bias
			lo = tmp % 0x9d
			bias = 0x40
			if lo > 0x3e {
				bias = 0x62
			}
			return uint32((peer & 0xff00) + (tmp / 0x9d << 8) + bias + lo)
		}
		if start > code {
			high = mid - 1
		} else {
			low = mid + 1
		}
	}
	return 0
}
func big5CNS(code uint32) (uint32, byte) {
	if code < 0xc940 {
		for _, r := range b1c4 {
			if r[0] == code {
				return r[1] | 0x8080, 0xf7
			}
		}
		if c := searchRange(big5Level1ToCnsPlane1[:], 23, int(code)); c != 0 {
			return c | 0x8080, 0x95
		}
	} else if code == 0xc94a {
		return 0xc4c2, 0x95
	} else {
		for _, r := range b2c3 {
			if r[0] == code {
				return r[1] | 0x8080, 0xf6
			}
		}
		if c := searchRange(big5Level2ToCnsPlane2[:], 46, int(code)); c != 0 {
			return c | 0x8080, 0x96
		}
	}
	return 0, 0
}
func cnsBig5(code uint32, lc byte) uint32 {
	code &= 0x7f7f
	switch lc {
	case 0x95:
		return searchRange(cnsPlane1ToBig5Level1[:], 24, int(code))
	case 0x96:
		return searchRange(cnsPlane2ToBig5Level2[:], 47, int(code))
	case 0xf6:
		for _, r := range b2c3 {
			if r[1] == code {
				return r[0]
			}
		}
	case 0xf7:
		for _, r := range b1c4 {
			if r[1] == code {
				return r[0]
			}
		}
	}
	return 0
}
func taiwan(src, dst int, s string, n int) uint32 {
	var cns uint32
	var lc byte
	switch src {
	case 36:
		cns, lc = big5CNS(Pack(s, 2))
		if lc == 0 {
			return 0
		}
	case 4:
		if s[0] == 0x8e {
			plane := s[1]
			switch plane {
			case 0xa1:
				lc = 0x95
			case 0xa2:
				lc = 0x96
			default:
				lc = plane - 0xa3 + 0xf6
			}
			cns = Pack(s[2:], 2)
		} else {
			lc = 0x95
			cns = Pack(s, 2)
		}
	case 7:
		lc = s[0]
		if lc == 0x9d {
			lc = s[1]
			cns = Pack(s[2:], 2)
		} else if lc == 0x95 || lc == 0x96 {
			cns = Pack(s[1:], 2)
		} else {
			return 0
		}
	}
	switch dst {
	case 36:
		return cnsBig5(cns, lc)
	case 7:
		if lc == 0x95 || lc == 0x96 {
			return uint32(lc)<<16 | cns
		}
		return 0x9d000000 | uint32(lc)<<16 | cns
	case 4:
		if lc == 0x95 {
			return cns
		}
		if lc == 0x96 {
			return 0x8ea20000 | cns
		}
		if lc >= 0xf6 && lc <= 0xfa {
			return 0x8e000000 | uint32(lc-0xf6+0xa3)<<16 | cns
		}
	}
	return 0
}
