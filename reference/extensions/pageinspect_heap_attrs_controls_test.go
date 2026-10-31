package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func attrsConsumers() []fixture.Step {
	return []fixture.Step{
		attrsValue("SELECT attrs FROM attrs_split5", attrsArray("01020304", "05060708")),
		attrsValue("SELECT attrs FROM attrs_split6", attrsArray("01020304", "05060708")),
		attrsRows("SELECT * FROM attrs_rows2 ORDER BY lp", [][]any{attrsRow(8160, 32, 0, 2, 24, nil, attrsArray("01020304", "05060708"))}),
		attrsRows("SELECT * FROM attrs_rows3 ORDER BY lp", [][]any{attrsRow(8160, 32, 0, 2, 24, nil, attrsArray("01020304", "05060708"))}),
	}
}

func attrsStore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE attrs_images(id integer,raw bytea,data bytea)", 0))
	image, data := attrsImage(attrsBytes("0102030405060708"), 0, 2, nil, true), attrsBytes("0102030405060708")
	run(fixture.Exec("INSERT INTO attrs_images VALUES(1,$1,$2)", 1, image, data))
	clear(image)
	clear(data)
	for _, q := range []string{
		"CREATE VIEW attrs_split5 AS SELECT heap_part.tuple_data_split(t_bits=>NULL,t_infomask2=>2,t_data=>data,rel_oid=>'attrs_schema'::regclass::oid,t_infomask=>0) AS attrs FROM attrs_images",
		"CREATE VIEW attrs_split6 AS SELECT heap_part.tuple_data_split(do_detoast=>false,t_bits=>NULL,t_infomask2=>2,t_data=>data,rel_oid=>'attrs_schema'::regclass::oid,t_infomask=>0) AS attrs FROM attrs_images",
		"CREATE VIEW attrs_rows2 AS SELECT h.* FROM attrs_images CROSS JOIN LATERAL heap_part.heap_page_item_attrs(raw,'attrs_schema'::regclass) h",
		"CREATE VIEW attrs_rows3 AS SELECT h.* FROM attrs_images CROSS JOIN LATERAL heap_part.heap_page_item_attrs(raw,'attrs_schema'::regclass,false) h",
		"CREATE TABLE attrs_defaults(id integer,v bytea[] DEFAULT heap_part.tuple_data_split('attrs_schema'::regclass::oid,'\\x01020304'::bytea,1,2,'10000000',false),CONSTRAINT attrs_valid CHECK(heap_part.tuple_data_split('attrs_schema'::regclass::oid,'\\x01020304'::bytea,1,2,'10000000') IS NOT NULL AND v IS NOT NULL))",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO attrs_defaults(id) VALUES(1)", 1))
	heapBoth(t, p, b, c, fixture.Select("SELECT v FROM attrs_defaults ORDER BY id", []string{"v"}, []string{"_BYTEA"}, [][]any{{attrsArray("01020304", "NULL")}}))
}

func TestReferencePageinspectHeapAttrsControls(t *testing.T) {
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		attrsStore(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		held := make([]fixture.HeldStatement, 4)
		for i, s := range attrsConsumers() {
			st, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: s.Query}
		}
		check := func() {
			for i, s := range attrsConsumers() {
				heapBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
		}
		check()
		run(fixture.Exec("CREATE SCHEMA heap_moved", 0))
		for _, tc := range []struct{ old, signature, renamed string }{
			{"tuple_data_split", "oid,bytea,integer,integer,text", "split_five"},
			{"tuple_data_split", "oid,bytea,integer,integer,text,boolean", "split_six"},
			{"heap_page_item_attrs", "bytea,regclass", "attrs_two"},
			{"heap_page_item_attrs", "bytea,regclass,boolean", "attrs_three"},
		} {
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'heap_part."+tc.old+"("+tc.signature+")'::regprocedure::bigint").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("routine identity", oid, err)
			}
			run(fixture.Exec("ALTER FUNCTION heap_part."+tc.old+"("+tc.signature+") RENAME TO "+tc.renamed, 0))
			run(fixture.Exec("ALTER FUNCTION heap_part."+tc.renamed+"("+tc.signature+") SET SCHEMA heap_moved", 0))
			heapBoth(t, p, b, c, fixture.Select("SELECT 'heap_moved."+tc.renamed+"("+tc.signature+")'::regprocedure::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}}))
			check()
			sig := "heap_moved." + tc.renamed + "(" + tc.signature + ")"
			run(fixture.Exec("ALTER EXTENSION pageinspect DROP FUNCTION "+sig, 0))
			check()
			run(fixture.Exec("ALTER EXTENSION pageinspect ADD FUNCTION "+sig, 0))
		}
		run(fixture.Exec("ALTER TABLE attrs_schema RENAME TO attrs_original", 0))
		run(fixture.Exec("CREATE TABLE attrs_schema(a text)", 0))
		check()
		// A dropped physical slot survives type removal and serialized schema.
		for _, q := range []string{"CREATE DOMAIN attrs_dead AS bigint", "ALTER TABLE attrs_original ADD COLUMN dead attrs_dead", "ALTER TABLE attrs_original DROP COLUMN dead", "DROP DOMAIN attrs_dead"} {
			run(fixture.Exec(q, 0))
		}
		for i, s := range attrsConsumers() {
			if i < 2 {
				s.Rows[0][0] = attrsArray("01020304", "05060708", "NULL")
			} else {
				s.Rows[0][13] = attrsArray("01020304", "05060708", "NULL")
			}
			heapBoth(t, p, b, c, s)
		}
		// Replay retains the descriptor's dropped physical slot as trailing NULL,
		// rather than pretending DROP compacts the descriptor.
		want := attrsConsumers()
		for i := range want {
			if i < 2 {
				want[i].Rows[0][0] = attrsArray("01020304", "05060708", "NULL")
			} else {
				want[i].Rows[0][13] = attrsArray("01020304", "05060708", "NULL")
			}
		}
		checkReplay := func(conn *sql.Conn, recorder *fixture.Recorder) {
			for _, s := range want {
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
			}
		}
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		other, destination, recorder := pgcryptoControlDestination(t, p, "attrs_restore")
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		checkReplay(destination, recorder)
		if fixture.ExtensionDump(t, p.Ctx, other) != dump {
			t.Fatal("symbolic redump changed")
		}
		retained := make([]*sql.Rows, len(want))
		metadata := make([][]*sql.ColumnType, len(want))
		for i, s := range want {
			rows, err := c.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			retained[i] = rows
			metadata[i], err = rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("UPDATE attrs_images SET raw=NULL,data=NULL", 1))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		checkReplay(c, b.Recorder)
		for i, rows := range retained {
			attrsCheckColumns(t, metadata[i], want[i].Columns, want[i].Types, false, true)
			if !rows.Next() {
				t.Fatal("retained row absent", rows.Err())
			}
			values, dest := make([]any, len(want[i].Columns)), make([]any, len(want[i].Columns))
			for j := range values {
				dest[j] = &values[j]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for j, v := range values {
				if !reflect.DeepEqual(fixture.Normalize(v), want[i].Rows[0][j]) {
					t.Fatal("retained changed", i, j, v)
				}
				if raw, ok := v.([]byte); ok {
					clear(raw)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained cardinality", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			attrsCheckColumns(t, metadata[i], want[i].Columns, want[i].Types, false, true)
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, want[i])
		}
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		bad := dump + "\nINSERT INTO attrs_images VALUES(2,NULL,NULL);\nINSERT INTO attrs_images VALUES(1/0,NULL,NULL);\n"
		diagnostic := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &diagnostic)
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed Restore published prefix")
		}
		checkReplay(c, b.Recorder)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled Restore changed catalog")
		}
		checkReplay(c, b.Recorder)
	})
}
