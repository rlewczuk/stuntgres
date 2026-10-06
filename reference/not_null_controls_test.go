//go:build reference

package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceNotNullControls(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec(`CREATE TABLE t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
	text := "O'Brien\n\\tail"
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), text, true)))
	p.step(t, referenceExec("INSERT INTO t VALUES(8,NULL,NULL,NULL)", 1))
	p.step(t, referenceExec("CREATE TABLE generated(a INT NOT NULL)", 0))
	p.step(t, referenceExec("COMMENT ON TABLE t IS 'persistent'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CONSTRAINT "n""é\\tail" NOT NULL`) || !strings.Contains(dump.String(), `CONSTRAINT "generated_a_not_null" NOT NULL`) {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	expected := referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}})
	p.step(t, expected)
	p.step(t, referencePrepared(expected))
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String())
	}
	for _, tc := range []struct {
		script string
		want   referenceDiagnostic
	}{
		{"CREATE TABLE replacement(a INT CONSTRAINT nn NOT NULL);INSERT INTO replacement VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"}},
		{"CREATE TABLE replacement(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", referenceDiagnostic{Code: "42710", Message: `constraint "nn" for relation "replacement" already exists`}},
		{"CREATE TABLE replacement(a INT,NOT NULL missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "replacement" does not exist`}},
	} {
		err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.script))
		tc.want.Severity, tc.want.SeverityUnlocalized = "ERROR", "ERROR"
		referenceAssertDiagnostic(t, err, &tc.want)
		p.step(t, expected)
		var after bytes.Buffer
		if err = stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != dump.String() {
			t.Fatal(err)
		}
	}
	p.step(t, referencePrepared(referenceExec(`ALTER TABLE t DROP CONSTRAINT "n""é\\tail"`, 0)))
	p.step(t, referenceExec("ALTER TABLE generated DROP CONSTRAINT generated_a_not_null", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(NULL,NULL,NULL,NULL)", 1))
}
