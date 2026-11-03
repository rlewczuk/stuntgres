package fts

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func headlineUnitInteger(text string) (int32, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 32)
	return int32(n), err
}
func TestFTSHeadlineOptionsGrammar(t *testing.T) {
	for _, tc := range []struct{ text, start, stop string }{
		{"", "<b>", "</b>"}, {", , \t", "<b>", "</b>"},
		{`"StartSel"="a, b"StopSel="c""d"`, "a, b", "c\"d"},
		{`StartSel='a''b',StopSel=E'c\\d'`, "a'b", "c\\d"},
		{`StartSel='\n',StopSel='\t'`, "\\n", "\\t"},
		{`StartSel="",StopSel=''`, "", ""},
		{"StartSel==x", "=x", "</b>"},
		{"StartSel=00012,StopSel=+0003", "12", "3"},
		{"STARTSEL=x,StartSel=y,STOPSEL=z", "y", "z"},
	} {
		options, err := ParseHeadlineOptions(t.Context(), tc.text, headlineUnitInteger)
		if err != nil || options.StartSel != tc.start || options.StopSel != tc.stop {
			t.Fatal(tc, options, err)
		}
	}
	for _, tc := range []struct {
		text string
		want HeadlineOptions
	}{
		{"hIgHlIgHtAlL=true", HeadlineOptions{MaxWords: 35, MinWords: 15, ShortWord: 3, HighlightAll: true, StartSel: "<b>", StopSel: "</b>", FragmentDelimiter: " ... "}},
		{"mInWoRdS=1", HeadlineOptions{MaxWords: 35, MinWords: 1, ShortWord: 3, StartSel: "<b>", StopSel: "</b>", FragmentDelimiter: " ... "}},
	} {
		options, err := ParseHeadlineOptions(t.Context(), tc.text, headlineUnitInteger)
		if err != nil || options != tc.want {
			t.Fatal(tc, options, err)
		}
	}
	for _, text := range []string{"1", "on", "true", "t", "y", "yes", "TrUe"} {
		o, err := ParseHeadlineOptions(t.Context(), "HighlightAll="+text+",MinWords=-1,MaxWords=0,ShortWord=-1", headlineUnitInteger)
		if err != nil || !o.HighlightAll {
			t.Fatal(text, o, err)
		}
	}
	o, err := ParseHeadlineOptions(t.Context(), "HighlightAll=nonsense", headlineUnitInteger)
	if err != nil || o.HighlightAll {
		t.Fatal(o, err)
	}
}
func TestFTSHeadlineParametersInitialKey(t *testing.T) {
	parameters, err := headlineParameters(t.Context(), "=x")
	var diagnostic *sqlstate.Error
	if parameters != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != `invalid parameter list format: "=x"` {
		t.Fatal(parameters, err)
	}
	for _, tc := range []struct{ input, key, value string }{
		{"==x", "=", "x"},
		{`""=x`, "", "x"},
		{`"StartSel"=x`, "StartSel", "x"},
		{"StartSel==x", "StartSel", "=x"},
	} {
		parameters, err := headlineParameters(t.Context(), tc.input)
		if err != nil || len(parameters) != 1 || parameters[0].key != tc.key || parameters[0].value != tc.value {
			t.Fatal(tc, parameters, err)
		}
	}
}
func TestFTSHeadlineOptionsDiagnostics(t *testing.T) {
	for _, tc := range []struct{ input, code, message string }{
		{"StartSel=", "42601", `invalid parameter list format: "StartSel="`},
		{"StartSel", "42601", `invalid parameter list format: "StartSel"`},
		{"=x", "42601", `invalid parameter list format: "=x"`},
		{"==x", "22023", `unrecognized headline parameter: "="`},
		{`""=x`, "22023", `unrecognized headline parameter: ""`},
		{"Bad=1", "22023", `unrecognized headline parameter: "Bad"`},
		{"HİghlightAll=true", "22023", `unrecognized headline parameter: "HİghlightAll"`},
		{"MİnWords=1", "22023", `unrecognized headline parameter: "MİnWords"`},
		{"MaxWords=0,MinWords=0", "22023", "MinWords must be less than MaxWords"},
		{"MinWords=0,ShortWord=-1", "22023", "MinWords must be positive"},
		{"ShortWord=-1,MaxFragments=-1", "22023", "ShortWord must be >= 0"},
		{"MaxFragments=-1", "22023", "MaxFragments must be >= 0"},
		// Deserialize the whole list before inspecting unknown options.
		{"Bad=1,StartSel=", "42601", `invalid parameter list format: "Bad=1,StartSel="`},
	} {
		_, err := ParseHeadlineOptions(t.Context(), tc.input, headlineUnitInteger)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(tc, err)
		}
	}
	text := strings.Repeat("StartSel=\"x\" ", 16)
	baseline := &countingContext{Context: t.Context(), after: int(^uint(0) >> 1)}
	if _, err := ParseHeadlineOptions(baseline, text, headlineUnitInteger); err != nil {
		t.Fatal(err)
	}
	for after := 1; after <= baseline.calls; after++ {
		ctx := &countingContext{Context: t.Context(), after: after}
		if _, err := ParseHeadlineOptions(ctx, text, headlineUnitInteger); !errors.Is(err, context.Canceled) {
			t.Fatal("option cancellation", after, err)
		}
	}
}
