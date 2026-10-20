package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestFormatEncodingNormalizers(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"utf-8", "UTF8"}, {"u.t_f-8", "UTF8"}, {"UNICODE", "UNICODE"}, {"unicode", "UTF8"}} {
		got, notice, err := normalizeClientEncoding(tc.input)
		if err != nil || notice != nil || got != tc.want {
			t.Fatal(tc, got, notice, err)
		}
	}
	for _, tc := range []struct{ input, code, message, detail string }{
		{"not_an_encoding", "22023", `invalid value for parameter "client_encoding": "not_an_encoding"`, ""},
		{strings.Repeat("-", 80) + "UTF8", "22023", `invalid value for parameter "client_encoding": "` + strings.Repeat("-", 63) + `"`, ""},
		{"MULE_INTERNAL", "0A000", `invalid value for parameter "client_encoding": "MULE_INTERNAL"`, "Conversion between MULE_INTERNAL and UTF8 is not supported."},
		{"latin1", "0A000", `client encoding "LATIN1" is not supported by stuntgres`, "Only UTF8 client encoding is supported."},
		{"SQL_ASCII", "0A000", `client encoding "SQL_ASCII" is not supported by stuntgres`, "Only UTF8 client encoding is supported."},
	} {
		_, notice, err := normalizeClientEncoding(tc.input)
		var d *sqlstate.Error
		if notice != nil || !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Detail != tc.detail || d.Position != "" {
			t.Fatal(tc, notice, err)
		}
	}
	for _, name := range []string{"lc_messages", "lc_monetary", "lc_numeric", "lc_time"} {
		for _, value := range []string{"C", "POSIX", ""} {
			got, notice, err := normalizeLocale(name, value)
			if name == "lc_messages" && value == "" {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "22023" || d.Detail != "" || notice != nil {
					t.Fatal(name, value, notice, err)
				}
			} else if err != nil || notice != nil || got != value {
				t.Fatal(name, value, got, notice, err)
			}
		}
		for _, value := range []string{"c", "C.UTF-8", "missing", "é🙂"} {
			_, notice, err := normalizeLocale(name, value)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22023" || d.Message != fmt.Sprintf(`invalid value for parameter "%s": "%s"`, name, value) || d.Detail != "Only C and POSIX locales are supported by stuntgres." {
				t.Fatal(name, value, notice, err)
			}
			if value == "é🙂" {
				if notice == nil || notice.Severity != "WARNING" || notice.Code != "22023" || notice.Message != `locale name "é🙂" contains non-ASCII characters` {
					t.Fatal(notice)
				}
			} else if notice != nil {
				t.Fatal(notice)
			}
		}
	}
	for _, value := range []string{"info", "disabled", "debug5", "debug4", "debug3", "debug2", "debug1", "log", "notice", "warning", "error"} {
		got, notice, err := normalizeICUValidationLevel(strings.ToUpper(value))
		if err != nil || notice != nil || got != value {
			t.Fatal(value, got, notice, err)
		}
	}
	got, notice, err := normalizeICUValidationLevel("debug")
	if err != nil || notice != nil || got != "debug2" {
		t.Fatal(got, notice, err)
	}
	_, _, err = normalizeICUValidationLevel("bogus")
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22023" || d.Hint != "Available values: disabled, debug5, debug4, debug3, debug2, debug1, log, notice, warning, error." {
		t.Fatal(err)
	}
}

func TestFormatEncodingCapturedDefaults(t *testing.T) {
	e := New()
	ctx := context.Background()
	values := map[string]string{"client_encoding": "utf-8", "lc_messages": "POSIX", "lc_monetary": "", "lc_numeric": "POSIX", "lc_time": "C", "icu_validation_level": "DEBUG", "extra_float_digits": "-1"}
	provider := &settingsDefaultProviderForTest{values: values, calls: map[string]int{}}
	settings := sessionSettings{defaults: make(map[string]string), values: make(map[string]string)}
	if err := settings.initialize(ctx, e.settings.Definitions(), e.settings.Order(), provider); err != nil {
		t.Fatal(err)
	}
	for _, name := range e.settings.Order() {
		if provider.calls[name] != 1 {
			t.Fatal("default not captured exactly once", name, provider.calls)
		}
	}
	for name, want := range map[string]string{"client_encoding": "UTF8", "lc_messages": "POSIX", "lc_monetary": "", "lc_numeric": "POSIX", "lc_time": "C", "icu_validation_level": "debug2", "extra_float_digits": "-1"} {
		if settings.value(name) != want {
			t.Fatal(name, settings.value(name), want)
		}
		if localeSetting(name) && settings.defaultTyped[name].Locale == (sqltype.Locale{}) {
			t.Fatal("locale default not typed", name)
		}
	}
	values["lc_numeric"] = "C.UTF-8"
	values["extra_float_digits"] = "3"
	if settings.value("lc_numeric") != "POSIX" || settings.typedValue("extra_float_digits").Number != -1 {
		t.Fatal("live provider leaked into captured defaults")
	}
	for _, bad := range []struct{ name, value string }{{"lc_numeric", "C.UTF-8"}, {"lc_messages", ""}, {"client_encoding", "LATIN1"}, {"icu_validation_level", "bogus"}, {"extra_float_digits", "4"}} {
		owner := sessionSettings{defaults: make(map[string]string), values: make(map[string]string)}
		p := &settingsDefaultProviderForTest{values: map[string]string{bad.name: bad.value}, calls: map[string]int{}}
		if err := owner.initialize(ctx, e.settings.Definitions(), e.settings.Order(), p); err == nil {
			t.Fatal("invalid physical default accepted", bad)
		}
	}
	float, err := e.settings.Lookup("extra_float_digits")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"-15", "0", "1", "3"} {
		value, err := float.Parse(input)
		if err != nil || float.Format(value) != input {
			t.Fatal(input, value, err)
		}
	}
	for _, input := range []string{"-16", "4"} {
		_, err := float.Parse(input)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22023" {
			t.Fatal(input, err)
		}
	}
	messages, _ := e.settings.Lookup("lc_messages")
	stats, _ := e.settings.Lookup("log_statement_stats")
	if messages.Access != SettingPrivileged || stats.Access != SettingPrivileged {
		t.Fatal("privileged classification changed")
	}
	for _, name := range []string{"client_encoding", "lc_monetary", "lc_numeric", "lc_time", "icu_validation_level", "extra_float_digits"} {
		d, err := e.settings.Lookup(name)
		if err != nil || d.Access != SettingSession {
			t.Fatal(name, d, err)
		}
	}
}

func TestFormatEncodingCurrentRoleAndResetAll(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.executeRoleAttributes(scope, &parser.CreateRole{Name: parser.Identifier{Text: "fe_unit_reader"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}, 0)
	if err := scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	messages, _ := e.settings.Lookup("lc_messages")
	stats, _ := e.settings.Lookup("log_statement_stats")
	if err := settingAccess(messages, op); err != nil {
		t.Fatal("real bootstrap superuser denied", err)
	}
	for _, definition := range []SettingDefinition{stats, {Name: "demo.privileged", Access: SettingPrivileged}} {
		var d *sqlstate.Error
		if err := settingAccess(definition, op); !errors.As(err, &d) || d.Code != "42501" {
			t.Fatal("unrelated privileged setting relaxed", definition, err)
		}
	}
	if _, err := e.executeSetting(op, &parser.Set{Name: parser.Identifier{Text: "lc_messages"}, Values: []string{"POSIX"}}, 0, false); err != nil {
		t.Fatal(err)
	}
	reader, ok := e.Roles().Lookup("fe_unit_reader")
	if !ok {
		t.Fatal("real ordinary role missing")
	}
	op.session.mu.Lock()
	op.session.authorization.state.CurrentUser = reader.Ref
	op.session.mu.Unlock()
	if op.RoleState().SessionUser == reader.Ref {
		t.Fatal("fixture lost superuser session identity")
	}
	var diagnostic *sqlstate.Error
	if err := settingAccess(messages, op); !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != `permission denied to set parameter "lc_messages"` {
		t.Fatal("current ordinary role admitted", err)
	}
	if op.session.settings.value("lc_messages") != "POSIX" {
		t.Fatal("access check mutated setting")
	}
	if _, err := e.executeSetting(op, &parser.Reset{All: true}, 0, false); err != nil {
		t.Fatal("all-reset incorrectly reused individual permission", err)
	}
	if op.session.settings.value("lc_messages") != "C" {
		t.Fatal("all-reset retained privileged override")
	}
}

func TestFormatEncodingCanonicalDeparse(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	ctx := context.Background()
	for _, tc := range []struct {
		id          sqltype.ID
		input, want string
	}{{sqltype.Float4, "1.23456789", "'1.2345679'::real"}, {sqltype.Float8, "1.2345678901234567", "'1.2345678901234567'::double precision"}} {
		typ, ok := view.LookupType(tc.id)
		if !ok {
			t.Fatal(tc.id)
		}
		value, err := typ.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		node := literalExpression{Info: expr.Info{Usage: typ.Usage()}, value: value}
		for _, precision := range []int32{0, -1, -15} {
			renderer := pgRenderer{view: view, output: sqltype.OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: precision}}
			got, err := renderer.literal(ctx, node)
			if err != nil || got != tc.want {
				t.Fatal("rounded precision leaked into persistent scalar source", precision, got, err)
			}
			array, err := sqltype.Array(45003, "fe_literal_array", typ)
			if err != nil {
				t.Fatal(err)
			}
			values, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{value})
			if err != nil {
				t.Fatal(err)
			}
			node := literalExpression{Info: expr.Info{Usage: array.Usage()}, value: values}
			want := "'{1.2345679}'::real[]"
			if tc.id == sqltype.Float8 {
				want = "'{1.2345678901234567}'::double precision[]"
			}
			got, err = renderer.literal(ctx, node)
			if err != nil || got != want {
				t.Fatal("rounded precision leaked into persistent array source", precision, got, err)
			}
		}
	}
}

func TestFormatEncodingValidationNotices(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, threshold := range []noticeLevel{levelWarning, levelError} {
		op.noticeThreshold = threshold
		_, err := e.executeSetting(op, &parser.Set{Name: parser.Identifier{Text: "lc_time"}, Local: true, Values: []string{"é🙂"}}, 0, true)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" {
			t.Fatal(err)
		}
		notices := op.DetachNotices()
		if threshold == levelError {
			if len(notices) != 0 {
				t.Fatal("filtered validation warnings delivered", notices)
			}
		} else if len(notices) != 2 || notices[0].Code != "25P01" || notices[0].Message != "SET LOCAL can only be used in transaction blocks" || notices[1].Code != "22023" || notices[1].Severity != "WARNING" || notices[1].Message != `locale name "é🙂" contains non-ASCII characters` {
			t.Fatal("failed validation lost or reordered actual warnings", notices)
		}
		if op.session.settings.value("lc_time") != "C" {
			t.Fatal("failed locale validation published candidate")
		}
		if len(op.DetachNotices()) != 0 {
			t.Fatal("notice transfer duplicated records")
		}
	}
}
