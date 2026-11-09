package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLReturnNextPositions(t *testing.T) {
	for _, source := range []string{
		"DECLARE r record; BEGIN RETURN NEXT r; RETURN; END",
		"DECLARE é record; BEGIN RETURN /* α */ NEXT /* β */ é; RETURN; END",
		"DECLARE \"next\" record; BEGIN RETURN NEXT \"next\"; END",
	} {
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		in := block.Statements[0]
		if in.ReturnNext == nil || in.Return || in.Expression != nil {
			t.Fatal("lost distinct NEXT variant")
		}
		operand := in.ReturnNext
		want := strings.Index(source, "RETURN")
		if in.Pos.Offset != want || in.Pos.Character != utf8.RuneCountInString(source[:want])+1 {
			t.Fatal("wrong RETURN position", in.Pos)
		}
		text := source[operand.Start.Offset:operand.End.Offset]
		if text != "r" && text != "é" && text != "\"next\"" {
			t.Fatal("operand includes NEXT/comments", text)
		}
		if operand.Start.Character != utf8.RuneCountInString(source[:operand.Start.Offset])+1 || source[operand.Delimiter.Offset] != ';' {
			t.Fatal("wrong operand provenance", operand)
		}
	}
	for _, source := range []string{
		"BEGIN RETURN NEXT r; END trailing",
		"BEGIN RETURN NEXT r; END; SELECT 1",
		"BEGIN RETURN NEXT; END",
		"BEGIN RETURN NEXT r garbage; END",
	} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatal("accepted incomplete/extra syntax", source)
		}
	}
	block, err := ParsePLpgSQL("BEGIN RETURN \"next\"; END", PLReturnPolicy{})
	if err != nil || !block.Statements[0].Return || block.Statements[0].ReturnNext != nil {
		t.Fatal("quoted NEXT became keyword", err)
	}
	if _, err := ParsePLpgSQL("DECLARE c refcursor; r record; BEGIN FETCH NEXT FROM c INTO r; MOVE NEXT FROM c; END", PLReturnPolicy{}); err != nil {
		t.Fatal("cursor NEXT changed", err)
	}
}

func TestPLpgSQLReturnNextLiterals(t *testing.T) {
	for _, tc := range []struct{ name, literal string }{
		{"ordinary", "'next'"},
		{"escape", "E'next'"},
		{"dollar", "$literal$next$literal$"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "BEGIN RETURN /* α */ " + tc.literal + " /* β */; END"
			block, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			if len(block.Statements) != 1 {
				t.Fatal("wrong statement count", len(block.Statements))
			}
			in := block.Statements[0]
			if !in.Return || in.ReturnNext != nil || in.Expression == nil {
				t.Fatal("literal RETURN became NEXT")
			}
			start := strings.Index(source, tc.literal)
			end := start + len(tc.literal)
			delimiter := strings.Index(source, ";")
			if in.Expression == nil || source[in.ExpressionStart.Offset:in.ExpressionEnd.Offset] != tc.literal {
				t.Fatal("lost original literal expression", in.Expression)
			}
			if in.ExpressionStart.Offset != start || in.ExpressionEnd.Offset != end || in.ExpressionDelimiter.Offset != delimiter ||
				in.ExpressionStart.Character != utf8.RuneCountInString(source[:start])+1 ||
				in.ExpressionEnd.Character != utf8.RuneCountInString(source[:end])+1 ||
				in.ExpressionDelimiter.Character != utf8.RuneCountInString(source[:delimiter])+1 {
				t.Fatal("wrong literal provenance", in)
			}
			want := strings.Index(source, "RETURN")
			if in.Pos.Offset != want || in.Pos.Character != utf8.RuneCountInString(source[:want])+1 {
				t.Fatal("wrong RETURN position", in.Pos)
			}
			for _, suffix := range []string{" trailing", "; SELECT 1"} {
				if _, err := ParsePLpgSQL(source+suffix, PLReturnPolicy{}); err == nil {
					t.Fatal("accepted trailing syntax", source+suffix)
				}
			}
		})
	}
}

func TestPLpgSQLReturnNextCompoundNames(t *testing.T) {
	for _, tc := range []struct{ name, prefix, expression, ending string }{
		{"block", `<<"next">> DECLARE x integer := 1; BEGIN RETURN `, "next.x", `; END "next"`},
		{"block_trivia", `<<"next">> DECLARE x integer := 1; BEGIN RETURN `, "next /* α /* β */ */ -- γ\n . x", `; END "next"`},
		{"record_field", `DECLARE "next" row_type; BEGIN RETURN `, "next /* α */ . id", "; END"},
		{"record_arithmetic", `DECLARE "next" row_type; BEGIN RETURN `, "next -- α\n . id + 1", "; END"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.prefix + tc.expression + " /* δ */" + tc.ending
			block, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			if len(block.Statements) != 1 {
				t.Fatal("wrong statement count", len(block.Statements))
			}
			in := block.Statements[0]
			if !in.Return || in.ReturnNext != nil || in.Expression == nil {
				t.Fatal("compound RETURN became NEXT")
			}
			start := len(tc.prefix)
			end := start + len(tc.expression)
			delimiter := end + len(" /* δ */")
			if source[in.ExpressionStart.Offset:in.ExpressionEnd.Offset] != tc.expression {
				t.Fatal("lost original compound expression", in.Expression)
			}
			if in.ExpressionStart.Offset != start || in.ExpressionEnd.Offset != end || in.ExpressionDelimiter.Offset != delimiter ||
				in.ExpressionStart.Character != utf8.RuneCountInString(source[:start])+1 ||
				in.ExpressionEnd.Character != utf8.RuneCountInString(source[:end])+1 ||
				in.ExpressionDelimiter.Character != utf8.RuneCountInString(source[:delimiter])+1 {
				t.Fatal("wrong compound provenance", in)
			}
			want := strings.Index(source, "RETURN")
			if in.Pos.Offset != want || in.Pos.Character != utf8.RuneCountInString(source[:want])+1 {
				t.Fatal("wrong RETURN position", in.Pos)
			}
			for _, suffix := range []string{" trailing", "; SELECT 1"} {
				if _, err := ParsePLpgSQL(source+suffix, PLReturnPolicy{}); err == nil {
					t.Fatal("accepted trailing syntax", source+suffix)
				}
			}
		})
	}
}

func TestPLpgSQLReturnNextDotOperands(t *testing.T) {
	for _, prefix := range []string{"BEGIN RETURN NEXT ", "BEGIN RETURN NEXT /* α */ -- β\n "} {
		source := prefix + ".5; END"
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		in := block.Statements[0]
		if in.ReturnNext == nil || in.Return || in.Expression != nil {
			t.Fatal("leading-dot numeric operand became ordinary RETURN")
		}
		operand := in.ReturnNext
		start := len(prefix)
		if operand.Expression == nil || operand.Start.Offset != start || operand.End.Offset != start+2 ||
			operand.Delimiter.Offset != start+2 || source[operand.Start.Offset:operand.End.Offset] != ".5" ||
			operand.Start.Character != utf8.RuneCountInString(source[:start])+1 ||
			operand.End.Character != utf8.RuneCountInString(source[:start+2])+1 ||
			operand.Delimiter.Character != operand.End.Character {
			t.Fatal("wrong numeric operand provenance", operand)
		}
	}
	l := lexer{input: "RETURN NEXT /* α */ ..", plMode: true}
	var value sqlSymType
	if code := l.Lex(&value); code != PL_RETURN_NEXT || l.err != nil {
		t.Fatal("range punctuation became compound RETURN", code, l.err)
	}
	if code := l.Lex(&value); code != DOT_DOT || value.tok.raw != ".." || l.err != nil {
		t.Fatal("lost range token", code, value.tok, l.err)
	}
}
