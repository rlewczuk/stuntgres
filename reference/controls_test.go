//go:build reference

package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	for _, query := range []string{
		`CREATE TABLE "Empty" ("spare" BIGINT)`,
		`CREATE TABLE "Round" ("id" INTEGER NOT NULL, "a""b" TEXT, "flag" BOOLEAN, "big" BIGINT)`,
		`INSERT INTO "Round" VALUES (0, '', FALSE, 0), (1, NULL, NULL, NULL), (2, 'first
O''Brien\last', TRUE, -9223372036854775808)`,
		`CREATE TABLE public."a.b" ("q""x" INTEGER, note TEXT)`,
		`INSERT INTO public."a.b" VALUES (9, 'qualified')`,
	} {
		if _, err := p.memory.ExecContext(p.ctx, query); err != nil {
			t.Fatal(err)
		}
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
	// PostgreSQL executes the exact memory-produced script, not reconstructed fixtures.
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect(`SELECT * FROM "Empty"`, []string{"spare"}, []string{"INT8"}, nil))
	p.step(t, referenceSelect(`SELECT * FROM "Round" ORDER BY id`, []string{"id", `a"b`, "flag", "big"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, [][]any{
		{int64(0), "", false, int64(0)}, {int64(1), nil, nil, nil}, {int64(2), "first\nO'Brien\\last", true, int64(-9223372036854775808)},
	}))
	p.step(t, referenceSelect(`SELECT "q""x",note FROM public."a.b"`, []string{`q"x`, "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "qualified"}}))
	var restored bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), restored.Bytes()) {
		t.Fatalf("dump changed across restore:\nbefore:\n%s\nafter:\n%s", dump.String(), restored.String())
	}
}
