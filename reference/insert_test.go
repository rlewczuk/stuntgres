//go:build reference

package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertImplicitShortTargets(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (1)", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 2, "20")))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1)", 1, "5")))
	p.step(t, referenceExec("INSERT INTO t VALUES ($1)", 1, []byte("6")))
	p.step(t, referenceExec("CREATE TABLE repeats(a INT,b INT,tail TEXT)", 0))
	p.step(t, referencePrepared(referenceExec("INSERT INTO repeats VALUES ($1,$1),($2,$2)", 2, 11, 12)))
	p.step(t, referenceExec("INSERT INTO t VALUES (3,30,'three')", 1))
	p.step(t, referenceExec("INSERT INTO t(label,id) VALUES ('reordered',4)", 1))
	p.step(t, referenceExec("INSERT INTO t VALUES (7,70,'full',TRUE)", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES (8),(9)", 2)))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t(label,id) VALUES ('ten',10),('eleven',11)", 2)))
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{
		{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), int64(30), "three", nil}, {int64(4), nil, "reordered", nil}, {int64(5), nil, nil, nil}, {int64(6), nil, nil, nil}, {int64(7), int64(70), "full", true}, {int64(8), nil, nil, nil}, {int64(9), nil, nil, nil}, {int64(10), nil, "ten", nil}, {int64(11), nil, "eleven", nil},
	}))
	p.step(t, referencePrepared(referenceSelect("SELECT * FROM t WHERE id = $1", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(2), int64(20), nil, nil}}, 2)))
	p.step(t, referenceSelect("SELECT * FROM repeats ORDER BY a", []string{"a", "b", "tail"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(11), int64(11), nil}, {int64(12), int64(12), nil}}))
	p.step(t, referenceExec(`CREATE TABLE "Odd"("Id" INT NOT NULL,label TEXT)`, 0))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "Odd" VALUES ($1)`, 1, "7")))
	p.step(t, referenceSelect(`SELECT * FROM "Odd" ORDER BY "Id"`, []string{"Id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}))
}

func TestReferenceInsertImplicitShortQuotedDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec(`CREATE TABLE "t""é"("a""b" INT NOT NULL,label TEXT,flag BOOL)`, 0))
	p.step(t, referenceExec(`INSERT INTO "t""é" VALUES(99,'sentinel',TRUE)`, 1))
	for _, tc := range []struct {
		query, code, message, hint, token string
	}{
		{`INSERT INTO "t""é" VALUES(TRUE)`, "42804", `column "a"b" is of type integer but expression is of type boolean`, "You will need to rewrite or cast the expression.", "TRUE"},
		{`INSERT INTO "t""é"("a""b","a""b") VALUES(1,2)`, "42701", `column "a"b" specified more than once`, "", `"a""b"`},
		{`INSERT INTO "t""é"("m""issing") VALUES(1)`, "42703", `column "m"issing" of relation "t"é" does not exist`, "", `"m""issing"`},
	} {
		position := referencePosition(utf8.RuneCountInString(tc.query[:strings.LastIndex(tc.query, tc.token)]) + 1)
		step := referenceImplicitError(tc.query, referenceDiagnostic{
			Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position,
		})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referenceSelect(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}}))
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}}))
	}
}

func TestReferenceInsertImplicitShortDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	if _, err := p.native.ExecContext(p.ctx, "CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.native.ExecContext(p.ctx, "INSERT INTO t VALUES(1),(2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.native.ExecContext(p.ctx, "INSERT INTO t(label,id) VALUES('reordered',3)"); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatalf("execute native dump on PostgreSQL: %v\\n%s", err, dump.String())
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{
		{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "reordered", nil},
	}))
}
