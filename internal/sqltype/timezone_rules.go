package sqltype

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed tzdata/zones.zip
var zoneArchive []byte

//go:embed tzdata/Default
var defaultZoneAbbreviations string

type zoneType struct {
	east                    int32
	daylight                bool
	abbreviation            string
	standardTime, universal bool
}
type zoneTransition struct {
	second int64
	kind   uint8
}
type zoneAbbreviation struct {
	east     int32
	daylight bool
	owner    string
}

// Zone is an immutable rule handle. All seconds are integer Unix seconds;
// callers retain their microsecond remainder separately.
type Zone struct {
	name            string
	types           []zoneType
	transitions     []zoneTransition
	defaultType     int
	recurrence      *posixZone
	goBack, goAhead bool
	fixed           bool
}

func (z *Zone) Name() string {
	if z == nil {
		return "UTC"
	}
	return z.name
}

var zones struct {
	once          sync.Once
	archive       *zip.Reader
	members       map[string]*zip.File
	cache         sync.Map
	abbreviations map[string]zoneAbbreviation
}

func initializeZones() {
	r, err := zip.NewReader(bytes.NewReader(zoneArchive), int64(len(zoneArchive)))
	if err != nil {
		panic(err)
	}
	zones.archive = r
	zones.members = make(map[string]*zip.File, len(r.File))
	for _, f := range r.File {
		zones.members[strings.ToLower(f.Name)] = f
	}
	zones.abbreviations = make(map[string]zoneAbbreviation)
	for _, line := range strings.Split(defaultZoneAbbreviations, "\n") {
		if p := strings.IndexByte(line, '#'); p >= 0 {
			line = line[:p]
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			entry := zoneAbbreviation{}
			if east, err := strconv.ParseInt(fields[1], 10, 32); err == nil {
				entry.east = int32(east)
				entry.daylight = len(fields) > 2 && fields[2] == "D"
			} else {
				entry.owner = fields[1]
			}
			zones.abbreviations[strings.ToLower(fields[0])] = entry
		}
	}
}
func FixedZone(name string, east int32) *Zone {
	return &Zone{name: name, types: []zoneType{{east: east, abbreviation: name}}, fixed: true}
}
func ResolveZone(name string) (*Zone, error) {
	if len(name) > 255 {
		return nil, failure("22023", fmt.Sprintf("time zone \"%s\" not recognized", name))
	}
	zones.once.Do(initializeZones)
	if cached, ok := zones.cache.Load(name); ok {
		return cached.(*Zone), nil
	}
	key := strings.ToLower(name)
	if cached, ok := zones.cache.Load(key); ok {
		if key != name {
			zones.cache.Store(name, cached)
		}
		return cached.(*Zone), nil
	}
	var z *Zone
	if f := zones.members[strings.TrimPrefix(key, ":")]; f != nil {
		r, err := f.Open()
		if err != nil {
			panic(err)
		}
		b, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil {
			panic(err)
		}
		if closeErr != nil {
			panic(closeErr)
		}
		z, err = parseTZif(f.Name, b)
		if err != nil {
			panic(err)
		}
	} else {
		canonical := timezoneASCIIUpper(name)
		p, ok := parsePOSIX(canonical)
		if !ok || strings.HasPrefix(name, ":") {
			return nil, failure("22023", fmt.Sprintf("time zone \"%s\" not recognized", name))
		}
		z = posixRulesZone(canonical, p)
	}
	actual, _ := zones.cache.LoadOrStore(key, z)
	if key != name {
		zones.cache.Store(name, actual)
	}
	return actual.(*Zone), nil
}
func parseTZif(name string, b []byte) (*Zone, error) {
	fail := func() (*Zone, error) { return nil, fmt.Errorf("corrupt embedded TZif %s", name) }
	if len(b) < 44 || string(b[:4]) != "TZif" {
		return fail()
	}
	width := 4
	modern := b[4] != '\x00'
	if modern {
		counts := tzCounts(b)
		skip := 44 + counts[3]*5 + counts[4]*6 + counts[5] + counts[2]*8 + counts[1] + counts[0]
		if skip+44 > len(b) {
			return fail()
		}
		b = b[skip:]
		width = 8
		if string(b[:4]) != "TZif" {
			return fail()
		}
	}
	c := tzCounts(b)
	size := 44 + c[3]*(width+1) + c[4]*6 + c[5] + c[2]*(width+4) + c[1] + c[0]
	if c[4] < 1 || c[4] > 256 || c[3] > 2000 || size > len(b) || (c[0] != 0 && c[0] != c[4]) || (c[1] != 0 && c[1] != c[4]) {
		return fail()
	}
	z := &Zone{name: name, types: make([]zoneType, c[4]), transitions: make([]zoneTransition, c[3])}
	p := 44
	for i := range z.transitions {
		if width == 8 {
			z.transitions[i].second = int64(binary.BigEndian.Uint64(b[p : p+8]))
		} else {
			z.transitions[i].second = int64(int32(binary.BigEndian.Uint32(b[p : p+4])))
		}
		p += width
		if i > 0 && z.transitions[i].second < z.transitions[i-1].second {
			return fail()
		}
	}
	for i := range z.transitions {
		z.transitions[i].kind = b[p]
		p++
		if int(z.transitions[i].kind) >= c[4] {
			return fail()
		}
	}
	chars := b[p+c[4]*6 : p+c[4]*6+c[5]]
	for i := range z.types {
		idx := int(b[p+5])
		if idx >= len(chars) {
			return fail()
		}
		if b[p+4] > 1 {
			return fail()
		}
		end := bytes.IndexByte(chars[idx:], 0)
		if end < 0 {
			return fail()
		}
		z.types[i] = zoneType{east: int32(binary.BigEndian.Uint32(b[p : p+4])), daylight: b[p+4] != 0, abbreviation: string(chars[idx : idx+end])}
		p += 6
	}
	flags := 44 + c[3]*(width+1) + c[4]*6 + c[5] + c[2]*(width+4)
	for i := range z.types {
		if c[1] > 0 {
			if b[flags+i] > 1 {
				return fail()
			}
			z.types[i].standardTime = b[flags+i] != 0
		}
		if c[0] > 0 {
			if b[flags+c[1]+i] > 1 {
				return fail()
			}
			z.types[i].universal = b[flags+c[1]+i] != 0
		}
	}
	usedZero := false
	for _, tr := range z.transitions {
		usedZero = usedZero || tr.kind == 0
	}
	if usedZero {
		candidate := -1
		if len(z.transitions) > 0 && z.types[z.transitions[0].kind].daylight {
			for i := int(z.transitions[0].kind) - 1; i >= 0; i-- {
				if !z.types[i].daylight {
					candidate = i
					break
				}
			}
		}
		if candidate < 0 {
			for i, t := range z.types {
				if !t.daylight {
					candidate = i
					break
				}
			}
		}
		if candidate >= 0 {
			z.defaultType = candidate
		}
	}
	footer := ""
	if modern && len(b[size:]) > 2 && b[size] == '\n' && b[len(b)-1] == '\n' {
		footer = string(b[size+1 : len(b)-1])
	}
	if len(z.types)+2 <= 256 && footer != "" {
		if rule, ok := parsePOSIX(footer); ok {
			charCount := len(chars)
			available := string(chars)
			allowed := true
			extension := posixRulesZone(name, rule)
			kinds := extension.types
			for _, t := range kinds {
				abbreviation := t.abbreviation + "\x00"
				if !strings.Contains(available, abbreviation) {
					if charCount+len(t.abbreviation) >= 50 {
						allowed = false
					} else {
						available += abbreviation
						charCount += len(abbreviation)
					}
				}
			}
			if allowed {
				z.recurrence = rule
				whileLast := len(z.transitions)
				for whileLast > 1 && z.transitions[whileLast-1].kind == z.transitions[whileLast-2].kind {
					whileLast--
				}
				z.transitions = z.transitions[:whileLast]
				base := len(z.types)
				for _, tr := range extension.transitions {
					if len(z.transitions) >= zoneMaxTransitions {
						break
					}
					if len(z.transitions) > 0 && tr.second <= z.transitions[len(z.transitions)-1].second {
						continue
					}
					tr.kind += uint8(base)
					z.transitions = append(z.transitions, tr)
				}
				z.types = append(z.types, kinds...)
			}
		}
	}
	z.findRepeats()
	z.determineFixed()
	return z, nil
}
func (z *Zone) determineFixed() {
	z.fixed = true
	for _, kind := range z.types {
		if kind.east != z.types[0].east {
			z.fixed = false
			break
		}
	}
}
func (z *Zone) isFixed() bool { return z == nil || z.fixed }
func tzCounts(b []byte) [6]int {
	var c [6]int
	for i := range c {
		c[i] = int(binary.BigEndian.Uint32(b[20+i*4 : 24+i*4]))
	}
	return c
}
func (z *Zone) typeAt(second int64) zoneType {
	if z == nil {
		return zoneType{abbreviation: "UTC"}
	}
	second, _ = z.repeatedSecond(second)
	i := sort.Search(len(z.transitions), func(i int) bool { return z.transitions[i].second > second }) - 1
	if i < 0 {
		return z.types[z.defaultType]
	}
	return z.types[z.transitions[i].kind]
}
func (z *Zone) OffsetAt(second int64) (int32, string) {
	t := z.typeAt(second)
	return t.east, t.abbreviation
}

// CivilOffset preserves PostgreSQL's pre-transition choice for gaps and
// post-transition choice for folds, rather than Go's time.Date policy.
func (z *Zone) CivilOffset(localSecond int64) int32 {
	if z == nil {
		return 0
	}
	if z.fixed {
		return z.types[0].east
	}
	probe := localSecond - 86400
	before := z.typeAt(probe)
	boundary, after, ok := z.nextBoundary(probe)
	if !ok {
		return before.east
	}
	beforeTime := localSecond - int64(before.east)
	afterTime := localSecond - int64(after.east)
	if beforeTime < boundary && afterTime < boundary {
		return before.east
	}
	if beforeTime >= boundary && afterTime >= boundary {
		return after.east
	}
	if beforeTime > afterTime {
		return before.east
	}
	return after.east
}
func (z *Zone) nextBoundary(second int64) (int64, zoneType, bool) {
	mapped, shift := z.repeatedSecond(second)
	i := sort.Search(len(z.transitions), func(i int) bool { return z.transitions[i].second > mapped })
	if i == len(z.transitions) && z.goAhead {
		mapped -= zoneRepeatSeconds
		shift -= zoneRepeatSeconds
		i = sort.Search(len(z.transitions), func(i int) bool { return z.transitions[i].second > mapped })
	}
	if i < len(z.transitions) {
		tr := z.transitions[i]
		return tr.second - shift, z.types[tr.kind], true
	}
	return 0, zoneType{}, false
}

// PostgreSQL's C-locale provider folds ASCII, not Unicode abbreviations.
func timezoneASCIIUpper(name string) string {
	changed := false
	for i := range name {
		if name[i] >= 'a' && name[i] <= 'z' {
			changed = true
			break
		}
	}
	if !changed {
		return name
	}
	var result strings.Builder
	result.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		result.WriteByte(c)
	}
	return result.String()
}
