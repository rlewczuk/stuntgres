package sqltype

func numericZoneEast(s string) int32 {
	negative := s[0] == '-'
	s = trim(s[1:])
	h, end, _ := timestampIntegerPrefix(s, 2147483647)
	m, sec := int64(0), int64(0)
	if end < len(s) && s[end] == ':' {
		tail := s[end+1:]
		m, end, _ = timestampIntegerPrefix(tail, 2147483647)
		if end < len(tail) && tail[end] == ':' {
			sec, _, _ = timestampIntegerPrefix(tail[end+1:], 2147483647)
		}
	} else if len(s) > 2 {
		m = h % 100
		h /= 100
	}
	east := int32(h*3600 + m*60 + sec)
	if negative {
		east = -east
	}
	return east
}
func (p *timestampDecoder) setNumericZone(s string) timestampDecodeStatus {
	status := numericZone(s)
	if status == timestampDecoded {
		p.zone = zoneSpecification{east: numericZoneEast(s), fixed: true}
	}
	return status
}
