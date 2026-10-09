package reference_test

import "testing"

func TestReferenceCharacterEncodingDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE inputs(t TEXT,c CHAR(2),v VARCHAR(2),u BPCHAR)", 0))
	p.step(t, referenceExec("CREATE TABLE unlimited(v VARCHAR)", 0))
	for _, column := range []string{"t", "c", "v", "u", "unlimited"} {
		q := "INSERT INTO inputs(" + column + ") VALUES($1)"
		if column == "unlimited" {
			q = "INSERT INTO unlimited(v) VALUES($1)"
		}
		for _, backend := range p.backends() {
			func() {
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
				for _, tc := range []struct {
					payload []byte
					bytes   string
				}{
					{[]byte{'a', 0, 'b'}, "0x00"}, {[]byte{0xff}, "0xff"}, {[]byte{0xc3, 0x28}, "0xc3 0x28"}, {[]byte{0xe2, 0x82}, "0xe2 0x82"},
				} {
					for _, arg := range []any{string(tc.payload), tc.payload} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes, Context: "unnamed portal parameter $1"}, arg)
						s.errorStage = "execute"
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						referenceRunStep(t, p.ctx, held, backend.recorder, s)
					}
				}
			}()
		}
	}
	p.step(t, referenceSelect("SELECT t,c,v,u FROM inputs", []string{"t", "c", "v", "u"}, []string{"TEXT", "BPCHAR", "VARCHAR", "BPCHAR"}, nil))
	p.step(t, referenceSelect("SELECT v FROM unlimited", []string{"v"}, []string{"VARCHAR"}, nil))
}

func TestReferenceCharacterUtilitySemanticStages(t *testing.T) {
	p := newReferenceScenario(t)
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
				s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: shape.position})
				s.prepared, s.errorStage = prepared, "execute"
				p.step(t, s)
			}
		}
	}
}

func TestReferenceCharacterSQLZeroEscapes(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE inputs(t TEXT)", 0))
	for _, tc := range []struct {
		query string
		want  referenceDiagnostic
	}{
		{`INSERT INTO inputs(t) VALUES(E'\000')`, referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`}},
		{`INSERT INTO inputs(t) VALUES(U&'\0000')`, referenceDiagnostic{Code: "42601", Message: "invalid Unicode escape value", Position: "33"}},
	} {
		s := referenceImplicitError(tc.query, tc.want)
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceSelect("SELECT t FROM inputs", []string{"t"}, []string{"TEXT"}, nil))
}
