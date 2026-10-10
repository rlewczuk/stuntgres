package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceNotNullInheritanceCompleteInputAndNoEffects(t *testing.T) {
	for _, tc := range []struct{ q, token string }{
		{"CREATE TABLE child(a INT) INHERITS()", ")"},
		{"CREATE TABLE child(a INT) INHERITS(parent", ""},
		{"CREATE TABLE child(a INT) INHERITS(parent,)", ")"},
		{"CREATE TABLE child(a INT) INHERITS(parent other)", "other"},
		{`CREATE TABLE child(a INT) INHERITS("s".)`, ")"},
		{"CREATE TABLE child(a INT) INHERITS(parent) garbage", "garbage"},
		{"CREATE TABLE child(a INT) INHERITS(parent) INHERITS(other)", "INHERITS"},
		{`CREATE TABLE "échild"(a INT) INHERITS("s"."p",other,)`, ")"},
	} {
		for _, prefix := range []string{"", "CREATE TABLE prefix(a INT); INSERT INTO keeper VALUES(92); "} {
			for _, prepared := range []bool{false, true} {
				t.Run(prefix+tc.q+strconv.FormatBool(prepared), func(t *testing.T) {
					p := newReferenceScenario(t)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
					run(referenceExec("CREATE TABLE keeper(v INT)", 0))
					run(referenceExec("INSERT INTO keeper VALUES(91)", 1))
					run(referenceExec("CREATE TABLE existing(a INT)", 0))
					run(referenceExec("INSERT INTO existing VALUES(51)", 1))
					q := prefix + tc.q
					position := utf8.RuneCountInString(q) + 1
					message, code := "syntax error at end of input", "42601"
					if tc.token != "" {
						position = utf8.RuneCountInString(q[:strings.LastIndex(q, tc.token)]) + 1
						message = `syntax error at or near "` + tc.token + `"`
					}
					s := referenceImplicitError(q, referenceDiagnostic{Code: code, Message: message, Position: strconv.Itoa(position)})
					s.prepared = prepared
					if prepared {
						s.errorStage = "prepare"
					} else {
						s.errorStage = "execute"
					}
					run(s)
					run(referenceSelect("SELECT * FROM keeper", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
					run(referenceSelect("SELECT * FROM existing", []string{"a"}, []string{"INT4"}, [][]any{{int64(51)}}))
					for _, name := range []string{"child", "échild", "prefix"} {
						absent := referenceImplicitError(`SELECT * FROM "`+name+`"`, referenceDiagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
						absent.selectRows = true
						run(absent)
					}
				})
			}
		}
	}
}

func TestReferenceNotNullPublicExcludedSyntaxAndQuotedDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec(`CREATE TABLE "t""é"("a""b\\tail" INT,s TEXT)`, 0))
	p.step(t, referenceExec(`INSERT INTO "t""é" VALUES(NULL,'keep')`, 1))
	p.step(t, referenceImplicitError(`ALTER TABLE "t""é" ALTER "a""b\\tail" SET NOT NULL`, referenceDiagnostic{Code: "23502", Message: `column "a"b\\tail" of relation "t"é" contains null values`, Schema: "public", Table: `t"é`, Column: `a"b\\tail`}))
	p.step(t, referenceExec("CREATE TABLE t(a INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	for _, tc := range []struct{ q, token, message string }{
		{"ALTER TABLE t ADD NOT NULL a NOT VALID", "NOT VALID", "NOT NULL modifiers are unsupported"},
		{"ALTER TABLE t VALIDATE CONSTRAINT nn", "VALIDATE", "ALTER TABLE action is unsupported"},
		{"CREATE TABLE noinherit(a INT NOT NULL NO INHERIT)", "NO INHERIT", "NOT NULL modifiers are unsupported"},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
			s.prepared = prepared
			if prepared {
				s.errorStage = "prepare"
			} else {
				s.errorStage = "execute"
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
		}
	}
	if p.postgres != nil {
		pg := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
		pg(referenceExec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID", 0))
		pg(referenceExec("ALTER TABLE t VALIDATE CONSTRAINT nn", 0))
		pg(referenceExec("CREATE TABLE noinherit(a INT NOT NULL NO INHERIT)", 0))
	}
	parentSentinel := func() {
		for _, b := range p.backends() {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	parentSentinel()
	p.step(t, referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError("UPDATE t SET a=NULL", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
		s.prepared, s.errorStage = prepared, "execute"
		p.step(t, s)
	}
	parentSentinel()
	for _, b := range p.backends() {
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		_, err := b.db.ExecContext(ctx, "ALTER TABLE t ALTER a SET NOT NULL")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
