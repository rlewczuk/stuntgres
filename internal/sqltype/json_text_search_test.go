package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/workadmission"
	"testing"
)

func TestJSONTextSearchFilters(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		input         string
		flags         JSONTextSearchFlags
		code, message string
		hint          bool
	}{
		{`"string"`, JSONTextSearchString, "", "", false},
		{`["NUMERIC","boolean","key","string","string"]`, JSONTextSearchAll, "", "", false},
		{`"ALL"`, JSONTextSearchAll, "", "", false}, {`[]`, 0, "", "", false},
		{`{}`, 0, "22023", "wrong flag type, only arrays and scalars are allowed", false},
		{`null`, 0, "22023", "flag array element is not a string", true},
		{`true`, 0, "22023", "flag array element is not a string", true},
		{`1`, 0, "22023", "flag array element is not a string", true},
		{`["all",1]`, 0, "22023", "flag array element is not a string", true},
		{`[null]`, 0, "22023", "flag array element is not a string", true},
		{`" string"`, 0, "22023", `wrong flag in flag array: " string"`, true},
		{`""`, 0, "22023", `wrong flag in flag array: ""`, true},
		{`["string","bogus"]`, 0, "22023", `wrong flag in flag array: "bogus"`, true},
		{`[[]]`, 0, "XX000", "unexpected end of flag array", false},
		{`[{}]`, 0, "XX000", "unexpected end of flag array", false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			value, err := types[JSONB].Input(t.Context(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			before := value.jsonb
			got, err := ParseJSONTextSearchFlags(t.Context(), value)
			if tc.code == "" {
				if err != nil || got != tc.flags {
					t.Fatal(got, err)
				}
			} else {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || (d.Hint != "") != tc.hint || d.Detail != "" || d.Context != "" {
					t.Fatal(err)
				}
				if tc.hint && d.Hint != `Possible values are: "string", "numeric", "boolean", "key", and "all".` {
					t.Fatal(d.Hint)
				}
			}
			if value.jsonb != before {
				t.Fatal("filter payload replaced")
			}
		})
	}
	for _, bad := range []Value{{}, Null(types[JSONB]), String(types[Text], "all"), {typ: types[JSONB]}} {
		if _, err := ParseJSONTextSearchFlags(t.Context(), bad); err == nil {
			t.Fatal("invalid filter accepted")
		}
	}
}

func TestJSONTextSearchTraversal(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		id     ID
		source string
		flags  JSONTextSearchFlags
		want   []string
	}{
		{JSON, `{"k":"cat","k":"dog"}`, JSONTextSearchString, []string{"cat", "dog"}},
		{JSONB, `{"k":"cat","k":"dog"}`, JSONTextSearchString, []string{"dog"}},
		{JSON, `{"outer":["red",{"inner":"blue"}]}`, JSONTextSearchAll, []string{"outer", "red", "inner", "blue"}},
		{JSONB, `{"outer":["red",{"inner":"blue"}]}`, JSONTextSearchAll, []string{"outer", "red", "inner", "blue"}},
		{JSON, `[1e2,1.00,-0]`, JSONTextSearchNumeric, []string{"1e2", "1.00", "-0"}},
		{JSONB, `[1e2,1.00,-0]`, JSONTextSearchNumeric, []string{"100", "1.00", "0"}},
		{JSON, `[true,false,null,"",{"\u006b":"a\nb"}]`, JSONTextSearchAll, []string{"true", "false", "", "k", "a\nb"}},
		{JSONB, `[123456789012345678901234567890.00]`, JSONTextSearchNumeric, []string{"123456789012345678901234567890.00"}},
		{JSON, strings.Repeat("[", 10000) + `"deep"` + strings.Repeat("]", 10000), JSONTextSearchString, []string{"deep"}},
	} {
		value, err := types[tc.id].Input(t.Context(), tc.source, -1)
		if err != nil {
			t.Fatal(err)
		}
		before := value
		var got []string
		err = WalkJSONTextSearch(t.Context(), value, tc.flags, func(s string) error { got = append(got, s); return nil })
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %q != %q: %v", tc.source, got, tc.want, err)
		}
		if value != before {
			t.Fatal("traversal mutated retained input")
		}
	}
	for _, source := range []string{`"\u0000"`, `{"\u0000":null}`, `["ok","\u0000"]`} {
		value, err := types[JSON].Input(t.Context(), source, -1)
		if err != nil {
			t.Fatal(err)
		}
		err = WalkJSONTextSearch(t.Context(), value, 0, func(string) error { t.Fatal("unselected visit"); return nil })
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22P05" {
			t.Fatal(err)
		}
	}
	// A field callback is after the first value token; a scalar callback is
	// after its lookahead. Later unrelated bad Unicode must not win.
	sentinel := errors.New("visitor refused")
	for _, source := range []string{`["first","\u0000"]`, `{"key":["\u0000"]}`} {
		value, err := types[JSON].Input(t.Context(), source, -1)
		if err != nil {
			t.Fatal(err)
		}
		err = WalkJSONTextSearch(t.Context(), value, JSONTextSearchAll, func(string) error { return sentinel })
		if err != sentinel {
			t.Fatal("callback timing", err)
		}
	}
}

func TestJSONTextSearchCancellation(t *testing.T) {
	types := testTypes()
	for _, id := range []ID{JSON, JSONB} {
		for _, source := range []string{`["first","second"]`, `["` + strings.Repeat("x", 20000) + `"]`, `[` + strings.Repeat("1", 20000) + `]`} {
			// JSONB numeric input has its own finite numeric limits.
			if id == JSONB && source[1] != '"' {
				continue
			}
			value, err := types[id].Input(t.Context(), source, -1)
			if err != nil {
				t.Fatal(err)
			}
			refusal := errors.New("conversion refused")
			ctx := workadmission.Activate(workadmission.With(t.Context(), func(_ context.Context, class workadmission.Class) error {
				if class == workadmission.WorkTSVectorConvert {
					return refusal
				}
				return nil
			}))
			if err := WalkJSONTextSearch(ctx, value, 0, func(string) error { t.Fatal("unselected callback"); return nil }); err != refusal {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := WalkJSONTextSearch(ctx, value, JSONTextSearchAll, func(string) error { return nil }); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	for _, id := range []ID{JSON, JSONB} {
		value, err := types[id].Input(t.Context(), `["first","second"]`, -1)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		visits := 0
		err = WalkJSONTextSearch(ctx, value, JSONTextSearchString, func(string) error { visits++; cancel(); return nil })
		cancel()
		if !errors.Is(err, context.Canceled) || visits != 1 {
			t.Fatal("cancellation allowed later callback", visits, err)
		}
	}
	long, err := types[JSON].Input(t.Context(), `"`+strings.Repeat(`\u0061`, 10000)+`"`, -1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	admissions := 0
	ctx = workadmission.Activate(workadmission.With(ctx, func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkTSVectorConvert {
			admissions++
			if admissions == 2 {
				cancel()
				return context.Canceled
			}
		}
		return nil
	}))
	if err := WalkJSONTextSearch(ctx, long, 0, func(string) error { t.Fatal("unselected callback"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("long decode ignored cancellation", err)
	}
}
