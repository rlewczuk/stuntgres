package parser

import (
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func regOperatorSignatureParts(text string) (name string, args []string, err error) {
	err = ParseRegOperatorSignature(text, func(raw string) error {
		name = raw
		return nil
	}, func(raw string) error {
		args = append(args, raw)
		return nil
	})
	return
}

func TestRegOperatorSignatureSeparation(t *testing.T) {
	for _, tc := range []struct {
		input, name string
		args        []string
	}{
		{`+(int4,int4)`, `+`, []string{"int4", "int4"}},
		{` "Reg Op" . "+" (numeric(10,2), numeric(8,3)) `, ` "Reg Op" . "+" `, []string{"numeric(10,2)", "numeric(8,3)"}},
		{`"a(b"."x""y"("type,name"[],NONE)`, `"a(b"."x""y"`, []string{`"type,name"[]`, "NONE"}},
		{`+()`, `+`, nil},
	} {
		name, args, err := regOperatorSignatureParts(tc.input)
		if err != nil || name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Fatal(tc.input, name, args, err)
		}
	}
	for _, tc := range []struct{ input, message string }{{`+`, `expected a left parenthesis`}, {`+(integer,integer`, `expected a right parenthesis`}, {`+(integer,)`, `expected a type name`}, {`+("unterminated,integer)`, `improper type name`}, {`+(integer[,integer)`, `improper type name`}, {`+(numeric(10,2),integer))`, `improper type name`}} {
		_, _, err := regOperatorSignatureParts(tc.input)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22P02" || d.Message != tc.message {
			t.Fatal(tc.input, err)
		}
	}
}

// Operand separation must preserve non-ASCII bytes for the ordinary type parser.
// In particular U+00A0 cannot become the bare NONE sentinel or an empty body.
func TestRegOperatorSignatureScannerWhitespace(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
	}{
		{"ascii", "+( \t\n\r\v\finteger \t\n\r\v\f, \t\n\r\v\fNONE \t\n\r\v\f) \t\n\r\v\f", []string{"integer", "NONE"}},
		{"nbsp_before_none", "-( NONE,integer)", []string{" NONE", "integer"}},
		{"nbsp_after_none", "-(NONE ,integer)", []string{"NONE ", "integer"}},
		{"nbsp_before_type", "+( integer,integer)", []string{" integer", "integer"}},
		{"nbsp_after_type", "+(integer ,integer)", []string{"integer ", "integer"}},
		{"nbsp_before_second_type", "-(NONE, integer)", []string{"NONE", " integer"}},
		{"nbsp_after_second_type", "-(NONE,integer )", []string{"NONE", "integer "}},
		{"nbsp_only_body", "+( )", []string{" "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, args, err := regOperatorSignatureParts(tc.input)
			if err != nil || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("input %q: operands %#v error %v, want %#v", tc.input, args, err, tc.args)
			}
		})
	}
}

func TestRegOperatorSignatureIncrementalOrder(t *testing.T) {
	nameFailure := &sqlstate.Error{Code: "42602", Message: "invalid name syntax"}
	for _, input := range []string{"a..+(integer", "a..+(missing,)"} {
		err := ParseRegOperatorSignature(input, func(name string) error {
			if name != "a..+" {
				t.Fatal("unexpected name", name)
			}
			return nameFailure
		}, func(string) error {
			t.Fatal("operand visited after failed name")
			return nil
		})
		if err != nameFailure {
			t.Fatal("delimiter preempted name callback", input, err)
		}
	}
	typeFailure := &sqlstate.Error{Code: "42704", Message: "missing type"}
	for _, input := range []string{"+(missing,)", "+(missing,integer[)", "+(missing,\"unterminated)"} {
		err := ParseRegOperatorSignature(input, func(string) error { return nil }, func(arg string) error {
			if arg != "missing" {
				t.Fatal("later operand visited", arg)
			}
			return typeFailure
		})
		if err != typeFailure {
			t.Fatal("later syntax preempted operand callback", input, err)
		}
	}
	called := false
	err := ParseRegOperatorSignature("+(missing,integer", func(string) error { return nil }, func(string) error {
		called = true
		return typeFailure
	})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Message != "expected a right parenthesis" || called {
		t.Fatal("operand visited before right delimiter", err, called)
	}
	_, args, err := regOperatorSignatureParts("+(,integer)")
	if err != nil || !reflect.DeepEqual(args, []string{"", "integer"}) {
		t.Fatal("empty leading operand did not reach type callback", args, err)
	}
}
