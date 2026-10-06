//go:build reference

package reference_test

import "testing"

func TestReferenceCharacterEncodingDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE inputs(t TEXT,c CHAR(2),v VARCHAR(2),u BPCHAR)", 0))
	for _, column := range []string{"t", "c", "v", "u"} {
		for _, tc := range []struct {
			payload []byte
			bytes   string
		}{
			{[]byte{'a', 0, 'b'}, "0x00"}, {[]byte{0xff}, "0xff"}, {[]byte{0xc3, 0x28}, "0xc3 0x28"}, {[]byte{0xe2, 0x82}, "0xe2 0x82"},
		} {
			for _, arg := range []any{string(tc.payload), tc.payload} {
				s := referenceImplicitError("INSERT INTO inputs("+column+") VALUES($1)", referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes}, arg)
				pg := *s.diagnostic
				// The wire portal context is external-only, not fabricated natively.
				pg.Context = "unnamed portal parameter $1"
				s.postgresDiagnostic = &pg
				s.errorStage = "execute"
				p.step(t, s)
				p.step(t, referencePrepared(s))
			}
		}
	}
	p.step(t, referenceSelect("SELECT t,c,v,u FROM inputs", []string{"t", "c", "v", "u"}, []string{"TEXT", "BPCHAR", "VARCHAR", "BPCHAR"}, nil))
}

func TestReferenceCharacterUtilitySemanticStages(t *testing.T) {
	p := newReferencePair(t)
	// Both utility adapters accept Prepare and report the same semantic
	// validation record at Execute, preserving the metadata-only utility seam.
	for _, tc := range []struct{ decl, code, message string }{
		{"char(0)", "22023", "length for type char must be at least 1"},
		{"varchar(0)", "22023", "length for type varchar must be at least 1"},
		{"char(10485761)", "22023", "length for type char cannot exceed 10485760"},
		{"varchar(10485761)", "22023", "length for type varchar cannot exceed 10485760"},
		{"bpchar(-1)", "22023", "length for type char must be at least 1"},
		{`"varchar"(-1)`, "22023", "length for type varchar must be at least 1"},
		{"text(3)", "42601", `type modifier is not allowed for type "text"`},
	} {
		t.Run(tc.decl, func(t *testing.T) {
			q := "/*é🙂*/ CREATE TABLE invalid(v " + tc.decl + ")"
			s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: "31"})
			s = referencePrepared(s)
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
			s.prepared = false
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		})
	}
}

func TestReferenceCharacterSQLZeroEscapes(t *testing.T) {
	p := newReferencePair(t)
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
}
