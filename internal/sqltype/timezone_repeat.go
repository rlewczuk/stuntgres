package sqltype

// PostgreSQL REL_18_0 localtime.c generates bounded POSIX transitions, then
// repeats a verified Gregorian 400-year cycle outside that bounded index.
const zoneRepeatSeconds int64 = 12622780800
const zoneMaxTransitions = 2000

func posixRulesZone(name string, p *posixZone) *Zone {
	z := &Zone{name: name, types: []zoneType{p.standard}, recurrence: p}
	if !p.hasDST {
		z.determineFixed()
		return z
	}
	z.types = append(z.types, p.daylight)
	yearLimit := 2171
	for year := 1770; year < yearLimit && len(z.transitions)+2 <= zoneMaxTransitions; year++ {
		start := p.start.transition(year, p.standard.east)
		end := p.end.transition(year, p.daylight.east)
		reversed := end < start
		if reversed {
			start, end = end, start
		}
		yearSeconds := (civilDay(year+1, 1, 1) - civilDay(year, 1, 1)) * 86400
		if !reversed && !(start < end && end-start < yearSeconds+int64(p.daylight.east-p.standard.east)) {
			continue
		}
		first, last := uint8(1), uint8(0)
		if reversed {
			first, last = 0, 1
		}
		z.transitions = append(z.transitions, zoneTransition{start, first}, zoneTransition{end, last})
		yearLimit = year + 401
	}
	if len(z.transitions) == 0 {
		z.types = []zoneType{p.daylight}
		z.defaultType = 0
	}
	z.findRepeats()
	z.determineFixed()
	return z
}
func (z *Zone) findRepeats() {
	if len(z.transitions) < 2 {
		return
	}
	first, last := z.transitions[0], z.transitions[len(z.transitions)-1]
	for _, tr := range z.transitions[1:] {
		if tr.second-first.second == zoneRepeatSeconds && z.types[tr.kind] == z.types[first.kind] {
			z.goBack = true
			break
		}
	}
	for i := len(z.transitions) - 2; i >= 0; i-- {
		tr := z.transitions[i]
		if last.second-tr.second == zoneRepeatSeconds && z.types[tr.kind] == z.types[last.kind] {
			z.goAhead = true
			break
		}
	}
}
func (z *Zone) repeatedSecond(second int64) (int64, int64) {
	if len(z.transitions) == 0 {
		return second, 0
	}
	first, last := z.transitions[0].second, z.transitions[len(z.transitions)-1].second
	shift := int64(0)
	if second < first && z.goBack {
		shift = ((first-second-1)/zoneRepeatSeconds + 1) * zoneRepeatSeconds
	}
	if second > last && z.goAhead {
		shift = -((second-last-1)/zoneRepeatSeconds + 1) * zoneRepeatSeconds
	}
	return second + shift, shift
}
