package dual

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
)

type errClass int

const (
	classNone     errClass = iota
	classSQL               // SQLSTATE plus message (and optional details)
	classCanceled          // context cancellation or deadline
	classOrdinary          // any other error: compared by presence only
)

type errInfo struct {
	class                                 errClass
	code, message, detail, hint, position string
}

// classify reads an error's identity. SQLSTATE comes from the first error in
// the chain with SQLState(); its message from an exported Message string
// field (stuntgres, lib/pq, pgconn), else from Error() without the driver's
// prefix and code suffix.
func classify(err error, o *Options) errInfo {
	if err == nil {
		return errInfo{}
	}
	if o.ErrorFields != nil {
		if code, message, ok := o.ErrorFields(err); ok && code != "" {
			return errInfo{class: classSQL, code: code, message: message}
		}
	}
	if isCanceled(err) {
		return errInfo{class: classCanceled, message: err.Error()}
	}
	var s interface{ SQLState() string }
	if errors.As(err, &s) {
		if code := s.SQLState(); code != "" {
			info := errInfo{class: classSQL, code: code, message: sqlMessage(s, code)}
			info.detail, _ = stringField(s, "Detail")
			info.hint, _ = stringField(s, "Hint")
			info.position = positionField(s)
			return info
		}
	}
	return errInfo{class: classOrdinary, message: err.Error()}
}

func sqlMessage(s any, code string) string {
	if m, ok := stringField(s, "Message"); ok {
		return m
	}
	e, ok := s.(error)
	if !ok {
		return ""
	}
	return stripMessage(e.Error(), code)
}

// stripMessage removes a known driver framing: "stuntgres: " (this also covers
// a decorated stuntgres diagnostic, whose Error is the decoration's),
// "pq: …(CODE)" and pgx's "SEVERITY: …(SQLSTATE CODE)".
func stripMessage(text, code string) string {
	if rest, ok := strings.CutPrefix(text, "stuntgres: "); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(text, "pq: "); ok {
		if body, ok := strings.CutSuffix(rest, " ("+code+")"); ok {
			return body
		}
	}
	if body, ok := strings.CutSuffix(text, " (SQLSTATE "+code+")"); ok {
		if severity, message, ok := strings.Cut(body, ": "); ok && isSeverity(severity) {
			return message
		}
	}
	return text
}

func isSeverity(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// structField finds an exported field of the struct behind v.
func structField(v any, name string) (reflect.Value, bool) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return reflect.Value{}, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	sf, ok := rv.Type().FieldByName(name)
	if !ok || !sf.IsExported() {
		return reflect.Value{}, false
	}
	f, err := rv.FieldByIndexErr(sf.Index)
	if err != nil {
		return reflect.Value{}, false
	}
	return f, true
}

func stringField(v any, name string) (string, bool) {
	f, ok := structField(v, name)
	if !ok || f.Kind() != reflect.String {
		return "", false
	}
	return f.String(), true
}

// positionField renders Position: a string as is, an integer as decimal with
// 0 meaning absent.
func positionField(v any) string {
	f, ok := structField(v, "Position")
	if !ok {
		return ""
	}
	switch {
	case f.Kind() == reflect.String:
		return f.String()
	case f.CanInt():
		if n := f.Int(); n != 0 {
			return strconv.FormatInt(n, 10)
		}
	case f.CanUint():
		if n := f.Uint(); n != 0 {
			return strconv.FormatUint(n, 10)
		}
	}
	return ""
}

// text is the record's errmsg for the error.
func (i errInfo) text() string { return i.message }

// shown is the error in a diff line.
func (i errInfo) shown() string {
	switch i.class {
	case classNone:
		return "none"
	case classSQL:
		return shown(i.code + " " + i.message)
	case classCanceled:
		return "canceled"
	}
	return shown("error " + i.message)
}

func sameError(a, b errInfo) bool {
	if a.class != b.class {
		return false
	}
	return a.class != classSQL || (a.code == b.code && a.message == b.message)
}

// compareErrors adds the error line, or the detail lines of equal SQL errors.
func compareErrors(d *diffLines, n, r errInfo, details bool) {
	if !sameError(n, r) {
		d.add("error: stuntgres " + n.shown() + ", reference " + r.shown())
		return
	}
	if !details || n.class != classSQL {
		return
	}
	for _, f := range [...]struct{ name, native, ref string }{
		{"detail", n.detail, r.detail},
		{"hint", n.hint, r.hint},
		{"position", n.position, r.position},
	} {
		if f.native != f.ref {
			d.add("error " + f.name + ": stuntgres " + jsonValue(optional(f.native)) + ", reference " + jsonValue(optional(f.ref)))
		}
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
