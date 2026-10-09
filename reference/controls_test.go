package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceDumpRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := newReferenceScenario(t)
	catalogControlSQLWitness(t, p)
	d := p.nativeConnector.Driver().(*stuntgres.Driver)
	db := catalogControlPool(t, d, "reference/"+t.Name())
	other := catalogControlPool(t, d, "reference/"+t.Name())
	if err := other.Ping(); err != nil {
		t.Fatal(err)
	}
	catalogControlExec(t, db, `CREATE TABLE "odd""table" ("id" INT NOT NULL, "text" TEXT, "flag" BOOL)`)
	catalogControlExec(t, db, `CREATE TABLE "empty" ("big" BIGINT)`)
	catalogControlExec(t, db, `INSERT INTO "odd""table" VALUES ($1,$2,$3),(1,NULL,NULL),(2,'',FALSE)`, 0, "line\nO'Brien\\end", false)
	catalogControlExec(t, db, `CREATE TABLE public."a.b" ("q""x" INTEGER, note TEXT)`)
	catalogControlExec(t, db, `INSERT INTO public."a.b" VALUES (9,'round trip')`)

	for _, q := range []string{
		`CREATE TABLE "Empty" ("spare" BIGINT)`,
		`CREATE TABLE "Round" ("id" INTEGER NOT NULL, "a""b" TEXT, "flag" BOOLEAN, "big" BIGINT)`,
		`INSERT INTO "Round" VALUES (0, '', FALSE, 0), (1, NULL, NULL, NULL), (2, 'first
O''Brien\last', TRUE, -9223372036854775808)`,
		`CREATE TABLE public."legacy.a.b" ("q""x" INTEGER, note TEXT)`,
		`INSERT INTO public."legacy.a.b" VALUES (9, 'qualified')`,
	} {
		catalogControlExec(t, db, q)
	}
	query := `SELECT "id","text","flag" FROM "odd""table" ORDER BY "id"`
	want := [][]any{{int64(0), "line\nO'Brien\\end", false}, {int64(1), nil, nil}, {int64(2), "", false}}
	catalogControlTuples(t, other, `SELECT "q""x",note FROM public."a.b"`, [][]any{{int64(9), "round trip"}})
	var first, second bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &first); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(ctx, db, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("dump was not stable")
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	_, err := other.Query(query)
	referenceState(t, err, "42P01")
	if err = stuntgres.Restore(ctx, db, strings.NewReader(first.String())); err != nil {
		t.Fatal(err)
	}
	catalogControlTuples(t, other, query, want)
	catalogControlTuples(t, other, `SELECT "q""x",note FROM public."a.b"`, [][]any{{int64(9), "round trip"}})
	catalogControlTuples(t, other, `SELECT big FROM empty`, [][]any{})
	var restored bytes.Buffer
	if err = stuntgres.Dump(ctx, other, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.String() != first.String() {
		t.Fatalf("round trip dump changed:\n%s\n%s", first.String(), restored.String())
	}

	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, first.String()); err != nil {
			t.Fatal(err)
		}
	}
	p.step(t, referenceSelect(`SELECT * FROM "Empty"`, []string{"spare"}, []string{"INT8"}, nil))
	p.step(t, referenceSelect(`SELECT * FROM "Round" ORDER BY id`, []string{"id", `a"b`, "flag", "big"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, [][]any{
		{int64(0), "", false, int64(0)}, {int64(1), nil, nil, nil}, {int64(2), "first\nO'Brien\\last", true, int64(-9223372036854775808)},
	}))
	p.step(t, referenceSelect(query, []string{"id", "text", "flag"}, []string{"INT4", "TEXT", "BOOL"}, want))
	p.step(t, referenceSelect(`SELECT "q""x",note FROM public."a.b"`, []string{`q"x`, "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "round trip"}}))
	p.step(t, referenceSelect(`SELECT "q""x",note FROM public."legacy.a.b"`, []string{`q"x`, "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "qualified"}}))
	stmt, err := other.Prepare(`SELECT id FROM "odd""table" ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if err = stuntgres.Restore(ctx, db, strings.NewReader(" -- empty\n /* nested /* yes */ */ ")); err != nil {
		t.Fatal(err)
	}
	_, err = stmt.Query()
	referenceState(t, err, "42P01")
	var empty bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &empty); err != nil {
		t.Fatal(err)
	}
	if empty.String() != "" {
		t.Fatalf("empty dump: %q", empty.String())
	}
}
