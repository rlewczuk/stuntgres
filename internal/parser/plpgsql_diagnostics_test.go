package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLDiagnosticsParser(t *testing.T) {
	const source = "DECLARE x bigint; s text; BEGIN\n-- é\nGET DIAGNOSTICS x=ROW_COUNT,x:=PG_ROUTINE_OID,s=PG_CONTEXT;\nGET CURRENT DIAGNOSTICS x=ROW_COUNT;\nGET STACKED DIAGNOSTICS s=MESSAGE_TEXT,s=PG_CONTEXT,s=PG_EXCEPTION_CONTEXT; END"
	raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Statements) != 3 {
		t.Fatal("lost statement boundaries")
	}
	want := [][]PLDiagnosticItem{{PLRowCount, PLRoutineOID, PLContext}, {PLRowCount}, {PLMessageText, PLContext, PLExceptionContext}}
	for i, statement := range raw.Statements {
		d := statement.Diagnostics
		if !ValidPLDiagnostics(d) || d.Stacked != (i == 2) || len(d.Assignments) != len(want[i]) || d.Pos != statement.Pos || statement.Expression != nil || statement.SQL != nil || statement.Target != nil {
			t.Fatal("diagnostics variant/area/order", i, statement)
		}
		if d.End != d.Delimiter || source[d.End.Offset] != ';' || d.Text != source[d.Pos.Offset:d.End.Offset] || d.End.Character != utf8.RuneCountInString(source[:d.End.Offset])+1 {
			t.Fatal("lost exact source occurrence or UTF8 statement delimiter", d)
		}
		for j, assignment := range d.Assignments {
			if assignment.Item != want[i][j] || assignment.Target == nil {
				t.Fatal("lost destination/item occurrence", i, j)
			}
			pos := assignment.ItemPos
			if pos.Character != utf8.RuneCountInString(source[:pos.Offset])+1 || !strings.HasPrefix(source[pos.Offset:], assignment.Item.String()) {
				t.Fatal("lost UTF8 item coordinate", i, j, pos)
			}
			at := assignment.Target.SourcePosition()
			if at.Character != utf8.RuneCountInString(source[:at.Offset])+1 {
				t.Fatal("lost UTF8 target position", at)
			}
		}
	}
	if raw.Statements[0].Pos.Offset != strings.Index(source, "GET") || raw.Statements[0].Pos.Character != utf8.RuneCountInString(source[:strings.Index(source, "GET")])+1 {
		t.Fatal("GET coordinate counted bytes as characters")
	}
	fresh, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(raw, fresh) {
		t.Fatal("per-call parser state", err)
	}
	fresh.Statements[0].Diagnostics.Assignments[0].Item = PLContext
	if raw.Statements[0].Diagnostics.Assignments[0].Item != PLRowCount || ValidPLDiagnostics(fresh.Statements[0].Diagnostics) {
		t.Fatal("shared mutable assignment backing or forged parser admission")
	}
	for _, body := range []string{
		"GET DIAGNOSTICS x=;", "GET DIAGNOSTICS x=ROW_COUNT,;", "GET DIAGNOSTICS x=ROW_COUNT junk;",
		"GET CURRENT DIAGNOSTICS x=MESSAGE_TEXT;", "GET STACKED DIAGNOSTICS x=ROW_COUNT;",
		"GET STACKED DIAGNOSTICS x=PG_ROUTINE_OID;", "GET DIAGNOSTICS x=PG_EXCEPTION_DETAIL;",
		"GET DIAGNOSTICS x=bogus;", "IF false THEN GET DIAGNOSTICS x=bogus; END IF;",
	} {
		if _, err := ParsePLpgSQL("DECLARE x bigint; BEGIN "+body+" END", PLReturnPolicy{}); err == nil {
			t.Fatal("accepted malformed/unreachable diagnostics", body)
		}
	}
	if _, err := ParsePLpgSQL("DECLARE x bigint; BEGIN GET DIAGNOSTICS x=ROW_COUNT; END; junk", PLReturnPolicy{}); err == nil {
		t.Fatal("accepted valid prefix with invalid suffix")
	}
	if _, err := Parse("SELECT get,current,stacked,diagnostics FROM t"); err != nil {
		t.Fatal("contextual identifiers changed SQL", err)
	}
	if _, err := ParsePLpgSQL(`DECLARE get bigint; current bigint; stacked bigint; diagnostics bigint; BEGIN get:=1; current:=get; stacked:=current; diagnostics:=stacked; GET DIAGNOSTICS diagnostics=ROW_COUNT; END`, PLReturnPolicy{}); err != nil {
		t.Fatal("contextual identifiers changed PL expressions", err)
	}
}

func TestPLpgSQLDiagnosticsParserQuotedItems(t *testing.T) {
	for _, tc := range []struct{ area, token string }{
		{"", `"row_count"`}, {"CURRENT ", `"ROW_COUNT"`},
		{"", `"pg_context"`}, {"", `"PG_CONTEXT"`},
		{"STACKED ", `"message_text"`}, {"STACKED ", `"MESSAGE_TEXT"`},
		{"", `U&"row\005fcount"`}, {"", `U&"PG\005fCONTEXT"`},
	} {
		t.Run(tc.area+tc.token, func(t *testing.T) {
			source := `DECLARE "é" text; BEGIN IF false THEN GET ` + tc.area + `DIAGNOSTICS "é"=` + tc.token + `; END IF; RETURN 1; END`
			raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
			var got *sqlstate.Error
			at := strings.Index(source, tc.token)
			want := sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
				Message:  `unrecognized GET DIAGNOSTICS item at or near "` + tc.token + `"`,
				Position: strconv.Itoa(utf8.RuneCountInString(source[:at]) + 1),
			}
			if raw != nil || !errors.As(err, &got) || *got != want {
				t.Fatalf("quoted item admission: raw=%#v diagnostic=%#v err=%v want=%#v", raw, got, err, want)
			}
		})
	}
}

func TestPLpgSQLDiagnosticsParserSeparateAdmission(t *testing.T) {
	const source = "DECLARE x text; BEGIN GET DIAGNOSTICS x=ROW_COUNT; RETURN x; END"
	original, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	donor, err := ParsePLpgSQL(strings.Replace(source, "ROW_COUNT", "PG_CONTEXT", 1), PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	prior := original.Statements[0].Diagnostics
	replacement := donor.Statements[0].Diagnostics
	if prior == replacement || &prior.Assignments[0] == &replacement.Assignments[0] || !ValidPLDiagnostics(prior) || !ValidPLDiagnostics(replacement) {
		t.Fatal("separate parses did not produce independently admitted backing")
	}
	if prior.Pos != replacement.Pos || prior.Assignments[0].ItemPos != replacement.Assignments[0].ItemPos || !reflect.DeepEqual(prior.Assignments[0].Target, replacement.Assignments[0].Target) {
		t.Fatal("donor does not preserve the coordinates and target of the source occurrence")
	}
	// Substitution is not mutation of the seal: both payloads remain admitted.
	// The owning definition validator must additionally bind it to its source.
	original.Statements[0].Diagnostics = replacement
	if !ValidPLDiagnostics(original.Statements[0].Diagnostics) || prior.Assignments[0].Item != PLRowCount || replacement.Assignments[0].Item != PLContext {
		t.Fatal("substitution mutated previously admitted backing")
	}
}
