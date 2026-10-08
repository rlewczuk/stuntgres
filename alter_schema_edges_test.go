package stuntgres_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterSchemaViewsAndFailedRestore(t *testing.T) {
	db := alterSchemaFixture(t)
	for _, q := range []string{"CREATE VIEW public.pairs AS SELECT l.id AS left_id,r.id AS right_id FROM s1.t AS l JOIN s1.t AS r ON l.id<r.id", "CREATE VIEW public.nested AS SELECT left_id,right_id FROM public.pairs"} {
		execSQL(t, db, q)
	}
	execSQL(t, db, "ALTER TABLE s1.t SET SCHEMA s2")
	want := [][]any{{int64(7), int64(9)}}
	wantTuples(t, db, "SELECT left_id,right_id FROM public.pairs", want)
	wantTuples(t, db, "SELECT left_id,right_id FROM public.nested", want)
	var definition string
	if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='pairs'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, `"s2"."t"`) || strings.Contains(definition, `"s1"."t"`) {
		t.Fatal("stale source namespace", definition)
	}
	execSQL(t, db, "CREATE VIEW public.replayed AS "+definition)
	wantTuples(t, db, "SELECT left_id,right_id FROM public.replayed", want)
	beforeDrop := alterSchemaDump(t, db)
	_, err := db.Exec("DROP TABLE s2.t RESTRICT")
	var diagnostic *stuntgres.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" {
		t.Fatal("dependent DROP TABLE was not rejected", err)
	}
	if after := alterSchemaDump(t, db); after != beforeDrop {
		t.Fatal("restricted DROP changed the dependent catalog")
	}
	before := alterSchemaDump(t, db)
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader(before+"CREATE TABLE late_duplicate(id INT); CREATE TABLE late_duplicate(id INT);")); err == nil {
		t.Fatal("late failing restore accepted")
	}
	if after := alterSchemaDump(t, db); after != before {
		t.Fatal("failed Restore replaced moved catalog")
	}
	wantTuples(t, db, "SELECT id,note FROM s2.t ORDER BY id", [][]any{{int64(7), "keep"}, {int64(9), nil}})
}

func TestAlterSchemaRetainedMetadata(t *testing.T) {
	db := alterSchemaFixture(t)
	held, err := db.Query("SELECT id,note FROM s1.t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "ALTER TABLE s1.t SET SCHEMA s2")
	dump := alterSchemaDump(t, db)
	if err = stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(context.Background(), db, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	after, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range after {
		nullable, known := c.Nullable()
		oldNullable, oldKnown := before[i].Nullable()
		if c.Name() != before[i].Name() || c.DatabaseTypeName() != before[i].DatabaseTypeName() || c.ScanType() != before[i].ScanType() || nullable != oldNullable || known != oldKnown {
			t.Fatal("retained metadata changed", i, c)
		}
	}
	var got [][]any
	for held.Next() {
		var id, note any
		if err := held.Scan(&id, &note); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{id, note})
	}
	if held.Err() != nil || !reflect.DeepEqual(got, [][]any{{int64(7), "keep"}, {int64(9), nil}}) {
		t.Fatal(got, held.Err())
	}
}

func TestAlterSchemaQuotedCollision(t *testing.T) {
	db := alterSchemaFixture(t)
	execSQL(t, db, `CREATE SCHEMA "dest""\schema"`)
	execSQL(t, db, `CREATE TABLE s1."t""\name"(id INT)`)
	execSQL(t, db, `CREATE SEQUENCE "dest""\schema"."t""\name"`)
	before := alterSchemaDump(t, db)
	_, err := db.Exec(`ALTER TABLE s1."t""\name" SET SCHEMA "dest""\schema"`)
	alterDropDiagnostic(t, err, "42P07", `relation "t"\name" already exists in schema "dest"\schema"`, "")
	if after := alterSchemaDump(t, db); after != before {
		t.Fatal("quoted collision changed state")
	}
}
