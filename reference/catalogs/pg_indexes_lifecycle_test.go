package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func pgIndexesRetained(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, pg bool, want []any) {
	t.Helper()
	defer rows.Close()
	fixture.PgIndexesColumnMetadata(t, types, pg)
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
		fixture.AssertRawValue(t, pg, fixture.PgIndexesTypes[i], v)
		got[i] = fixture.Normalize(v)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cardinality", rows.Err())
	}
	fixture.PgIndexesColumnMetadata(t, types, pg)
}
func pgIndexesLifecycleScenario(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	db := b.DB
	run := func(q string, n int64) { fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, n)) }
	stmt, err := db.PrepareContext(p.Ctx, fixture.PgIndexesProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: fixture.PgIndexesProbe}
	check := func(row []any) {
		fixture.PgIndexesCheck(t, p, b, db, "pgi_plain", row)
		fixture.PgIndexesCheck(t, p, b, held, "pgi_plain", row)
	}
	check(nil)
	run("CREATE TABLE pgi_table(n INT,s TEXT,p TEXT)", 0)
	run("CREATE INDEX pgi_plain ON pgi_table(n)", 0)
	original := fixture.PgIndexesRow("public", "pgi_table", "pgi_plain", "CREATE INDEX pgi_plain ON public.pgi_table USING btree (n)")
	check(original)
	run("INSERT INTO pgi_table VALUES(1,'s','p')", 1)
	run("UPDATE pgi_table SET n=2", 1)
	run("REINDEX INDEX pgi_plain", 0)
	check(original)
	for _, arg := range []any{nil, "pgi_missing"} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PgIndexesSelect(b, fixture.PgIndexesProbe, nil, arg))
	}
	var old *sql.Rows
	var oldTypes []*sql.ColumnType
	if !b.PG {
		old, err = db.QueryContext(p.Ctx, fixture.PgIndexesProbe, "pgi_plain")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		oldTypes, err = old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.PgIndexesColumnMetadata(t, oldTypes, false)
	} else {
		// Materialize the PG result before DDL; never assume pq Query is an eager capture.
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PgIndexesSelect(b, fixture.PgIndexesProbe, [][]any{original}, "pgi_plain"))
	}
	run("ALTER TABLE pgi_table RENAME COLUMN n TO renamed_n", 0)
	run("ALTER TABLE pgi_table RENAME TO pgi_renamed_table", 0)
	run("ALTER INDEX pgi_plain RENAME TO pgi_renamed_index", 0)
	check(nil)
	renamed := fixture.PgIndexesRow("public", "pgi_renamed_table", "pgi_renamed_index", "CREATE INDEX pgi_renamed_index ON public.pgi_renamed_table USING btree (renamed_n)")
	fixture.PgIndexesCheck(t, p, b, held, "pgi_renamed_index", renamed)
	run("DROP INDEX pgi_renamed_index", 0)
	fixture.PgIndexesCheck(t, p, b, held, "pgi_renamed_index", nil)
	run("CREATE INDEX pgi_plain ON pgi_renamed_table(s)", 0)
	check(fixture.PgIndexesRow("public", "pgi_renamed_table", "pgi_plain", "CREATE INDEX pgi_plain ON public.pgi_renamed_table USING btree (s)"))
	if old != nil {
		pgIndexesRetained(t, old, oldTypes, false, original)
	}
	run("ALTER TABLE pgi_renamed_table DROP COLUMN s", 0)
	check(nil)
}
func TestReferencePGIndexesLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { pgIndexesLifecycleScenario(t, p, b) })
	}
}
