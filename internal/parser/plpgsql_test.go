package parser

import "testing"

func TestPLpgSQLParserFullConsumption(t *testing.T) {
	for _, source := range []string{
		"BEGIN END", "BEGIN END;",
		"<<b>> DECLARE x integer := 1; y x%TYPE DEFAULT x+1; BEGIN x = y; BEGIN x := 7; END; RETURN x; END b",
		"DECLARE s text := 'END; --'; BEGIN s := $$BEGIN; END$$; END",
		"DECLARE x record; BEGIN x := ROW(1,2); x.f1 := 3; RETURN; END",
		"DECLARE x public.tab.col%TYPE; r public.tab%ROWTYPE; BEGIN END",
		"DECLARE \"constant\" integer := 1; BEGIN \"constant\" := 2; END",
		"DECLARE b boolean := true AND NOT false; BEGIN b := b OR false; RETURN b IS NOT NULL AND b; END",
		"BEGIN IF true THEN RETURN; END IF; END",
		"BEGIN LOOP END LOOP; END",
		"BEGIN RAISE NOTICE 'supported'; ASSERT true; END",
		"BEGIN SELECT 1; END",
	} {
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil || block == nil {
			t.Fatalf("%q: %v", source, err)
		}
	}
	for _, source := range []string{
		"BEGIN END; junk", "BEGIN END; BEGIN END", "<<a>> BEGIN END b",
		"DECLARE x integer; BEGIN RETURN 7; x := 1+; END",
		"BEGIN BEGIN END RETURN 7; END",
		"BEGIN RETURN NEXT 1; END", "BEGIN END\x00", "BEGIN END\xff",
	} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatalf("accepted incomplete/excluded body %q", source)
		}
	}
	// Private entry modes must never leak into an ordinary parse or prewalk.
	statements, err := Parse("BEGIN; SELECT 1; COMMIT")
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	raw, err := ParsePLpgSQL("DECLARE x integer; BEGIN x := 1; END", PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if raw.Declarations[0].Name.Pos.Character != 9 || raw.Statements[0].Pos.Character != 26 {
		t.Fatal("lost original character positions", raw)
	}
	for _, expression := range []string{"1/0", "$0", "r.a IS NULL AND r.b IS NULL", "NOT r.a IS NULL OR r.b IS NULL", "r.a IS DISTINCT FROM r.b"} {
		source := "DECLARE \"é\" integer; BEGIN RETURN " + expression + "; END"
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		raw := block.Statements[0].Expression
		if got := PLExpressionSource(source, block.Statements[0].ExpressionStart, block.Statements[0].ExpressionEnd); got != expression {
			t.Fatalf("source %q, want %q", got, expression)
		}
		start := ExpressionStart(raw)
		if start.Offset != len("DECLARE \"é\" integer; BEGIN RETURN ") || start.Character != len("DECLARE \"e\" integer; BEGIN RETURN ")+1 {
			t.Fatalf("lost byte/character expression origin: %v", start)
		}
	}
	if _, err := Parse("SELECT $0"); err == nil {
		t.Fatal("PL result-placeholder admission leaked into ordinary SQL")
	}
}
