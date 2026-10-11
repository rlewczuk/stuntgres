package sqltype

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

// networkDatum is an owned comparable scalar. IPv4 occupies address[0:4];
// its remaining bytes are always zero. Mapped IPv6 retains family 6.
type networkDatum struct {
	address [16]byte
	family  uint8
	prefix  uint8
}

func IsNetwork(id ID) bool { return id == Inet || id == Cidr }

func networkDecimal(s string, limit int, leadingZero bool) (int, bool) {
	if len(s) == 0 || !leadingZero && len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
		if n > limit {
			return 0, false
		}
	}
	return n, true
}
func networkHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func networkIPv4(s string, dst []byte, terminalDot bool) (int, bool) {
	n := 0
	for {
		if n == len(dst) {
			return 0, false
		}
		i := strings.IndexByte(s, '.')
		part := s
		if i >= 0 {
			part = s[:i]
		}
		value, ok := networkDecimal(part, 255, true)
		if !ok {
			return 0, false
		}
		dst[n] = byte(value)
		n++
		if i < 0 {
			return n, true
		}
		s = s[i+1:]
		// REL_18_0 inet_net_pton_ipv4 ends the mantissa after a dot when
		// no digit follows at end-of-input or before the separately parsed
		// mask. CIDR's standalone parser does not allow this termination.
		if terminalDot && s == "" {
			return n, true
		}
	}
}

// networkIPv6Tail follows REL_18_0 getv4: a dot or slash writes the current
// octet even without digits, but EOF requires digits. The caller already
// validated the mask; slashTerminated preserves that delimiter's provenance.
func networkIPv6Tail(s string, dst []byte, slashTerminated bool) bool {
	for n := 0; ; n++ {
		if n == len(dst) {
			return false
		}
		end := strings.IndexByte(s, '.')
		part := s
		if end >= 0 {
			part = s[:end]
		}
		value, ok := networkDecimal(part, 255, false)
		if !ok && !(part == "" && (end >= 0 || slashTerminated)) {
			return false
		}
		dst[n] = byte(value)
		if end < 0 {
			return true
		}
		s = s[end+1:]
	}
}

func networkIPv6(s string, slashTerminated bool) ([16]byte, bool) {
	var address [16]byte
	used, compression := 0, -1
	if strings.HasPrefix(s, "::") {
		compression = 0
		s = s[2:]
	} else if strings.HasPrefix(s, ":") {
		return address, false
	}
	for s != "" {
		end := strings.IndexByte(s, ':')
		part := s
		if end >= 0 {
			part = s[:end]
		}
		if strings.Contains(part, ".") {
			// REL_18_0 getv4 zero-fills omitted trailing octets and accepts empty
			// interior octets; its leading-zero rule differs from standalone IPv4.
			if used > 12 || end >= 0 {
				return address, false
			}
			if !networkIPv6Tail(part, address[used:used+4], slashTerminated) {
				return address, false
			}
			used += 4
			s = ""
			break
		}
		if len(part) == 0 || len(part) > 4 || used > 14 {
			return address, false
		}
		word := uint16(0)
		for i := range len(part) {
			digit, ok := networkHex(part[i])
			if !ok {
				return address, false
			}
			word = word<<4 | uint16(digit)
		}
		address[used], address[used+1] = byte(word>>8), byte(word)
		used += 2
		if end < 0 {
			s = ""
			break
		}
		s = s[end+1:]
		// A colon before a validated slash may end the address; a colon
		// before EOF may not. The final size/compression checks still apply.
		if s == "" {
			if slashTerminated {
				break
			}
			return address, false
		}
		if s[0] == ':' {
			if compression >= 0 {
				return address, false
			}
			compression = used
			s = s[1:]
		}
	}
	if compression >= 0 {
		if used >= 16 {
			return address, false
		}
		tail := used - compression
		copy(address[16-tail:], address[compression:used])
		clear(address[compression : 16-tail])
	} else if used != 16 {
		return address, false
	}
	return address, true
}

func parseNetwork(text string, cidr bool) (networkDatum, bool) {
	var n networkDatum
	address, mask, explicit := text, "", false
	if i := strings.IndexByte(text, '/'); i >= 0 {
		address, mask, explicit = text[:i], text[i+1:], true
	}
	if strings.ContainsRune(address, ':') {
		n.family = 6
		n.prefix = 128
		if explicit {
			p, ok := networkDecimal(mask, 128, false)
			if !ok {
				return n, false
			}
			n.prefix = uint8(p)
		}
		var ok bool
		// Preserve slash versus EOF even though the mask is decoded separately.
		n.address, ok = networkIPv6(address, explicit)
		return n, ok
	}
	n.family = 4
	count := 0
	if cidr && len(address) > 2 && address[0] == '0' && (address[1] == 'x' || address[1] == 'X') {
		digits := address[2:]
		if len(digits) > 8 {
			return n, false
		}
		for i := range len(digits) {
			v, ok := networkHex(digits[i])
			if !ok {
				return n, false
			}
			if i%2 == 0 {
				n.address[i/2] = v << 4
			} else {
				n.address[i/2] |= v
			}
		}
		count = (len(digits) + 1) / 2
	} else {
		var ok bool
		count, ok = networkIPv4(address, n.address[:4], !cidr)
		if !ok {
			return n, false
		}
	}
	p := 32
	if explicit {
		var ok bool
		p, ok = networkDecimal(mask, 32, true)
		if !ok {
			return n, false
		}
	} else if cidr {
		switch {
		case n.address[0] >= 240:
			p = 32
		case n.address[0] >= 224:
			p = 8
		case n.address[0] >= 192:
			p = 24
		case n.address[0] >= 128:
			p = 16
		default:
			p = 8
		}
		if p < count*8 {
			p = count * 8
		}
		if p == 8 && n.address[0] == 224 {
			p = 4
		}
	} else if count != 4 {
		return n, false
	}
	if !cidr && p/8 > count {
		return n, false
	}
	n.prefix = uint8(p)
	return n, true
}

func (n networkDatum) masked() networkDatum {
	full, bits := int(n.prefix)/8, n.prefix%8
	if bits != 0 {
		n.address[full] &= byte(0xff << (8 - bits))
		full++
	}
	clear(n.address[full:])
	return n
}
func (n networkDatum) valid(cidr bool) bool {
	switch n.family {
	case 4:
		if n.prefix > 32 {
			return false
		}
		for _, v := range n.address[4:] {
			if v != 0 {
				return false
			}
		}
	case 6:
		if n.prefix > 128 {
			return false
		}
	default:
		return false
	}
	return !cidr || n == n.masked()
}
func (d *Descriptor) networkInput(text string) (Value, error) {
	n, ok := parseNetwork(text, d.id == Cidr)
	if !ok {
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for type %s: \"%s\"", d.name, text))
	}
	if d.id == Cidr && n != n.masked() {
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: fmt.Sprintf("invalid cidr value: \"%s\"", text), Detail: "Value has bits set to right of mask."}
	}
	return Value{typ: d, network: n}, nil
}

func appendNetworkIPv4(dst []byte, address []byte) []byte {
	for i, v := range address {
		if i > 0 {
			dst = append(dst, '.')
		}
		dst = strconv.AppendUint(dst, uint64(v), 10)
	}
	return dst
}
func (n networkDatum) output(alwaysPrefix bool) string {
	var buffer [64]byte
	dst := buffer[:0]
	if n.family == 4 {
		dst = appendNetworkIPv4(dst, n.address[:4])
	} else {
		var words [8]uint16
		bestStart, bestLength := -1, 0
		for i := range words {
			words[i] = uint16(n.address[2*i])<<8 | uint16(n.address[2*i+1])
		}
		for i := 0; i < 8; {
			if words[i] != 0 {
				i++
				continue
			}
			start := i
			for i < 8 && words[i] == 0 {
				i++
			}
			if i-start > bestLength {
				bestStart, bestLength = start, i-start
			}
		}
		if bestLength < 2 {
			bestStart = -1
		}
		for i := range 8 {
			if bestStart >= 0 && i >= bestStart && i < bestStart+bestLength {
				if i == bestStart {
					dst = append(dst, ':')
				}
				continue
			}
			if i != 0 {
				dst = append(dst, ':')
			}
			// Like REL_18_0 inet_net_ntop_ipv6, dotted tails depend on the winning
			// zero run. :: and ::1 therefore remain ordinary compressed IPv6.
			if i == 6 && bestStart == 0 && (bestLength == 6 || bestLength == 5 && words[5] == 0xffff) {
				dst = appendNetworkIPv4(dst, n.address[12:])
				break
			}
			dst = strconv.AppendUint(dst, uint64(words[i]), 16)
		}
		if bestStart >= 0 && bestStart+bestLength == 8 {
			dst = append(dst, ':')
		}
	}
	maximum := uint8(32)
	if n.family == 6 {
		maximum = 128
	}
	if alwaysPrefix || n.prefix != maximum {
		dst = append(dst, '/')
		dst = strconv.AppendUint(dst, uint64(n.prefix), 10)
	}
	return string(dst)
}
func compareNetwork(a, b networkDatum) int {
	if n := cmp.Compare(a.family, b.family); n != 0 {
		return n
	}
	common := min(a.prefix, b.prefix)
	for i := range common / 8 {
		if n := cmp.Compare(a.address[i], b.address[i]); n != 0 {
			return n
		}
	}
	if bits := common % 8; bits != 0 {
		mask := byte(0xff << (8 - bits))
		if n := cmp.Compare(a.address[common/8]&mask, b.address[common/8]&mask); n != 0 {
			return n
		}
	}
	if n := cmp.Compare(a.prefix, b.prefix); n != 0 {
		return n
	}
	for i := range a.address {
		if n := cmp.Compare(a.address[i], b.address[i]); n != 0 {
			return n
		}
	}
	return 0
}
