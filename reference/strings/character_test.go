package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCharacterValuesAndCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE c(id INT,c CHARACTER(3),v CHARACTER VARYING(2),t TEXT,u BPCHAR,w VARCHAR)", 0))
	p.Step(t, fixture.Exec("INSERT INTO c VALUES(1,'é🙂','é🙂','a  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", 3))
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1,$2,$3,$4,$5,$6)", 1, 4, []byte("e\u0301"), "e\u0301", "tail  ", "tail  ", "tail  ")))
	p.Step(t, fixture.Select("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "t", "u", "w"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), "é🙂 ", "é🙂", "a  ", "a  ", "a  "}, {int64(2), "   ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}, {int64(4), "e\u0301 ", "e\u0301", "tail  ", "tail  ", "tail  "}}))
	p.Step(t, fixture.Exec("CREATE TABLE owned(id INT,c CHAR(3),v VARCHAR(2),t TEXT,u BPCHAR,w VARCHAR)", 0))
	p.Step(t, fixture.Exec("INSERT INTO owned VALUES(1,'é🙂','é🙂','a  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", 3))
	p.Step(t, fixture.Select("SELECT * FROM owned ORDER BY id", []string{"id", "c", "v", "t", "u", "w"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), "é🙂 ", "é🙂", "a  ", "a  ", "a  "}, {int64(2), "   ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}}))
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO owned VALUES($1,$2,$3,$4,$5,$6)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			source := []byte("e\u0301")
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 4, source, source, source, source, source))
			source[0] = 'X'
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT c,v,t,u,w FROM owned WHERE id=4", []string{"c", "v", "t", "u", "w"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, [][]any{{"e\u0301 ", "e\u0301", "e\u0301", "e\u0301", "e\u0301"}}))
		}()
	}
	p.Step(t, fixture.Exec("CREATE TABLE casted(id INT,c CHAR(5),v VARCHAR(2),t TEXT,u BPCHAR)", 0))
	for _, prepared := range []bool{false, true} {
		s := fixture.Exec("INSERT INTO casted VALUES(1,CAST('too long' AS char(5)),'é🙂x'::varchar(2),CAST(CAST('a ' AS char(2)) AS text),CAST('ab  ' AS bpchar))", 1)
		if prepared {
			s = fixture.Prepared(s)
		}
		p.Step(t, s)
	}
	casted := fixture.Select("SELECT * FROM casted", []string{"id", "c", "v", "t", "u"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, [][]any{{int64(1), "too l", "é🙂", "a", "ab  "}, {int64(1), "too l", "é🙂", "a", "ab  "}})
	casted.Bag = true
	p.Step(t, casted)
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO casted VALUES(2,CAST($1 AS char(5)),CAST($2 AS varchar(2)),NULL,NULL)", 1, "ok", "é🙂x")))
	p.Step(t, fixture.Select("SELECT c,v,t,u FROM casted WHERE id=2", []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, [][]any{{"ok   ", "é🙂", nil, nil}}))
}

func TestReferenceCharacterAssignmentAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE c(c CHAR(5),v VARCHAR(5))", 0))
	for _, value := range []string{"good      ", "good\t", "abcde   "} {
		p.Step(t, fixture.Exec("INSERT INTO c VALUES($1,$2)", 1, value, value))
		p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1,$2)", 1, []byte(value), []byte(value))))
	}
	unchanged := fixture.Select("SELECT * FROM c", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, [][]any{{"good ", "good "}, {"good ", "good "}, {"good\t", "good\t"}, {"good\t", "good\t"}, {"abcde", "abcde"}, {"abcde", "abcde"}})
	unchanged.Bag = true
	p.Step(t, unchanged)
	for _, column := range []string{"c", "v"} {
		name := "character(5)"
		if column == "v" {
			name = "character varying(5)"
		}
		for _, value := range []string{"abcde\n", "abcde\r", "abcde\u00a0", "abcde\u3000", "good \t"} {
			s := fixture.ImplicitError("INSERT INTO c("+column+") VALUES($1)", fixture.Diagnostic{Code: "22001", Message: "value too long for type " + name}, value)
			s.ErrorStage = "execute"
			p.Step(t, s)
			p.Step(t, fixture.Prepared(s))
			p.Step(t, unchanged)
		}
		s := fixture.ImplicitError("INSERT INTO c("+column+") VALUES('ok'),('overlong')", fixture.Diagnostic{Code: "22001", Message: "value too long for type " + name})
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
		p.Step(t, unchanged)
		s = fixture.ImplicitError("INSERT INTO c("+column+") VALUES(CAST('abcdef' AS text))", fixture.Diagnostic{Code: "22001", Message: "value too long for type " + name})
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
		p.Step(t, unchanged)
	}
	for _, declaration := range []struct{ sql, typ, name string }{{"char(5)", "BPCHAR", "character(5)"}, {"varchar(5)", "VARCHAR", "character varying(5)"}} {
		p.Step(t, fixture.Exec("CREATE TABLE single(v "+declaration.sql+")", 0))
		for _, value := range []string{"good      ", "good\t", "abcde   "} {
			p.Step(t, fixture.Exec("INSERT INTO single VALUES($1)", 1, value))
			p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO single VALUES($1)", 1, []byte(value))))
		}
		bag := fixture.Select("SELECT v FROM single", []string{"v"}, []string{declaration.typ}, [][]any{{"good "}, {"good "}, {"good\t"}, {"good\t"}, {"abcde"}, {"abcde"}})
		bag.Bag = true
		p.Step(t, bag)
		for _, value := range []string{"good \t", "abcde\n", "abcde\r", "abcde\u00a0", "abcde\u3000"} {
			failure := fixture.ImplicitError("INSERT INTO single VALUES($1)", fixture.Diagnostic{Code: "22001", Message: "value too long for type " + declaration.name}, value)
			failure.ErrorStage = "execute"
			p.Step(t, fixture.Prepared(failure))
			p.Step(t, bag)
		}
		for _, q := range []string{"INSERT INTO single VALUES('ok'),('overlong')", "INSERT INTO single VALUES(CAST('abcdef' AS text))"} {
			failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22001", Message: "value too long for type " + declaration.name})
			failure.ErrorStage = "execute"
			p.Step(t, fixture.Prepared(failure))
			p.Step(t, bag)
		}
		p.Step(t, fixture.Exec("DROP TABLE single", 0))
	}
}

func TestReferenceCharacterEqualityAndOrdering(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE c(c CHAR(3),d CHAR(5),v VARCHAR(5),t TEXT,u BPCHAR)", 0))
	p.Step(t, fixture.Exec("INSERT INTO c VALUES('a','a','a ','a ','a  ')", 1))
	// Mixed bpchar/varchar equality is confirmed independently by root's pinned18 capture.
	for _, predicate := range []string{"c=v", "v=c", "u=v", "v=u"} {
		s := fixture.Select("SELECT c FROM c WHERE "+predicate, []string{"c"}, []string{"BPCHAR"}, [][]any{{"a  "}})
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
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
			s := fixture.Select("SELECT c FROM c WHERE "+tc.predicate, []string{"c"}, []string{"BPCHAR"}, want)
			p.Step(t, s)
			p.Step(t, fixture.Prepared(s))
		})
	}
	for _, predicate := range []string{"c=d", "d=c", "c=t", "t=c", "c=v", "v=c", "u=v", "v=u", "c='a    '", "'a    '=c", "c=CAST('a ' AS text)", "CAST('a ' AS text)=c", "v='a'", "t='a'"} {
		var want [][]any
		if predicate == "c=d" || predicate == "d=c" || predicate == "c=v" || predicate == "v=c" || predicate == "u=v" || predicate == "v=u" || predicate == "c='a    '" || predicate == "'a    '=c" {
			want = [][]any{{"a  ", "a ", "a ", "a  "}}
		}
		p.Step(t, fixture.Select("SELECT c,v,t,u FROM c WHERE "+predicate, []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, want))
	}
	p.Step(t, fixture.Exec("CREATE TABLE ordered(v BPCHAR)", 0))
	p.Step(t, fixture.Exec("INSERT INTO ordered VALUES('b '),('a\n'),('a ')", 3))
	p.Step(t, fixture.Select("SELECT v FROM ordered ORDER BY v", []string{"v"}, []string{"BPCHAR"}, [][]any{{"a "}, {"a\n"}, {"b "}}))
	p.Step(t, fixture.Exec("CREATE TABLE spaces(c BPCHAR,v VARCHAR,t TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO spaces VALUES('   ','   ','   ')", 1))
	p.Step(t, fixture.Select("SELECT c FROM spaces WHERE c=''", []string{"c"}, []string{"BPCHAR"}, [][]any{{"   "}}))
	for _, column := range []struct{ name, typ string }{{"v", "VARCHAR"}, {"t", "TEXT"}} {
		p.Step(t, fixture.Select("SELECT "+column.name+" FROM spaces WHERE "+column.name+"=''", []string{column.name}, []string{column.typ}, nil))
	}
}

func TestReferenceCharacterDumpReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	script := "CREATE TABLE c(id INT,c CHAR(4),v VARCHAR(20),u BPCHAR,w VARCHAR,t TEXT); CREATE TABLE empty(c CHAR(2),v VARCHAR(3)); INSERT INTO c VALUES(1,'é🙂','ok  ','O''Brien\n\\tail  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("native dump replay in PostgreSQL", err)
		}
	}
	p.Step(t, fixture.Select("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "u", "w", "t"}, []string{"INT4", "BPCHAR", "VARCHAR", "BPCHAR", "VARCHAR", "TEXT"}, [][]any{{int64(1), "é🙂  ", "ok  ", "O'Brien\n\\tail  ", "a  ", "a  "}, {int64(2), "    ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}}))
	p.Step(t, fixture.Select("SELECT * FROM empty", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, nil))
}
