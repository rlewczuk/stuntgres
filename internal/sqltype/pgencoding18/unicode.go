package pgencoding18

// Radix lookup follows REL_18_0 conv.c pg_mb_radix_conv. Each direction is
// independently generated; there is no inversion, Unicode pivot or cache.
type level struct {
	root   uint32
	bounds [4][2]byte
}
type radix struct {
	wide   []uint32
	narrow []uint16
	levels [4]level
}

func (r *radix) lookup(code uint32, n int) uint32 {
	l := r.levels[n-1]
	idx := l.root
	for i := range n {
		b := byte(code >> uint(8*(n-i-1)))
		bounds := l.bounds[i]
		if b < bounds[0] || b > bounds[1] {
			return 0
		}
	}
	for i := range n {
		b := byte(code >> uint(8*(n-i-1)))
		idx += uint32(b - l.bounds[i][0])
		if r.wide != nil {
			idx = r.wide[idx]
		} else {
			idx = uint32(r.narrow[idx])
		}
	}
	return idx
}
func Pack(s string, n int) uint32 {
	var v uint32
	for i := range n {
		v = v<<8 | uint32(s[i])
	}
	return v
}
func Width(code uint32) int {
	if code > 0xffffff {
		return 4
	}
	if code > 0xffff {
		return 3
	}
	if code > 0xff {
		return 2
	}
	if code > 0 {
		return 1
	}
	return 0
}
func utfWord(c uint32) uint32 {
	if c < 128 {
		return c
	}
	if c < 0x800 {
		return (0xc0|c>>6)<<8 | 0x80 | c&63
	}
	if c < 0x10000 {
		return (0xe0|c>>12)<<16 | (0x80|c>>6&63)<<8 | 0x80 | c&63
	}
	return (0xf0|c>>18)<<24 | (0x80|c>>12&63)<<16 | (0x80|c>>6&63)<<8 | 0x80 | c&63
}
func utfScalar(c uint32) uint32 {
	if c < 128 {
		return c
	}
	if c <= 0xffff {
		return (c>>8&31)<<6 | c&63
	}
	if c <= 0xffffff {
		return (c>>16&15)<<12 | (c>>8&63)<<6 | c&63
	}
	return (c>>24&7)<<18 | (c>>16&63)<<12 | (c>>8&63)<<6 | c&63
}
func gbLinear(c uint32) uint32 {
	return (c>>24-0x81)*12600 + (c>>16&255-0x30)*1260 + (c>>8&255-0x81)*10 + (c&255 - 0x30)
}
func gbUnlinear(c uint32) uint32 {
	return (0x81+c/12600)<<24 | (0x30+c/1260%10)<<16 | (0x81+c/10%126)<<8 | 0x30 + c%10
}

var gbRanges = [...][4]uint32{
	{0x0452, 0x200f, 0x8130d330, 0x8136a531}, {0x2643, 0x2e80, 0x8137a839, 0x8138fd38},
	{0x361b, 0x3917, 0x8230a633, 0x8230f237}, {0x3ce1, 0x4055, 0x8231d438, 0x8232af32},
	{0x4160, 0x4336, 0x8232c937, 0x8232f837}, {0x44d7, 0x464b, 0x8233a339, 0x8233c931},
	{0x478e, 0x4946, 0x8233e838, 0x82349638}, {0x49b8, 0x4c76, 0x8234a131, 0x8234e733},
	{0x9fa6, 0xd7ff, 0x82358f33, 0x8336c738}, {0xe865, 0xf92b, 0x8336d030, 0x84308534},
	{0xfa2a, 0xfe2f, 0x84309c38, 0x84318537}, {0xffe6, 0xffff, 0x8431a234, 0x8431a439},
	{0x10000, 0x10ffff, 0x90308130, 0xe3329a35},
}

func gbMap(code uint32, toUTF bool) uint32 {
	for _, r := range gbRanges {
		if toUTF {
			if code >= r[2] && code <= r[3] {
				return utfWord(gbLinear(code) - gbLinear(r[2]) + r[0])
			}
		} else {
			ucs := utfScalar(code)
			if ucs >= r[0] && ucs <= r[1] {
				return gbUnlinear(ucs - r[0] + gbLinear(r[2]))
			}
		}
	}
	return 0
}

// Step converts one source character (or a combined two-character sequence).
// A zero consumed count represents an untranslatable character; invalid marks
// a wrapper's stricter conversion-time validation, separate from Verify.
// The fixed two-word result needs no payload-sized temporary storage.
func Step(src, dst int, s string) (first, second uint32, consumed int, invalid bool) {
	n := Length(src, s)
	code := Pack(s, n)
	if code < 128 {
		return code, 0, n, false
	}
	if src != 6 && dst != 6 {
		return direct(src, dst, s, n)
	}
	local, dir := src, 0
	if src == 6 {
		local, dir = dst, 1
	}
	if local == 8 {
		if dir == 0 {
			return utfWord(code), 0, n, false
		}
		u := utfScalar(code)
		if u < 256 {
			return u, 0, n, false
		}
		return 0, 0, 0, false
	}
	if dir == 1 && len(s) > n {
		n2 := Length(6, s[n:])
		code2 := Pack(s[n:], n2)
		for _, c := range unicodeCombined[local][1] {
			if c[0] == code && c[1] == code2 {
				return c[2], 0, n + n2, false
			}
		}
	}
	if mapped := unicodeTrees[local][dir].lookup(code, n); mapped != 0 {
		return mapped, 0, n, false
	}
	if dir == 0 {
		for _, c := range unicodeCombined[local][0] {
			if c[0] == code {
				return c[1], c[2], n, false
			}
		}
	}
	if local == 39 {
		if mapped := gbMap(code, dir == 0); mapped != 0 {
			return mapped, 0, n, false
		}
	}
	return 0, 0, 0, false
}
