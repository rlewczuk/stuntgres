package sqltype

import (
	"context"
	"stuntgres/internal/sqlstate"
)

// These private immutable headers supply temporal kernels, not catalog identity.
// Public results are materialized JSONB or converted by the SQLJSON destination.
var jsonpathDatetimeTypes = [...]Descriptor{
	{id: Date, name: "date", sql: "DATE"},
	{id: Time, name: "time", sql: "TIME"},
	{id: Timetz, name: "timetz", sql: "TIME WITH TIME ZONE"},
	{id: Timestamp, name: "timestamp", sql: "TIMESTAMP"},
	{id: Timestamptz, name: "timestamptz", sql: "TIMESTAMP WITH TIME ZONE"},
}

func jsonpathDatetimeType(id ID) *Descriptor {
	for i := range jsonpathDatetimeTypes {
		if jsonpathDatetimeTypes[i].id == id {
			return &jsonpathDatetimeTypes[i]
		}
	}
	panic("non-temporal JSONPath type")
}

func jsonpathDatetimeMethod(name string) bool {
	switch name {
	case "datetime", "date", "time", "time_tz", "timestamp", "timestamp_tz":
		return true
	}
	return false
}

func jsonpathDatetimeSeparator(s string) bool {
	return len(s) == 1 && (s[0] == '-' || s[0] == '.' || s[0] == '/' || s[0] == ',' || s[0] == '\'' || s[0] == ';' || s[0] == ':' || s[0] == ' ')
}

func jsonpathDatetimeDescriptor(ctx context.Context, nodes []datetimeNode) (*Descriptor, error) {
	date, clock, zone := false, false, false
	for i, n := range nodes {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if n.key == nil {
			continue
		}
		switch n.key.kind {
		case "HH", "HH12", "HH24", "MI", "SS", "SSSS", "MS", "US", "FF1", "FF2", "FF3", "FF4", "FF5", "FF6", "AM", "PM", "am", "pm", "A_M", "P_M", "a_m", "p_m":
			clock = true
		case "TZ", "tz", "TZH", "TZM", "OF":
			zone = true
		case "FX":
		default:
			date = true
		}
	}
	id := Date
	if clock {
		id = Time
		if zone {
			id = Timetz
		}
		if date {
			id = Timestamp
			if zone {
				id = Timestamptz
			}
		}
	}
	if !date && !clock {
		return nil, datetimeError("22007", "datetime format is not dated and not timed", "", "")
	}
	return jsonpathDatetimeType(id), nil
}

// The SQL formatting compiler owns token parsing even for ISO forms. Compile
// static alternatives once, in PostgreSQL's type-preference order.
var jsonpathDatetimeISOFormats = func() [][]datetimeNode {
	var templates []string
	templates = append(templates, "YYYY-MM-DD")
	for _, zoned := range []bool{true, false} {
		for _, fraction := range []string{".US", ""} {
			base := "HH24:MI:SS" + fraction
			if !zoned {
				templates = append(templates, base)
				continue
			}
			for _, space := range []string{"", " "} {
				for _, zone := range []string{"TZH:TZM", "TZH"} {
					templates = append(templates, base+space+zone)
				}
			}
		}
	}
	for _, zoned := range []bool{true, false} {
		for _, separator := range []string{" ", `"T"`} {
			for _, fraction := range []string{".US", ""} {
				base := "YYYY-MM-DD" + separator + "HH24:MI:SS" + fraction
				if !zoned {
					templates = append(templates, base)
					continue
				}
				for _, space := range []string{"", " "} {
					for _, zone := range []string{"TZH:TZM", "TZH"} {
						templates = append(templates, base+space+zone)
					}
				}
			}
		}
	}
	formats := make([][]datetimeNode, len(templates))
	for i, template := range templates {
		var err error
		formats[i], err = compileDatetime(context.Background(), template, datetimeJSONPathFormat)
		if err != nil {
			panic(err)
		}
	}
	return formats
}()

func (e *jsonpathExecutor) datetimeMethod(n *jsonpathNode, item jsonpathItem) (jsonpathItem, error) {
	if item.itemKind() != jsonbString {
		return jsonpathItem{}, jsonpathStructure("22031", "jsonpath item method ."+n.text+"() can only be applied to a string")
	}
	precision := int32(-1)
	if n.text != "datetime" && n.left != nil {
		var ok bool
		precision, ok = jsonpathDatetimePrecision(n.left.number)
		if !ok {
			return jsonpathItem{}, jsonpathStructure("22031", "time precision of jsonpath item method ."+n.text+"() is out of range for type integer")
		}
	}
	var parsed Value
	var west int32
	var err error
	if n.text == "datetime" && n.left != nil {
		nodes, compileErr := compileDatetime(e.ctx, n.left.text, datetimeJSONPathFormat)
		if compileErr != nil {
			return jsonpathItem{}, compileErr
		}
		parsed, err = datetimeInput(e.ctx, nil, item.text, nodes, e.input, false, true, &west)
		if err != nil {
			if e.ctx.Err() != nil {
				return jsonpathItem{}, e.ctx.Err()
			}
			return jsonpathItem{}, &jsonpathStructuralError{err}
		}
	} else {
		for _, nodes := range jsonpathDatetimeISOFormats {
			parsed, err = datetimeInput(e.ctx, nil, item.text, nodes, e.input, false, true, &west)
			if err == nil {
				break
			}
			if e.ctx.Err() != nil {
				return jsonpathItem{}, e.ctx.Err()
			}
		}
		if err != nil {
			return jsonpathItem{}, jsonpathDatetimeUnrecognized(n.text, item.text)
		}
	}
	result := jsonpathItem{temporal: &parsed, offsetSecondsWest: west, hasOffset: parsed.typ.id == Timestamptz}
	if n.text != "datetime" {
		result, err = e.datetimeConvert(n.text, result, item.text)
		if err != nil {
			return jsonpathItem{}, err
		}
	}
	if n.text != "datetime" && n.left != nil {
		usage, warning, err := result.temporal.typ.ResolveModifiers(precision)
		if err != nil {
			return jsonpathItem{}, err
		}
		v := *result.temporal
		v.integer = roundTimestamp(v.integer, usage.Typmod)
		if v.typ.id == Timestamp || v.typ.id == Timestamptz {
			if v.integer < timestampMinimum || v.integer >= timestampEnd {
				return jsonpathItem{}, jsonpathStructure("22008", "timestamp out of range")
			}
		}
		result.temporal = &v
		if warning != nil && e.notice != nil {
			e.notice(*warning)
		}
	}
	return result, nil
}

func jsonpathDatetimePrecision(n Value) (int32, bool) {
	width := int64(len(n.text)) + n.exponent
	if n.negative || width > 10 {
		return 0, false
	}
	var value int64
	for i := range max(width, 0) {
		value *= 10
		if i < int64(len(n.text)) {
			value += int64(n.text[i] - '0')
		}
	}
	return int32(value), value <= 2147483647
}

func jsonpathDatetimeUnrecognized(method, input string) error {
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22031", Message: method + ` format is not recognized: "` + input + `"`}
	if method == "datetime" {
		d.Hint = "Use a datetime template argument to specify the input data format."
	}
	return &jsonpathStructuralError{d}
}

func jsonpathTimezoneError(source, target ID) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot convert value from " + jsonpathDatetimeType(source).name + " to " + jsonpathDatetimeType(target).name + " without time zone usage", Hint: "Use *_tz() function for time zone support."}
}

func (e *jsonpathExecutor) datetimeConvert(method string, item jsonpathItem, input string) (jsonpathItem, error) {
	v := *item.temporal
	source := v.typ.id
	var target ID
	switch method {
	case "date":
		target = Date
	case "time":
		target = Time
	case "time_tz":
		target = Timetz
	case "timestamp":
		target = Timestamp
	case "timestamp_tz":
		target = Timestamptz
	}
	if source == target {
		return item, nil
	}
	allowed, zoneEdge := false, false
	switch target {
	case Date:
		allowed, zoneEdge = source == Timestamp || source == Timestamptz, source == Timestamptz
	case Time:
		allowed, zoneEdge = source == Timestamp || source == Timestamptz || source == Timetz, source == Timetz || source == Timestamptz
	case Timetz:
		// Zoned timestamp projection is intentionally permitted even by the
		// ordinary executor; unzoned timestamp is not an admitted source.
		allowed, zoneEdge = source == Time || source == Timestamptz, source == Time
	case Timestamp:
		allowed, zoneEdge = source == Date || source == Timestamptz, source == Timestamptz
	case Timestamptz:
		allowed, zoneEdge = source == Date || source == Timestamp, source == Date || source == Timestamp
	}
	if !allowed {
		return jsonpathItem{}, jsonpathDatetimeUnrecognized(method, input)
	}
	if zoneEdge && !e.timezone {
		return jsonpathItem{}, jsonpathTimezoneError(source, target)
	}
	var err error
	v, err = temporalConversion(v, jsonpathDatetimeType(target).Usage(), e.input)
	if err != nil {
		if _, ok := err.(*sqlstate.Error); ok {
			return jsonpathItem{}, &jsonpathStructuralError{err}
		}
		return jsonpathItem{}, err
	}
	item.temporal = &v
	item.hasOffset = target == Timestamptz
	if item.hasOffset {
		east, _ := e.input.Zone.OffsetAt(floorDiv(v.integer, 1000000) + 946684800)
		item.offsetSecondsWest = -east
	}
	return item, nil
}

func (e *jsonpathExecutor) temporalCompare(a, b Value) (int, bool, error) {
	aid, bid := a.typ.id, b.typ.id
	if !e.timezone && aid != bid {
		if dateTimestampType(aid) && dateTimestampType(bid) && (aid == Timestamptz || bid == Timestamptz) {
			source := aid
			if aid == Timestamptz {
				source = bid
			}
			return 0, false, jsonpathTimezoneError(source, Timestamptz)
		}
		if aid == Time && bid == Timetz || aid == Timetz && bid == Time {
			return 0, false, jsonpathTimezoneError(Time, Timetz)
		}
	}
	return compareSQLJSONTemporal(e.ctx, a, b, e.input)
}

func jsonpathTemporalText(item jsonpathItem) string {
	v := *item.temporal
	if v.typ.id == Timestamptz && item.hasOffset {
		// Render the stored civil offset with the existing timestamp writer,
		// without allocating a synthetic Zone or changing typed PASSING output.
		v.integer -= int64(item.offsetSecondsWest) * 1000000
		v.typ = jsonpathDatetimeType(Timestamp)
		text := jsonCreateDateTime(v, OutputEnvironment{})
		if len(text) >= 3 && text[len(text)-3:] == " BC" {
			return jsonpathTemporalOffset(text[:len(text)-3], item.offsetSecondsWest) + " BC"
		}
		return jsonpathTemporalOffset(text, item.offsetSecondsWest)
	}
	return sqlJSONTemporalJSON(v)
}
