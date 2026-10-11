package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestAlterAddConstraintParserValidity(t *testing.T) {
	cases := []struct {
		sql     string
		kind    AlterTableActionKind
		invalid bool
	}{
		{`ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID`, AddCheck, true},
		{`ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID`, AddNotNull, true},
		{`ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(a) MATCH FULL NOT VALID`, AddForeignKey, true},
		{`ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID NO INHERIT NO INHERIT NOT VALID`, AddCheck, true},
		{`ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID NOT VALID`, AddNotNull, true},
		{`ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(a) NOT VALID NOT VALID`, AddForeignKey, true},
		{`ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NO INHERIT NOT VALID`, AddCheck, true},
		{`ALTER TABLE t ADD CONSTRAINT key UNIQUE NULLS NOT DISTINCT(a,b)`, AddUnique, false},
		{`ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY(a,b)`, AddUnique, false},
		{`ALTER TABLE t ADD CONSTRAINT ex EXCLUDE USING hash(a WITH =)`, AddExclusion, false},
	}
	for _, tc := range cases {
		statements, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		action := statements[0].(*AlterTable).Actions[0]
		invalid := action.NotValid
		if action.Check != nil {
			invalid = action.Check.NotValid
		}
		if action.ForeignKey != nil {
			invalid = action.ForeignKey.NotValid
		}
		if action.Action != tc.kind || invalid != tc.invalid {
			t.Fatal(tc.sql, action)
		}
		if tc.invalid && action.ValidityPos.Character != strings.Index(tc.sql, "NOT VALID")+1 {
			t.Fatal("lost modifier position", action)
		}
		if _, err := Parse(`ALTER TABLE other ADD CHECK(b<0)`); err != nil {
			t.Fatal(err)
		}
		// Parser scratch reuse must not mutate the retained payload or positions.
		if action.Action != tc.kind || action.Constraint.Text == "" {
			t.Fatal("retained attachment mutated", action)
		}
	}
	for _, kind := range []string{"UNIQUE(a)", "PRIMARY KEY(a)", "EXCLUDE(a WITH =)"} {
		q := `ALTER TABLE "é" ADD CONSTRAINT key ` + kind + " NOT VALID"
		_, err := Parse(q)
		var got *sqlstate.Error
		label := strings.Split(kind, "(")[0]
		if strings.HasPrefix(kind, "EXCLUDE") {
			label = "EXCLUDE"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: label + " constraints cannot be marked NOT VALID", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "NOT VALID")]) + 1)}
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatal(q, got, want, err)
		}
	}
	for _, q := range []string{
		"ALTER TABLE t ADD CHECK(a>0) NOT VALID nonsense",
		"ALTER TABLE t ADD FOREIGN KEY(a) REFERENCES p(a) NOT VALID nonsense",
		"ALTER TABLE t ADD NOT NULL a NOT VALID nonsense",
		"ALTER TABLE t ADD UNIQUE(a) NOT VALID nonsense",
	} {
		_, err := Parse(q)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42601" || got.Position != strconv.Itoa(strings.Index(q, "nonsense")+1) {
			t.Fatal("incomplete input or masked syntax", q, got, err)
		}
	}
}
