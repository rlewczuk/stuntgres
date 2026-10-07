package sqltype

type posixRule struct {
	kind                      byte
	day, month, week, weekday int
	seconds                   int64
}
type posixZone struct {
	standard, daylight zoneType
	hasDST             bool
	start, end         posixRule
}
type posixParser struct {
	text string
	at   int
}

func (p *posixParser) name() (string, bool) {
	start := p.at
	if p.at < len(p.text) && p.text[p.at] == '<' {
		p.at++
		start = p.at
		for p.at < len(p.text) && p.text[p.at] != '>' {
			p.at++
		}
		if p.at == len(p.text) {
			return "", false
		}
		s := p.text[start:p.at]
		p.at++
		return s, true
	}
	for p.at < len(p.text) && !asciiDigit(p.text[p.at]) && p.text[p.at] != ',' && p.text[p.at] != '-' && p.text[p.at] != '+' {
		p.at++
	}
	return p.text[start:p.at], true
}
func (p *posixParser) number(max int) (int, bool) {
	start, n := p.at, 0
	for p.at < len(p.text) && asciiDigit(p.text[p.at]) {
		n = n*10 + int(p.text[p.at]-'0')
		if n > max {
			return 0, false
		}
		p.at++
	}
	return n, p.at > start
}
func (p *posixParser) offset() (int64, bool) {
	sign := int64(1)
	if p.at < len(p.text) && (p.text[p.at] == '-' || p.text[p.at] == '+') {
		if p.text[p.at] == '-' {
			sign = -1
		}
		p.at++
	}
	hour, ok := p.number(167)
	if !ok {
		return 0, false
	}
	minute, second := 0, 0
	if p.at < len(p.text) && p.text[p.at] == ':' {
		p.at++
		minute, ok = p.number(59)
		if !ok {
			return 0, false
		}
		if p.at < len(p.text) && p.text[p.at] == ':' {
			p.at++
			second, ok = p.number(60)
			if !ok {
				return 0, false
			}
		}
	}
	return sign * int64(hour*3600+minute*60+second), true
}
func (p *posixParser) rule() (posixRule, bool) {
	r := posixRule{seconds: 7200}
	if p.at == len(p.text) {
		return r, false
	}
	r.kind = p.text[p.at]
	var ok bool
	if r.kind == 'M' {
		p.at++
		r.month, ok = p.number(12)
		if !ok || r.month == 0 || !p.take('.') {
			return r, false
		}
		r.week, ok = p.number(5)
		if !ok || r.week == 0 || !p.take('.') {
			return r, false
		}
		r.weekday, ok = p.number(6)
		if !ok {
			return r, false
		}
	} else {
		max := 365
		if r.kind == 'J' {
			p.at++
		} else {
			r.kind = 'N'
		}
		r.day, ok = p.number(max)
		if !ok || r.kind == 'J' && r.day == 0 {
			return r, false
		}
	}
	if p.take('/') {
		r.seconds, ok = p.offset()
		if !ok {
			return r, false
		}
	}
	return r, true
}
func (p *posixParser) take(c byte) bool {
	if p.at < len(p.text) && p.text[p.at] == c {
		p.at++
		return true
	}
	return false
}
func parsePOSIX(text string) (*posixZone, bool) {
	p := posixParser{text: text}
	standard, ok := p.name()
	if !ok {
		return nil, false
	}
	west, ok := p.offset()
	if !ok {
		return nil, false
	}
	z := &posixZone{standard: zoneType{east: int32(-west), abbreviation: standard}}
	if p.at == len(text) {
		return z, true
	}
	daylight, ok := p.name()
	if !ok || daylight == "" {
		return nil, false
	}
	z.hasDST = true
	z.daylight = zoneType{east: int32(-west + 3600), daylight: true, abbreviation: daylight}
	if p.at < len(text) && text[p.at] != ',' && text[p.at] != ';' {
		west, ok = p.offset()
		if !ok {
			return nil, false
		}
		z.daylight.east = int32(-west)
	}
	if p.at == len(text) {
		z.start = posixRule{kind: 'M', month: 3, week: 2, weekday: 0, seconds: 7200}
		z.end = posixRule{kind: 'M', month: 11, week: 1, weekday: 0, seconds: 7200}
		return z, true
	}
	if !p.take(',') && !p.take(';') {
		return nil, false
	}
	z.start, ok = p.rule()
	if !ok || !p.take(',') {
		return nil, false
	}
	z.end, ok = p.rule()
	return z, ok && p.at == len(text)
}

const unixEpochCivilDay int64 = -10957

func (r posixRule) transition(year int, east int32) int64 {
	day := civilDay(year, 1, 1)
	switch r.kind {
	case 'M':
		first := civilDay(year, r.month, 1)
		weekday := int((first - unixEpochCivilDay + 4) % 7)
		if weekday < 0 {
			weekday += 7
		}
		d := 1 + (r.weekday-weekday+7)%7 + (r.week-1)*7
		last := civilDay(year, r.month+1, 1) - first
		if int64(d) > last {
			d -= 7
		}
		day = first + int64(d-1)
	case 'J':
		day += int64(r.day - 1)
		if r.day >= 60 && (year%4 == 0 && (year%100 != 0 || year%400 == 0)) {
			day++
		}
	default:
		day += int64(r.day)
	}
	return (day-unixEpochCivilDay)*86400 + r.seconds - int64(east)
}
