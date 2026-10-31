package types_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFormatEncodingCapturedDefaults(t *testing.T) {
	e := engine.New()
	ctx := context.Background()
	values := map[string]string{"client_encoding": "utf-8", "lc_messages": "POSIX", "lc_monetary": "", "lc_numeric": "POSIX", "lc_time": "C", "icu_validation_level": "DEBUG", "extra_float_digits": "-1"}
	provider := &settingsDefaultProviderForTest{values: values, calls: map[string]int{}}
	store := session.SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := store.Initialize(ctx, e.Settings.Definitions(), e.Settings.Order(), provider); err != nil {
		t.Fatal(err)
	}
	for _, name := range e.Settings.Order() {
		if provider.calls[name] != 1 {
			t.Fatal("default not captured exactly once", name, provider.calls)
		}
	}
	for name, want := range map[string]string{"client_encoding": "UTF8", "lc_messages": "POSIX", "lc_monetary": "", "lc_numeric": "POSIX", "lc_time": "C", "icu_validation_level": "debug2", "extra_float_digits": "-1"} {
		if store.Value(name) != want {
			t.Fatal(name, store.Value(name), want)
		}
		if settings.IsLocaleSetting(name) && store.DefaultTyped[name].Locale == (sqltype.Locale{}) {
			t.Fatal("locale default not typed", name)
		}
	}
	values["lc_numeric"] = "C.UTF-8"
	values["extra_float_digits"] = "3"
	if store.Value("lc_numeric") != "POSIX" || store.TypedValue("extra_float_digits").Number != -1 {
		t.Fatal("live provider leaked into captured defaults")
	}
	for _, bad := range []struct{ name, value string }{{"lc_numeric", "C.UTF-8"}, {"lc_messages", ""}, {"client_encoding", "not-an-encoding"}, {"icu_validation_level", "bogus"}, {"extra_float_digits", "4"}} {
		owner := session.SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
		p := &settingsDefaultProviderForTest{values: map[string]string{bad.name: bad.value}, calls: map[string]int{}}
		if err := owner.Initialize(ctx, e.Settings.Definitions(), e.Settings.Order(), p); err == nil {
			t.Fatal("invalid physical default accepted", bad)
		}
	}
	encodingProvider := &settingsDefaultProviderForTest{values: map[string]string{"client_encoding": "LATIN1"}, calls: map[string]int{}}
	physical := enginetest.StartSession(t, e, encodingProvider)
	policy := physical.Settings.ClientEncoding()
	identity := policy.DefaultIdentity()
	if physical.Settings.Value("client_encoding") != "LATIN1" || physical.Settings.DefaultTyped["client_encoding"].ClientEncoding != 8 || policy.ID() != 8 || identity[0] == 0 || identity[1] == 0 || encodingProvider.calls["client_encoding"] != 1 {
		t.Fatal("physical encoding default did not capture actual admitted providers", identity, encodingProvider.calls)
	}
	if got, err := policy.Encode(ctx, "é"); err != nil || got != "\xe9" {
		t.Fatal("admitted physical output conversion", got, err)
	}
	if got, err := policy.Decode(ctx, "\xe9"); err != nil || got != "é" {
		t.Fatal("admitted physical input conversion", got, err)
	}
	encodingProvider.values["client_encoding"] = "SJIS"
	if physical.Settings.Value("client_encoding") != "LATIN1" || physical.Settings.DefaultTyped["client_encoding"].ClientEncoding != 8 || physical.Settings.ClientEncoding().DefaultIdentity() != identity || encodingProvider.calls["client_encoding"] != 1 {
		t.Fatal("live provider changed captured encoding defaults or authority")
	}
	float, err := e.Settings.Lookup("extra_float_digits")
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
	messages, _ := e.Settings.Lookup("lc_messages")
	stats, _ := e.Settings.Lookup("log_statement_stats")
	if messages.Access != settings.Privileged || stats.Access != settings.Privileged {
		t.Fatal("privileged classification changed")
	}
	for _, name := range []string{"client_encoding", "lc_monetary", "lc_numeric", "lc_time", "icu_validation_level", "extra_float_digits"} {
		d, err := e.Settings.Lookup(name)
		if err != nil || d.Access != settings.Session {
			t.Fatal(name, d, err)
		}
	}
}

func TestFormatEncodingCanonicalDeparse(t *testing.T) {
	e := engine.New()
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
		node := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
		for _, precision := range []int32{0, -1, -15} {
			renderer := stored.Deparser{View: view, Output: sqltype.OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: precision}}
			got, err := renderer.Literal(ctx, node)
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
			node := plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: values}
			want := "'{1.2345679}'::real[]"
			if tc.id == sqltype.Float8 {
				want = "'{1.2345678901234567}'::double precision[]"
			}
			got, err = renderer.Literal(ctx, node)
			if err != nil || got != want {
				t.Fatal("rounded precision leaked into persistent array source", precision, got, err)
			}
		}
	}
}

type settingsDefaultProviderForTest struct {
	values map[string]string
	calls  map[string]int
}

func (p *settingsDefaultProviderForTest) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	p.calls[d.Name]++
	if len(d.EnumValues) > 0 {
		d.EnumValues[0] = "provider-mutated-copy"
	}
	value, present := p.values[d.Name]
	return value, present, nil
}
