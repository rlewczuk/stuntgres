package dual

import (
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// normalize is a value's canonical text: nil for NULL (never hooked), then
// Options.Normalize, then the defaults keyed on the column type name.
func normalize(o *Options, ref bool, column, typ string, v driver.Value) *string {
	if v == nil {
		return nil
	}
	if o.Normalize != nil {
		if text, ok := o.Normalize(Value{Reference: ref, Column: column, Type: typ, Value: v}); ok {
			return &text
		}
	}
	return canonical(typ, v)
}

// canonical renders a driver value as PostgreSQL-style text, so values that
// different drivers deliver as []byte, string, int64, float64, bool or
// time.Time compare equal when PostgreSQL's text output is equal.
func canonical(typ string, v driver.Value) *string {
	if v == nil {
		return nil
	}
	s := canonicalText(typ, v)
	return &s
}

func canonicalText(typ string, v driver.Value) string {
	switch x := v.(type) {
	case []byte:
		if typ == "BYTEA" {
			return `\x` + hex.EncodeToString(x)
		}
		return canonicalString(typ, string(x))
	case string:
		return canonicalString(typ, x)
	case bool:
		if x {
			return "t"
		}
		return "f"
	case float64:
		return canonicalFloat(typ, x)
	case float32:
		return canonicalFloat(typ, float64(x))
	case time.Time:
		return canonicalTime(typ, x)
	}
	rv := reflect.ValueOf(v)
	switch {
	case rv.CanInt():
		return strconv.FormatInt(rv.Int(), 10)
	case rv.CanUint():
		return strconv.FormatUint(rv.Uint(), 10)
	}
	return fmt.Sprint(v)
}

func canonicalString(typ, s string) string {
	switch typ {
	case "BOOL":
		switch strings.ToLower(s) {
		case "t", "true":
			return "t"
		case "f", "false":
			return "f"
		}
	case "FLOAT4", "FLOAT8":
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return canonicalFloat(typ, f)
		}
	}
	return s
}

func canonicalFloat(typ string, f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case typ == "FLOAT4":
		return strconv.FormatFloat(float64(float32(f)), 'g', -1, 32)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func canonicalTime(typ string, t time.Time) string {
	switch typ {
	case "TIMESTAMPTZ":
		return withEra(t.UTC(), true, "+00")
	case "TIMESTAMP":
		return withEra(t, true, "")
	case "DATE":
		return withEra(t, false, "")
	case "TIME":
		if t.Year() == 0 && t.YearDay() == 2 && clock(t) == "00:00:00" {
			return "24:00:00" // lib/pq's representation of 24:00:00
		}
		return clock(t)
	case "TIMETZ":
		_, offset := t.Zone()
		return clock(t) + zoneOffset(offset)
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// withEra renders a date (and the clock) in t's own location; years <= 0 are
// written as PostgreSQL's BC years after the optional zone suffix.
func withEra(t time.Time, withClock bool, zone string) string {
	year, era := t.Year(), ""
	if year <= 0 {
		year, era = 1-year, " BC"
	}
	s := fmt.Sprintf("%04d-%02d-%02d", year, int(t.Month()), t.Day())
	if withClock {
		s += " " + clock(t) + zone
	}
	return s + era
}

// clock is HH:MM:SS with microseconds when nonzero, trailing zeros trimmed.
func clock(t time.Time) string {
	s := fmt.Sprintf("%02d:%02d:%02d", t.Hour(), t.Minute(), t.Second())
	if us := t.Nanosecond() / 1000; us != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%06d", us), "0")
	}
	return s
}

// zoneOffset is PostgreSQL's ±HH[:MM[:SS]] offset.
func zoneOffset(offset int) string {
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	s := fmt.Sprintf("%s%02d", sign, offset/3600)
	if m, sec := offset%3600/60, offset%60; m != 0 || sec != 0 {
		s += fmt.Sprintf(":%02d", m)
		if sec != 0 {
			s += fmt.Sprintf(":%02d", sec)
		}
	}
	return s
}
