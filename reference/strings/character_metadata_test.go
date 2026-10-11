package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCharacterAliasesAndMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct {
		declaration, typ, value string
		length                  int64
	}{
		{"char", "BPCHAR", "é", 1}, {"character", "BPCHAR", "é", 1},
		{"char(3)", "BPCHAR", "é  ", 3}, {"character(3)", "BPCHAR", "é  ", 3},
		{"bpchar(3)", "BPCHAR", "é  ", 3}, {"bpchar", "BPCHAR", "é", -1},
		{"varchar", "VARCHAR", "é", -1}, {"character varying", "VARCHAR", "é", -1},
		{"varchar(3)", "VARCHAR", "é", 3}, {"character varying(3)", "VARCHAR", "é", 3},
		{"text", "TEXT", "é", -1}, {`"bpchar"`, "BPCHAR", "é", -1}, {`"varchar"(3)`, "VARCHAR", "é", 3},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			p.Step(t, fixture.Exec("CREATE TABLE aliases(v "+tc.declaration+")", 0))
			for _, prepared := range []bool{false, true} {
				for _, backend := range p.Backends() {
					fixture.CharacterMetadata(t, p, backend.DB, "SELECT v FROM aliases", prepared, !backend.PG, []string{"v"}, []string{tc.typ}, []int64{tc.length}, []fixture.CharacterNullability{{true, true}})
				}
			}
			p.Step(t, fixture.Exec("INSERT INTO aliases VALUES('é'),(NULL)", 2))
			s := fixture.Select("SELECT v FROM aliases", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}, {nil}})
			s.Bag = true
			p.Step(t, s)
			p.Step(t, fixture.Prepared(s))
			for _, backend := range p.Backends() {
				fixture.CharacterMetadata(t, p, backend.DB, "SELECT v FROM aliases", false, !backend.PG, []string{"v"}, []string{tc.typ}, []int64{tc.length}, []fixture.CharacterNullability{{true, true}})
			}
			p.Step(t, fixture.Exec("DROP TABLE aliases", 0))
		})
	}
	for _, declaration := range []string{"char(1)", "varchar(1)", "char(10485760)", "varchar(10485760)"} {
		p.Step(t, fixture.Exec("CREATE TABLE maximum(v "+declaration+")", 0))
		p.Step(t, fixture.Exec("DROP TABLE maximum", 0))
	}
	for _, declaration := range []string{`"character"`, `"VARCHAR"`} {
		p.Step(t, fixture.ImplicitError("CREATE TABLE c(v "+declaration+")", fixture.Diagnostic{Code: "42704", Message: "type " + declaration + " does not exist", Position: "18"}))
	}
	_, err := p.Memory.ExecContext(p.Ctx, `CREATE TABLE c(v "char")`)
	fixture.State(t, err, "0A000")
}

func TestReferenceCharacterCastLabelsAndNullMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE inputs(t TEXT,c CHAR(2),v VARCHAR(2),u BPCHAR)", 0))
	for _, q := range []string{"INSERT INTO inputs(c) VALUES('abc')", "INSERT INTO inputs(c) VALUES(CAST('abcd' AS varchar(3)))", "INSERT INTO inputs(c) VALUES($1)"} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22001", Message: "value too long for type character(2)"})
		if q == "INSERT INTO inputs(c) VALUES($1)" {
			s.Args = []any{"abc"}
		}
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	for _, prepared := range []bool{false, true} {
		for _, backend := range p.Backends() {
			fixture.CharacterMetadata(t, p, backend.DB, "SELECT c,v,t,u FROM inputs", prepared, !backend.PG, []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, []int64{2, 2, -1, -1}, []fixture.CharacterNullability{{true, true}, {true, true}, {true, true}, {true, true}})
		}
		read := fixture.Select("SELECT c,v,t,u FROM inputs", []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, nil)
		read.Prepared = prepared
		p.Step(t, read)
	}
	p.Step(t, fixture.Exec("CREATE TABLE cast_names(c CHAR(3),v VARCHAR(3),t TEXT)", 0))
	columnQuery := "SELECT CAST(c AS varchar(2)),CAST(v AS char(4)),CAST(t AS text),c::char(4)::text,CAST(CAST(v AS char(2)) AS varchar(3)) FROM cast_names"
	for _, prepared := range []bool{false, true} {
		names := []string{"c", "v", "t", "c", "v"}
		types := []string{"VARCHAR", "BPCHAR", "TEXT", "TEXT", "VARCHAR"}
		lengths := []int64{2, 4, -1, -1, 3}
		// Only CAST(t AS text) is identical Usage and retains the physical
		// column proof. Resizing/retyping casts remain genuine expressions.
		nullability := []fixture.CharacterNullability{{}, {}, {true, true}, {}, {}}
		for _, backend := range p.Backends() {
			fixture.CharacterMetadata(t, p, backend.DB, columnQuery, prepared, !backend.PG, names, types, lengths, nullability)
		}
		read := fixture.Select(columnQuery, names, types, nil)
		read.Prepared = prepared
		p.Step(t, read)
	}
	p.Step(t, fixture.Exec("CREATE TABLE seed(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO seed VALUES(1)", 1))
	q := "SELECT CAST(NULL AS char(3)),CAST(NULL AS varchar(2)),CAST(NULL AS text),CAST(NULL AS bpchar),CAST(NULL AS varchar) FROM seed"
	names := []string{"bpchar", "varchar", "text", "bpchar", "varchar"}
	types := []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}
	lengths := []int64{3, 2, -1, -1, -1}
	for _, prepared := range []bool{false, true} {
		for _, backend := range p.Backends() {
			fixture.CharacterMetadata(t, p, backend.DB, q, prepared, !backend.PG, names, types, lengths, make([]fixture.CharacterNullability, len(types)))
		}
		s := fixture.Select(q, names, types, [][]any{{nil, nil, nil, nil, nil}})
		if prepared {
			s = fixture.Prepared(s)
		}
		p.Step(t, s)
	}
}

func TestReferenceCharacterCastIdentityMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE proof(c CHAR(3) NOT NULL,v VARCHAR(3),t TEXT NOT NULL,u BPCHAR,n CHAR(3))", 0))
	q := "SELECT c::char(3),c::char(4),v::varchar(3),v::varchar(2),CAST(t AS text),u::bpchar,n::char(3),c::char(3)::char(3),c::char(4)::char(4) FROM proof"
	names := []string{"c", "c", "v", "v", "t", "u", "n", "c", "c"}
	types := []string{"BPCHAR", "BPCHAR", "VARCHAR", "VARCHAR", "TEXT", "BPCHAR", "BPCHAR", "BPCHAR", "BPCHAR"}
	lengths := []int64{3, 4, 3, 2, -1, -1, 3, 3, 4}
	// Identity casts preserve the physical nullable/not-null proof. Typmod
	// changes, including a nested identity of a changed expression, do not.
	nullability := []fixture.CharacterNullability{{false, true}, {}, {true, true}, {}, {false, true}, {true, true}, {true, true}, {false, true}, {}}
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO proof VALUES('a','b  ','tail ','u  ',NULL)", 1))
		}
		for _, prepared := range []bool{false, true} {
			for _, backend := range p.Backends() {
				fixture.CharacterMetadata(t, p, backend.DB, q, prepared, !backend.PG, names, types, lengths, nullability)
			}
			var values [][]any
			if populated {
				values = [][]any{{"a  ", "a   ", "b  ", "b ", "tail ", "u  ", nil, "a  ", "a   "}}
			}
			s := fixture.Select(q, names, types, values)
			if prepared {
				s = fixture.Prepared(s)
			}
			p.Step(t, s)
		}
	}
}
