package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/xml"
)

func TestXML2ScalarAdapter(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, id := range []ID{Text, Float4, Bool} {
		v, err := XML2Scalar(ctx, types[id], `<r><a>12</a></r>`, `a`)
		if err != nil || !v.ValidResult(types[id].Usage()) || v.IsNull() {
			t.Fatal("typed result", id, v, err)
		}
		out, err := types[id].Output(ctx, v)
		want := map[ID]string{Text: "12", Float4: "12", Bool: "t"}[id]
		if err != nil || out != want {
			t.Fatal(id, out, err)
		}
		v, err = XML2Scalar(ctx, types[id], `<r>`, `[`)
		if err != nil || v.Type() != types[id] || v.IsNull() != (id != Bool) {
			t.Fatal("document projection", id, v, err)
		}
		_, err = XML2Scalar(ctx, types[id], `<r/>`, `[`)
		var diagnostic *sqlstate.Error
		wantError := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "10608", Message: "XPath Syntax Error", Detail: "Invalid expression"}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, wantError) {
			t.Fatal("complete legacy envelope", err)
		}
	}
	warnings := &xmlUnitDiagnostics{}
	input := `<r xml:space='invalid'>8</r>`
	v, err := XML2Valid(ctx, types[Bool], input, InputEnvironment{XMLInput: warnings})
	b, berr := v.Boolean()
	message := "line 1: Invalid value \"invalid\" for xml:space : \"default\" or \"preserve\" expected\n" + input + "\n                      ^"
	wantWarning := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: message}
	if err != nil || berr != nil || !b || len(warnings.warnings) != 1 || !reflect.DeepEqual(warnings.warnings[0], wantWarning) {
		t.Fatal("genuine warning", v, err, warnings.warnings)
	}
	_, err = XML2Scalar(ctx, types[Text], input, `[`)
	var legacy *sqlstate.Error
	if !errors.As(err, &legacy) || legacy.Detail != message+"\nInvalid expression" || legacy.Context != "" {
		t.Fatal("warning accumulated only in compile error", err)
	}
	if len(warnings.warnings) != 1 {
		t.Fatal("legacy emitted standalone notice")
	}
	for _, mode := range []XMLMode{XMLContent, XMLDocument, XMLContent} {
		v, err := XML2Valid(ctx, types[Bool], "text", InputEnvironment{XMLMode: mode})
		b, berr := v.Boolean()
		if err != nil || berr != nil || b != (mode == XMLContent) {
			t.Fatal("current mode", v, err)
		}
	}
	// The core mapper still owns its original primary and strict context errors.
	core := xmlError(&xml.XPathError{Stage: "compile", Detail: "Invalid expression"}).(*sqlstate.Error)
	if core.Message != "invalid XPath expression" || core.Code != "10608" {
		t.Fatal("core mapper changed", core)
	}
	ordinary := errors.New("ordinary XML owner error")
	if xmlError(ordinary) != ordinary {
		t.Fatal("ordinary error replaced")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, id := range []ID{Text, Float4, Bool} {
		v, err := XML2Scalar(cancelled, types[id], `<r/>`, ".")
		if !errors.Is(err, context.Canceled) || v.Type() != nil {
			t.Fatal("canceled scalar published", v, err)
		}
	}
	v, err = XML2Valid(cancelled, types[Bool], `<r/>`, InputEnvironment{})
	if !errors.Is(err, context.Canceled) || v.Type() != nil {
		t.Fatal("canceled validation published", v, err)
	}
}
