package pgencoding18

// HasPair reports an exact direct default conversion, not reachability.
func HasPair(source, destination int) bool {
	return source >= 0 && source < 42 && destination >= 0 && destination < 42 && pairs[source][destination]
}

// Dedicated REL_18_0 conversion_procs tables; intentionally includes their
// holes and asymmetric mappings instead of composing through UTF8.
func singleTable(src, dst int) []byte {
	switch [2]int{src, dst} {
	case [2]int{25, 22}:
		return iso2koi[:]
	case [2]int{22, 25}:
		return koi2iso[:]
	case [2]int{23, 22}:
		return win12512koi[:]
	case [2]int{22, 23}:
		return koi2win1251[:]
	case [2]int{20, 22}:
		return win8662koi[:]
	case [2]int{22, 20}:
		return koi2win866[:]
	case [2]int{20, 23}:
		return win8662win1251[:]
	case [2]int{23, 20}:
		return win12512win866[:]
	case [2]int{25, 23}:
		return iso2win1251[:]
	case [2]int{23, 25}:
		return win12512iso[:]
	case [2]int{25, 20}:
		return iso2win866[:]
	case [2]int{20, 25}:
		return win8662iso[:]
	case [2]int{29, 9}:
		return win1250_2_iso88592[:]
	case [2]int{9, 29}:
		return iso88592_2_win1250[:]
	}
	return nil
}
func micSingle(id int, toMIC bool) (byte, []byte) {
	switch id {
	case 8:
		return 0x81, nil
	case 9:
		return 0x82, nil
	case 10:
		return 0x83, nil
	case 11:
		return 0x84, nil
	case 22:
		return 0x8b, nil
	case 25:
		if toMIC {
			return 0x8b, iso2koi[:]
		}
		return 0x8b, koi2iso[:]
	case 23:
		if toMIC {
			return 0x8b, win12512koi[:]
		}
		return 0x8b, koi2win1251[:]
	case 20:
		if toMIC {
			return 0x8b, win8662koi[:]
		}
		return 0x8b, koi2win866[:]
	case 29:
		if toMIC {
			return 0x82, win1250_2_iso88592[:]
		}
		return 0x82, iso88592_2_win1250[:]
	}
	return 0, nil
}
func direct(src, dst int, s string, n int) (uint32, uint32, int, bool) {
	if tab := singleTable(src, dst); tab != nil {
		c := tab[s[0]-128]
		if c == 0 {
			return 0, 0, 0, false
		}
		return uint32(c), 0, n, false
	}
	if src == 5 && dst == 41 || src == 41 && dst == 5 {
		v, bad := jis2004(src, s, n)
		if bad {
			return 0, 0, 0, true
		}
		return v, 0, n, false
	}
	if src == 1 && dst == 35 {
		return eucToSJIS(s, n), 0, n, false
	}
	if src == 35 && dst == 1 {
		return sjisToEUC(s, n), 0, n, false
	}
	if src == 4 && dst == 36 || src == 36 && dst == 4 {
		v := taiwan(src, dst, s, n)
		if v == 0 {
			return 0, 0, 0, false
		}
		return v, 0, n, false
	}
	if dst == 7 {
		lc, tab := micSingle(src, true)
		if lc != 0 {
			c := s[0]
			if tab != nil {
				c = tab[c-128]
				if c == 0 {
					return 0, 0, 0, false
				}
			}
			return uint32(lc)<<8 | uint32(c), 0, n, false
		}
		switch src {
		case 2:
			return 0x910000 | Pack(s, 2), 0, n, false
		case 3:
			return 0x930000 | Pack(s, 2), 0, n, false
		case 1:
			return eucToMIC(s, n), 0, n, false
		case 35:
			if n == 1 {
				return 0x8900 | uint32(s[0]), 0, n, false
			}
			e := sjisToEUC(s, n)
			if e == 0 {
				return 0, 0, n, false
			}
			if e > 0xffff {
				return 0x940000 | e&0xffff, 0, n, false
			}
			return 0x920000 | e, 0, n, false
		case 4, 36:
			v := taiwan(src, dst, s, n)
			if v != 0 {
				return v, 0, n, false
			}
		}
	}
	if src == 7 {
		lc, tab := micSingle(dst, false)
		if lc != 0 {
			if n != 2 || s[0] != lc {
				return 0, 0, 0, false
			}
			c := s[1]
			if tab != nil {
				c = tab[c-128]
			}
			if c == 0 {
				return 0, 0, 0, false
			}
			return uint32(c), 0, n, false
		}
		switch dst {
		case 2:
			if s[0] == 0x91 {
				return Pack(s[1:], 2), 0, n, false
			}
		case 3:
			if s[0] == 0x93 {
				return Pack(s[1:], 2), 0, n, false
			}
		case 1:
			if v := micToEUC(s, n); v != 0 {
				return v, 0, n, false
			}
		case 35:
			switch s[0] {
			case 0x89:
				return uint32(s[1]), 0, n, false
			case 0x92:
				return jisToSJIS(int(s[1]), int(s[2]), false), 0, n, false
			case 0x94:
				return jisToSJIS(int(s[1]), int(s[2]), true), 0, n, false
			}
		case 4, 36:
			v := taiwan(src, dst, s, n)
			if v != 0 {
				return v, 0, n, false
			}
		}
	}
	return 0, 0, 0, false
}
func eucToMIC(s string, n int) uint32 {
	if s[0] == 0x8e {
		return 0x8900 | uint32(s[1])
	}
	if s[0] == 0x8f {
		return 0x940000 | Pack(s[1:], 2)
	}
	return 0x920000 | Pack(s, n)
}
func micToEUC(s string, n int) uint32 {
	switch s[0] {
	case 0x89:
		return 0x8e00 | uint32(s[1])
	case 0x92:
		return Pack(s[1:], 2)
	case 0x94:
		return 0x8f0000 | Pack(s[1:], 2)
	}
	return 0
}
