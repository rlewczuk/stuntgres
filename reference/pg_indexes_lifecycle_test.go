package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func pgIndexesRetained(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, pg bool, want []any) {
	t.Helper()
	defer rows.Close()
	pgIndexesColumnMetadata(t, types, pg)
	if !rows.Next() {
		t.Fatal("retained row missing", rows.Err())
	}
	got := make([]any, 5)
	dest := make([]any, 5)
	for i := range dest {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		referenceAssertRawValue(t, pg, pgIndexesTypes[i], v)
		got[i] = referenceNormalize(v)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cardinality", rows.Err())
	}
	pgIndexesColumnMetadata(t, types, pg)
}
func pgIndexesLifecycleScenario(t *testing.T, p *referencePair, b referenceBackend) {
	db := b.db
	run := func(q string, n int64) { referenceRunStep(t, p.ctx, db, b.recorder, referenceExec(q, n)) }
	stmt, err := db.PrepareContext(p.ctx, pgIndexesProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: db, statement: stmt, query: pgIndexesProbe}
	check := func(row []any) {
		pgIndexesCheck(t, p, b, db, "pgi_plain", row)
		pgIndexesCheck(t, p, b, held, "pgi_plain", row)
	}
	check(nil)
	run("CREATE TABLE pgi_table(n INT,s TEXT,p TEXT)", 0)
	run("CREATE INDEX pgi_plain ON pgi_table(n)", 0)
	original := pgIndexesRow("public", "pgi_table", "pgi_plain", "CREATE INDEX pgi_plain ON public.pgi_table USING btree (n)")
	check(original)
	run("INSERT INTO pgi_table VALUES(1,'s','p')", 1)
	run("UPDATE pgi_table SET n=2", 1)
	run("REINDEX INDEX pgi_plain", 0)
	check(original)
	for _, arg := range []any{nil, "pgi_missing"} {
		referenceRunStep(t, p.ctx, held, b.recorder, pgIndexesSelect(b, pgIndexesProbe, nil, arg))
	}
	var old *sql.Rows
	var oldTypes []*sql.ColumnType
	if !b.pg {
		old, err = db.QueryContext(p.ctx, pgIndexesProbe, "pgi_plain")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		oldTypes, err = old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgIndexesColumnMetadata(t, oldTypes, false)
	} else {
		// Materialize the PG result before DDL; never assume pq Query is an eager capture.
		referenceRunStep(t, p.ctx, held, b.recorder, pgIndexesSelect(b, pgIndexesProbe, [][]any{original}, "pgi_plain"))
	}
	run("ALTER TABLE pgi_table RENAME COLUMN n TO renamed_n", 0)
	run("ALTER TABLE pgi_table RENAME TO pgi_renamed_table", 0)
	run("ALTER INDEX pgi_plain RENAME TO pgi_renamed_index", 0)
	check(nil)
	renamed := pgIndexesRow("public", "pgi_renamed_table", "pgi_renamed_index", "CREATE INDEX pgi_renamed_index ON public.pgi_renamed_table USING btree (renamed_n)")
	pgIndexesCheck(t, p, b, held, "pgi_renamed_index", renamed)
	run("DROP INDEX pgi_renamed_index", 0)
	pgIndexesCheck(t, p, b, held, "pgi_renamed_index", nil)
	run("CREATE INDEX pgi_plain ON pgi_renamed_table(s)", 0)
	check(pgIndexesRow("public", "pgi_renamed_table", "pgi_plain", "CREATE INDEX pgi_plain ON public.pgi_renamed_table USING btree (s)"))
	if old != nil {
		pgIndexesRetained(t, old, oldTypes, false, original)
	}
	run("ALTER TABLE pgi_renamed_table DROP COLUMN s", 0)
	check(nil)
}
func TestReferencePGIndexesLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) { pgIndexesLifecycleScenario(t, p, b) })
	}
}
