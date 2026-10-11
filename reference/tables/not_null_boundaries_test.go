package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
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
					p := fixture.NewScenario(t)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) }
					run(fixture.Exec("CREATE TABLE keeper(v INT)", 0))
					run(fixture.Exec("INSERT INTO keeper VALUES(91)", 1))
					run(fixture.Exec("CREATE TABLE existing(a INT)", 0))
					run(fixture.Exec("INSERT INTO existing VALUES(51)", 1))
					q := prefix + tc.q
					position := utf8.RuneCountInString(q) + 1
					message, code := "syntax error at end of input", "42601"
					if tc.token != "" {
						position = utf8.RuneCountInString(q[:strings.LastIndex(q, tc.token)]) + 1
						message = `syntax error at or near "` + tc.token + `"`
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Position: strconv.Itoa(position)})
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					run(s)
					run(fixture.Select("SELECT * FROM keeper", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
					run(fixture.Select("SELECT * FROM existing", []string{"a"}, []string{"INT4"}, [][]any{{int64(51)}}))
					for _, name := range []string{"child", "échild", "prefix"} {
						absent := fixture.ImplicitError(`SELECT * FROM "`+name+`"`, fixture.Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
						absent.SelectRows = true
						run(absent)
					}
				})
			}
		}
	}
}

func TestReferenceNotNullPublicExcludedSyntaxAndQuotedDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec(`CREATE TABLE "t""é"("a""b\\tail" INT,s TEXT)`, 0))
	p.Step(t, fixture.Exec(`INSERT INTO "t""é" VALUES(NULL,'keep')`, 1))
	p.Step(t, fixture.ImplicitError(`ALTER TABLE "t""é" ALTER "a""b\\tail" SET NOT NULL`, fixture.Diagnostic{Code: "23502", Message: `column "a"b\\tail" of relation "t"é" contains null values`, Schema: "public", Table: `t"é`, Column: `a"b\\tail`}))
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	for _, tc := range []struct{ q, token, message string }{
		{"ALTER TABLE t ADD NOT NULL a NOT VALID", "NOT VALID", "NOT NULL modifiers are unsupported"},
		{"ALTER TABLE t VALIDATE CONSTRAINT nn", "VALIDATE", "ALTER TABLE action is unsupported"},
		{"CREATE TABLE noinherit(a INT NOT NULL NO INHERIT)", "NO INHERIT", "NOT NULL modifiers are unsupported"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			} else {
				s.ErrorStage = "execute"
			}
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
		}
	}
	if p.Postgres != nil {
		pg := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
		pg(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID", 0))
		pg(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT nn", 0))
		pg(fixture.Exec("CREATE TABLE noinherit(a INT NOT NULL NO INHERIT)", 0))
	}
	parentSentinel := func() {
		for _, b := range p.Backends() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	parentSentinel()
	p.Step(t, fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("UPDATE t SET a=NULL", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		p.Step(t, s)
	}
	parentSentinel()
	for _, b := range p.Backends() {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		_, err := b.DB.ExecContext(ctx, "ALTER TABLE t ALTER a SET NOT NULL")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
