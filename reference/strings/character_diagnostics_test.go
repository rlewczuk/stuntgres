package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCharacterEncodingDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE inputs(t TEXT,c CHAR(2),v VARCHAR(2),u BPCHAR)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE unlimited(v VARCHAR)", 0))
	for _, column := range []string{"t", "c", "v", "u", "unlimited"} {
		q := "INSERT INTO inputs(" + column + ") VALUES($1)"
		if column == "unlimited" {
			q = "INSERT INTO unlimited(v) VALUES($1)"
		}
		for _, backend := range p.Backends() {
			func() {
				stmt, err := backend.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					payload []byte
					bytes   string
				}{
					{[]byte{'a', 0, 'b'}, "0x00"}, {[]byte{0xff}, "0xff"}, {[]byte{0xc3, 0x28}, "0xc3 0x28"}, {[]byte{0xe2, 0x82}, "0xe2 0x82"},
				} {
					for _, arg := range []any{string(tc.payload), tc.payload} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes, Context: "unnamed portal parameter $1"}, arg)
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
					}
				}
			}()
		}
	}
	p.Step(t, fixture.Select("SELECT t,c,v,u FROM inputs", []string{"t", "c", "v", "u"}, []string{"TEXT", "BPCHAR", "VARCHAR", "BPCHAR"}, nil))
	p.Step(t, fixture.Select("SELECT v FROM unlimited", []string{"v"}, []string{"VARCHAR"}, nil))
}

func TestReferenceCharacterUtilitySemanticStages(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct{ decl, code, message string }{
		{"char(0)", "22023", "length for type char must be at least 1"},
		{"varchar(0)", "22023", "length for type varchar must be at least 1"},
		{"char(10485761)", "22023", "length for type char cannot exceed 10485760"},
		{"varchar(10485761)", "22023", "length for type varchar cannot exceed 10485760"},
		{"bpchar(-1)", "22023", "length for type char must be at least 1"},
		{`"varchar"(-1)`, "22023", "length for type varchar must be at least 1"},
		{"text(3)", "42601", `type modifier is not allowed for type "text"`},
	} {
		for _, shape := range []struct{ table, position string }{{"c", "25"}, {"invalid", "31"}} {
			q := "/*é🙂*/ CREATE TABLE " + shape.table + "(v " + tc.decl + ")"
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: shape.position})
				s.Prepared, s.ErrorStage = prepared, "execute"
				p.Step(t, s)
			}
		}
	}
}

func TestReferenceCharacterSQLZeroEscapes(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE inputs(t TEXT)", 0))
	for _, tc := range []struct {
		query string
		want  fixture.Diagnostic
	}{
		{`INSERT INTO inputs(t) VALUES(E'\000')`, fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`}},
		{`INSERT INTO inputs(t) VALUES(U&'\0000')`, fixture.Diagnostic{Code: "42601", Message: "invalid Unicode escape value", Position: "33"}},
	} {
		s := fixture.ImplicitError(tc.query, tc.want)
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	p.Step(t, fixture.Select("SELECT t FROM inputs", []string{"t"}, []string{"TEXT"}, nil))
}
