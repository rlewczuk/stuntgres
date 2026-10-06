//go:build reference

package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCharacterValuesAndCasts(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE c(id INT,c CHARACTER(3),v CHARACTER VARYING(2),t TEXT,u BPCHAR,w VARCHAR)", 0))
	p.step(t, referenceExec("INSERT INTO c VALUES(1,'é🙂','é🙂','a  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", 3))
	p.step(t, referencePrepared(referenceExec("INSERT INTO c VALUES($1,$2,$3,$4,$5,$6)", 1, 4, []byte("e\u0301"), "e\u0301", "tail  ", "tail  ", "tail  ")))
	p.step(t, referenceSelect("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "t", "u", "w"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), "é🙂 ", "é🙂", "a  ", "a  ", "a  "}, {int64(2), "   ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}, {int64(4), "e\u0301 ", "e\u0301", "tail  ", "tail  ", "tail  "}}))
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
	p := newReferencePair(t)
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
	}
	p.step(t, referenceExec("CREATE TABLE widths(a CHAR(2),b CHAR(4))", 0))
	p.step(t, referencePrepared(referenceExec("INSERT INTO widths VALUES($1,$1)", 1, "a")))
	p.step(t, referencePrepared(referenceExec("INSERT INTO widths VALUES($1,$1)", 1, nil)))
	widths := referenceSelect("SELECT * FROM widths", []string{"a", "b"}, []string{"BPCHAR", "BPCHAR"}, [][]any{{"a ", "a   "}, {nil, nil}})
	widths.bag = true
	p.step(t, widths)
	s := referenceImplicitError("INSERT INTO widths(a) VALUES(CAST('abcd' AS varchar(3)))", referenceDiagnostic{Code: "22001", Message: "value too long for type character(2)"})
	s.errorStage = "execute"
	p.step(t, referencePrepared(s))
}

func TestReferenceCharacterEqualityAndOrdering(t *testing.T) {
	p := newReferencePair(t)
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
	}{{"c=d", true}, {"d=c", true}, {"c='a    '", true}, {"'a    '=c", true}, {"c=t", false}, {"t=c", false}, {"c=CAST('a' AS text)", true}, {"CAST('a' AS text)=c", true}, {"c=CAST('a ' AS text)", false}, {"CAST('a ' AS text)=c", false}, {"v='a'", false}, {"t='a'", false}, {"c=NULL", false}} {
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
	p.step(t, referenceExec("CREATE TABLE ordered(v BPCHAR)", 0))
	p.step(t, referenceExec("INSERT INTO ordered VALUES('b '),('a\n'),('a ')", 3))
	p.step(t, referenceSelect("SELECT v FROM ordered ORDER BY v", []string{"v"}, []string{"BPCHAR"}, [][]any{{"a "}, {"a\n"}, {"b "}}))
}

func TestReferenceCharacterDumpReplay(t *testing.T) {
	p := newReferencePair(t)
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
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("native dump replay in PostgreSQL", err)
	}
	p.step(t, referenceSelect("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "u", "w", "t"}, []string{"INT4", "BPCHAR", "VARCHAR", "BPCHAR", "VARCHAR", "TEXT"}, [][]any{{int64(1), "é🙂  ", "ok  ", "O'Brien\n\\tail  ", "a  ", "a  "}, {int64(2), "    ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}}))
	p.step(t, referenceSelect("SELECT * FROM empty", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, nil))
}
