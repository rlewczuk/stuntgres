package parser

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLInsertExpressionBoundary(t *testing.T) {
	const command = "INSERT INTO io_frame VALUES(TG_NAME,TG_OP,OLD.v,NEW.v,TG_WHEN,TG_LEVEL,TG_TABLE_NAME,TG_TABLE_SCHEMA,TG_RELID='io_view'::regclass,TG_NARGS,TG_ARGV[0])"
	const source = "/* é中 */ BEGIN " + command + "; IF TG_OP='DELETE' THEN OLD.v:=999; RETURN OLD; END IF; IF TG_NAME='a' THEN NEW.v:=NEW.v+3; ELSE NEW.v:=NEW.v*2; END IF; RETURN NEW; END"
	block, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Statements) != 4 || block.Statements[1].If == nil || block.Statements[2].If == nil || !block.Statements[3].Return {
		t.Fatal("embedded INSERT consumed following procedural statements")
	}
	raw := block.Statements[0].SQL
	if raw == nil || !ValidPLSQLStatement(raw) || raw.Text != command || raw.End != raw.Delimiter || source[raw.End.Offset] != ';' {
		t.Fatal("lost admitted SQL source boundary", raw)
	}
	for _, pos := range []Position{raw.Start, raw.End} {
		if pos.Character != utf8.RuneCountInString(source[:pos.Offset])+1 {
			t.Fatal("lost absolute UTF8 provenance", pos)
		}
	}
	insert, ok := raw.Query.(*Insert)
	if !ok || len(insert.Rows) != 1 || len(insert.Rows[0]) != 11 {
		t.Fatal("lost full INSERT row syntax", raw.Query)
	}
	comparison, ok := insert.Rows[0][8].(OperatorExpr)
	if !ok || len(comparison.Name.Parts) != 1 || comparison.Name.Parts[0].Text != "=" || len(comparison.Args) != 2 {
		t.Fatal("comparison was narrowed to an input-only cell", insert.Rows[0][8])
	}
	left, ok := comparison.Args[0].(ColumnExpr)
	if !ok || len(left.Name.Parts) != 1 || left.Name.Parts[0].Text != "tg_relid" {
		t.Fatal("comparison lost lexical operand", comparison.Args[0])
	}
	if _, ok := comparison.Args[1].(RegclassExpr); !ok {
		t.Fatal("comparison lost regclass cast", comparison.Args[1])
	}
	subscript, ok := insert.Rows[0][10].(SubscriptExpr)
	if !ok || len(subscript.Subscripts) != 1 || subscript.Subscripts[0].Slice {
		t.Fatal("lost trigger argument subscript", insert.Rows[0][10])
	}
	if comparison.Pos.Offset != strings.Index(source, "='io_view'") {
		t.Fatal("comparison position no longer refers to original PL source", comparison.Pos)
	}
	fresh, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(block, fresh) || fresh.Statements[0].SQL == raw {
		t.Fatal("per-call syntax ownership", err)
	}
	for _, invalid := range []string{source + " trailing", "BEGIN INSERT INTO io_frame VALUES(TG_RELID=); RETURN NEW; END"} {
		if _, err := ParsePLpgSQL(invalid, PLReturnPolicy{}); err == nil {
			t.Fatal("accepted incomplete expression or trailing procedural input", invalid)
		}
	}
	// Procedural admission must not widen the ordinary input-only frontier.
	if _, err := Parse(command); err == nil {
		t.Fatal("PL parsing changed ordinary INSERT scalar admission")
	}
}
