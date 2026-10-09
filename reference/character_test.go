package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCharacterValuesAndCasts(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(id INT,c CHARACTER(3),v CHARACTER VARYING(2),t TEXT,u BPCHAR,w VARCHAR)", 0))
	p.step(t, referenceExec("INSERT INTO c VALUES(1,'é🙂','é🙂','a  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", 3))
	p.step(t, referencePrepared(referenceExec("INSERT INTO c VALUES($1,$2,$3,$4,$5,$6)", 1, 4, []byte("e\u0301"), "e\u0301", "tail  ", "tail  ", "tail  ")))
	p.step(t, referenceSelect("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "t", "u", "w"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), "é🙂 ", "é🙂", "a  ", "a  ", "a  "}, {int64(2), "   ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}, {int64(4), "e\u0301 ", "e\u0301", "tail  ", "tail  ", "tail  "}}))
	p.step(t, referenceExec("CREATE TABLE owned(id INT,c CHAR(3),v VARCHAR(2),t TEXT,u BPCHAR,w VARCHAR)", 0))
	p.step(t, referenceExec("INSERT INTO owned VALUES(1,'é🙂','é🙂','a  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", 3))
	p.step(t, referenceSelect("SELECT * FROM owned ORDER BY id", []string{"id", "c", "v", "t", "u", "w"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), "é🙂 ", "é🙂", "a  ", "a  ", "a  "}, {int64(2), "   ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}}))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO owned VALUES($1,$2,$3,$4,$5,$6)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			source := []byte("e\u0301")
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 4, source, source, source, source, source))
			source[0] = 'X'
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT c,v,t,u,w FROM owned WHERE id=4", []string{"c", "v", "t", "u", "w"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, [][]any{{"e\u0301 ", "e\u0301", "e\u0301", "e\u0301", "e\u0301"}}))
		}()
	}
	p.step(t, referenceExec("CREATE TABLE casted(id INT,c CHAR(5),v VARCHAR(2),t TEXT,u BPCHAR)", 0))
	for _, prepared := range []bool{false, true} {
		s := referenceExec("INSERT INTO casted VALUES(1,CAST('too long' AS char(5)),'é🙂x'::varchar(2),CAST(CAST('a ' AS char(2)) AS text),CAST('ab  ' AS bpchar))", 1)
		if prepared {
			s = referencePrepared(s)
		}
		p.step(t, s)
	}
	casted := referenceSelect("SELECT * FROM casted", []string{"id", "c", "v", "t", "u"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, [][]any{{int64(1), "too l", "é🙂", "a", "ab  "}, {int64(1), "too l", "é🙂", "a", "ab  "}})
	casted.bag = true
	p.step(t, casted)
	p.step(t, referencePrepared(referenceExec("INSERT INTO casted VALUES(2,CAST($1 AS char(5)),CAST($2 AS varchar(2)),NULL,NULL)", 1, "ok", "é🙂x")))
	p.step(t, referenceSelect("SELECT c,v,t,u FROM casted WHERE id=2", []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, [][]any{{"ok   ", "é🙂", nil, nil}}))
}

func TestReferenceCharacterAssignmentAndAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(c CHAR(5),v VARCHAR(5))", 0))
	for _, value := range []string{"good      ", "good\t", "abcde   "} {
		p.step(t, referenceExec("INSERT INTO c VALUES($1,$2)", 1, value, value))
		p.step(t, referencePrepared(referenceExec("INSERT INTO c VALUES($1,$2)", 1, []byte(value), []byte(value))))
	}
	unchanged := referenceSelect("SELECT * FROM c", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, [][]any{{"good ", "good "}, {"good ", "good "}, {"good\t", "good\t"}, {"good\t", "good\t"}, {"abcde", "abcde"}, {"abcde", "abcde"}})
	unchanged.bag = true
	p.step(t, unchanged)
	for _, column := range []string{"c", "v"} {
		name := "character(5)"
		if column == "v" {
			name = "character varying(5)"
		}
		for _, value := range []string{"abcde\n", "abcde\r", "abcde\u00a0", "abcde\u3000", "good \t"} {
			s := referenceImplicitError("INSERT INTO c("+column+") VALUES($1)", referenceDiagnostic{Code: "22001", Message: "value too long for type " + name}, value)
			s.errorStage = "execute"
			p.step(t, s)
			p.step(t, referencePrepared(s))
			p.step(t, unchanged)
		}
		s := referenceImplicitError("INSERT INTO c("+column+") VALUES('ok'),('overlong')", referenceDiagnostic{Code: "22001", Message: "value too long for type " + name})
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
		p.step(t, unchanged)
		s = referenceImplicitError("INSERT INTO c("+column+") VALUES(CAST('abcdef' AS text))", referenceDiagnostic{Code: "22001", Message: "value too long for type " + name})
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
		p.step(t, unchanged)
	}
	for _, declaration := range []struct{ sql, typ, name string }{{"char(5)", "BPCHAR", "character(5)"}, {"varchar(5)", "VARCHAR", "character varying(5)"}} {
		p.step(t, referenceExec("CREATE TABLE single(v "+declaration.sql+")", 0))
		for _, value := range []string{"good      ", "good\t", "abcde   "} {
			p.step(t, referenceExec("INSERT INTO single VALUES($1)", 1, value))
			p.step(t, referencePrepared(referenceExec("INSERT INTO single VALUES($1)", 1, []byte(value))))
		}
		bag := referenceSelect("SELECT v FROM single", []string{"v"}, []string{declaration.typ}, [][]any{{"good "}, {"good "}, {"good\t"}, {"good\t"}, {"abcde"}, {"abcde"}})
		bag.bag = true
		p.step(t, bag)
		for _, value := range []string{"good \t", "abcde\n", "abcde\r", "abcde\u00a0", "abcde\u3000"} {
			failure := referenceImplicitError("INSERT INTO single VALUES($1)", referenceDiagnostic{Code: "22001", Message: "value too long for type " + declaration.name}, value)
			failure.errorStage = "execute"
			p.step(t, referencePrepared(failure))
			p.step(t, bag)
		}
		for _, q := range []string{"INSERT INTO single VALUES('ok'),('overlong')", "INSERT INTO single VALUES(CAST('abcdef' AS text))"} {
			failure := referenceImplicitError(q, referenceDiagnostic{Code: "22001", Message: "value too long for type " + declaration.name})
			failure.errorStage = "execute"
			p.step(t, referencePrepared(failure))
			p.step(t, bag)
		}
		p.step(t, referenceExec("DROP TABLE single", 0))
	}
}

func TestReferenceCharacterEqualityAndOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(c CHAR(3),d CHAR(5),v VARCHAR(5),t TEXT,u BPCHAR)", 0))
	p.step(t, referenceExec("INSERT INTO c VALUES('a','a','a ','a ','a  ')", 1))
	// Mixed bpchar/varchar equality is confirmed independently by root's pinned18 capture.
	for _, predicate := range []string{"c=v", "v=c", "u=v", "v=u"} {
		s := referenceSelect("SELECT c FROM c WHERE "+predicate, []string{"c"}, []string{"BPCHAR"}, [][]any{{"a  "}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct {
		predicate string
		match     bool
	}{{"c=d", true}, {"d=c", true}, {"c='a    '", true}, {"'a    '=c", true}, {"c=t", false}, {"t=c", false}, {"c=CAST('a' AS text)", true}, {"CAST('a' AS text)=c", true}, {"c=CAST('a ' AS text)", false}, {"CAST('a ' AS text)=c", false}, {"v='a'", false}, {"t='a'", false}, {"c=NULL", false}, {"u='a'", true}} {
		t.Run(tc.predicate, func(t *testing.T) {
			var want [][]any
			if tc.match {
				want = [][]any{{"a  "}}
			}
			s := referenceSelect("SELECT c FROM c WHERE "+tc.predicate, []string{"c"}, []string{"BPCHAR"}, want)
			p.step(t, s)
			p.step(t, referencePrepared(s))
		})
	}
	for _, predicate := range []string{"c=d", "d=c", "c=t", "t=c", "c=v", "v=c", "u=v", "v=u", "c='a    '", "'a    '=c", "c=CAST('a ' AS text)", "CAST('a ' AS text)=c", "v='a'", "t='a'"} {
		var want [][]any
		if predicate == "c=d" || predicate == "d=c" || predicate == "c=v" || predicate == "v=c" || predicate == "u=v" || predicate == "v=u" || predicate == "c='a    '" || predicate == "'a    '=c" {
			want = [][]any{{"a  ", "a ", "a ", "a  "}}
		}
		p.step(t, referenceSelect("SELECT c,v,t,u FROM c WHERE "+predicate, []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, want))
	}
	p.step(t, referenceExec("CREATE TABLE ordered(v BPCHAR)", 0))
	p.step(t, referenceExec("INSERT INTO ordered VALUES('b '),('a\n'),('a ')", 3))
	p.step(t, referenceSelect("SELECT v FROM ordered ORDER BY v", []string{"v"}, []string{"BPCHAR"}, [][]any{{"a "}, {"a\n"}, {"b "}}))
	p.step(t, referenceExec("CREATE TABLE spaces(c BPCHAR,v VARCHAR,t TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO spaces VALUES('   ','   ','   ')", 1))
	p.step(t, referenceSelect("SELECT c FROM spaces WHERE c=''", []string{"c"}, []string{"BPCHAR"}, [][]any{{"   "}}))
	for _, column := range []struct{ name, typ string }{{"v", "VARCHAR"}, {"t", "TEXT"}} {
		p.step(t, referenceSelect("SELECT "+column.name+" FROM spaces WHERE "+column.name+"=''", []string{column.name}, []string{column.typ}, nil))
	}
}

func TestReferenceCharacterDumpReplay(t *testing.T) {
	p := newReferenceScenario(t)
	script := "CREATE TABLE c(id INT,c CHAR(4),v VARCHAR(20),u BPCHAR,w VARCHAR,t TEXT); CREATE TABLE empty(c CHAR(2),v VARCHAR(3)); INSERT INTO c VALUES(1,'é🙂','ok  ','O''Brien\n\\tail  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("native dump replay in PostgreSQL", err)
		}
	}
	p.step(t, referenceSelect("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "u", "w", "t"}, []string{"INT4", "BPCHAR", "VARCHAR", "BPCHAR", "VARCHAR", "TEXT"}, [][]any{{int64(1), "é🙂  ", "ok  ", "O'Brien\n\\tail  ", "a  ", "a  "}, {int64(2), "    ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}}))
	p.step(t, referenceSelect("SELECT * FROM empty", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, nil))
}
