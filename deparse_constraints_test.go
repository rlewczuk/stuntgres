package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func deparseText(t *testing.T, db *sql.DB, prepared bool, query string, want any, args ...any) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := db.Prepare(query)
		if e != nil {
			t.Fatal(query, e)
		}
		defer s.Close()
		rows, err = s.Query(args...)
	} else {
		rows, err = db.Query(query, args...)
	}
	if err != nil {
		t.Fatal(query, err)
	}
	defer rows.Close()
	ct, err := rows.ColumnTypes()
	if err != nil || len(ct) != 1 {
		t.Fatal(ct, err)
	}
	if ct[0].DatabaseTypeName() != "TEXT" || ct[0].ScanType() != reflect.TypeOf("") {
		t.Fatalf("metadata: %s %v", ct[0].DatabaseTypeName(), ct[0].ScanType())
	}
	if !rows.Next() {
		t.Fatal("missing result", rows.Err())
	}
	var got any
	if err = rows.Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v want %#v", query, got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra result", rows.Err())
	}
}

func deparseID(t *testing.T, db *sql.DB, query string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(query).Scan(&id); err != nil {
		t.Fatal(query, err)
	}
	if id <= 0 || id > 4294967295 {
		t.Fatal("invalid definition identity", id)
	}
	return id
}

func TestDeparseConstraintsCore(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE t(a INT,b BIGINT DEFAULT 7,CONSTRAINT ck CHECK(a::bigint>0))", 0)
			cid := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'")
			rid := deparseID(t, db, "SELECT conrelid FROM pg_catalog.pg_constraint WHERE conname='ck'")
			did := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef")
			if cid == did || cid == rid || did == rid {
				t.Fatal("identities collide", cid, did, rid)
			}
			wantTuples(t, db, "SELECT adrelid,adnum FROM pg_catalog.pg_attrdef", [][]any{{rid, int64(2)}})
			for _, tc := range []struct{ suffix, constraint, expr string }{
				{"", "CHECK (((a)::bigint > 0))", "((a)::bigint > 0)"},
				{",false", "CHECK (((a)::bigint > 0))", "((a)::bigint > 0)"},
				{",true", "CHECK (a::bigint > 0)", "a::bigint > 0"},
			} {
				deparseText(t, db, prepared, "SELECT pg_get_constraintdef(oid"+tc.suffix+") FROM pg_catalog.pg_constraint WHERE conname='ck'", tc.constraint)
				deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid"+tc.suffix+") FROM pg_catalog.pg_constraint WHERE conname='ck'", tc.expr)
			}
			deparseText(t, db, prepared, "SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", "7")
			deparseText(t, db, prepared, "SELECT pg_get_constraintdef($1,$2)", "CHECK (a::bigint > 0)", cid, true)
			rows, err := db.Query("SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='ck'")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			ct, err := rows.ColumnTypes()
			if err != nil || len(ct) != 1 || ct[0].DatabaseTypeName() != "PG_NODE_TREE" || ct[0].ScanType() != reflect.TypeOf("") {
				t.Fatal("tree metadata", ct, err)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var tree string
			if err = rows.Scan(&tree); err != nil {
				t.Fatal(err)
			}
			// Native representation is structural, not PostgreSQL nodeToString byte parity.
			if tree == "" || tree == "((a)::bigint > 0)" || tree == "a::bigint>0" {
				t.Fatal("tree is absent or substituted with SQL", tree)
			}
		})
	}
}

func TestDeparseConstraintsNullAndRelation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b BIGINT DEFAULT 7,CONSTRAINT ck CHECK(a::bigint>0))")
	execSQL(t, db, "CREATE TABLE other(z TEXT)")
	rid := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_class WHERE relname='other'")
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"SELECT pg_get_constraintdef(NULL)", "SELECT pg_get_constraintdef(NULL,true)", "SELECT pg_get_constraintdef(0)", "SELECT pg_get_constraintdef(4294967295::oid)", "SELECT pg_get_constraintdef(0,NULL)",
			"SELECT pg_get_expr(NULL,0)", "SELECT pg_get_expr(NULL,NULL)", "SELECT pg_get_expr(NULL,0,true)", "SELECT pg_get_expr(NULL,0,NULL)",
			"SELECT pg_get_expr(conbin,NULL) FROM pg_catalog.pg_constraint WHERE conname='ck'", "SELECT pg_get_expr(conbin,conrelid,NULL) FROM pg_catalog.pg_constraint WHERE conname='ck'",
			"SELECT pg_get_expr(conbin,4294967295::oid) FROM pg_catalog.pg_constraint WHERE conname='ck'", "SELECT pg_get_expr(adbin,4294967295::oid) FROM pg_catalog.pg_attrdef",
		} {
			deparseText(t, db, prepared, q, nil)
		}
		deparseText(t, db, prepared, fmt.Sprintf("SELECT pg_get_expr(conbin,%d::oid) FROM pg_catalog.pg_constraint WHERE conname='ck'", rid), "((z)::bigint > 0)")
		q := "SELECT pg_get_expr(conbin,0) FROM pg_catalog.pg_constraint WHERE conname='ck'"
		var err error
		if prepared {
			s, e := db.Prepare(q)
			if e != nil {
				t.Fatal(e)
			}
			_, err = s.Query()
			s.Close()
		} else {
			_, err = db.Query(q)
		}
		notNullDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "expression contains variables"})
	}
}

func TestDeparseConstraintsKindsAndDefaultIdentity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE modifiers(a INTEGER CONSTRAINT nn NOT NULL,b INTEGER,CONSTRAINT ck_no CHECK(b>0) NO INHERIT)")
	deparseText(t, db, false, "SELECT pg_get_constraintdef(oid) FROM pg_catalog.pg_constraint WHERE conname='nn'", "NOT NULL a")
	deparseText(t, db, true, "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='ck_no'", "CHECK (b > 0) NO INHERIT")
	deparseText(t, db, false, "SELECT pg_get_constraintdef(oid,false) FROM pg_catalog.pg_constraint WHERE conname='ck_no'", "CHECK ((b > 0)) NO INHERIT")
	deparseText(t, db, false, "SELECT pg_get_expr(conbin,conrelid) FROM pg_catalog.pg_constraint WHERE conname='nn'", nil)
	wantTuples(t, db, "SELECT conname,contype FROM pg_catalog.pg_constraint ORDER BY conname", [][]any{{"ck_no", "c"}, {"nn", "n"}})
	execSQL(t, db, "CREATE TABLE defaults(a INT DEFAULT NULL,b INT DEFAULT NULL::integer,c INT DEFAULT COALESCE(NULL::integer,NULL::integer),d INT DEFAULT 7)")
	wantTuples(t, db, "SELECT adnum FROM pg_catalog.pg_attrdef ORDER BY adnum", [][]any{{int64(3)}, {int64(4)}})
	old := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
	execSQL(t, db, "ALTER TABLE defaults ALTER d SET DEFAULT 3")
	fresh := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
	if old == fresh {
		t.Fatal("replacement retained definition identity")
	}
	deparseText(t, db, false, "SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef WHERE adnum=4", "3")
	execSQL(t, db, "ALTER TABLE defaults ALTER d SET DEFAULT NULL")
	wantTuples(t, db, "SELECT adnum FROM pg_catalog.pg_attrdef", [][]any{{int64(3)}})
	execSQL(t, db, "ALTER TABLE defaults ALTER d SET DEFAULT 7")
	if deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4") == fresh {
		t.Fatal("re-add reused identity")
	}
}

func TestDeparseConstraintsRenameReparseAndNative(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a::bigint>0))")
	id := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'")
	held, err := db.Prepare("SELECT pg_get_constraintdef($1,true)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	execSQL(t, db, `ALTER TABLE t RENAME a TO "A renamed"`)
	if deparseID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'") != id {
		t.Fatal("rename changed identity")
	}
	var got string
	if err = held.QueryRow(id).Scan(&got); err != nil || got != `CHECK ("A renamed"::bigint > 0)` {
		t.Fatal(got, err)
	}
	for _, pretty := range []bool{false, true} {
		var suffix string
		if err = db.QueryRow("SELECT pg_get_constraintdef($1,$2)", id, pretty).Scan(&suffix); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("copy_%t", pretty)
		execSQL(t, db, `CREATE TABLE `+name+`("A renamed" INT,CONSTRAINT copied `+suffix+`)`)
		tableCoreExec(t, db, false, "INSERT INTO "+name+" VALUES(1),(NULL)", 2)
		checkExecError(t, db, false, "INSERT INTO "+name+" VALUES(1),(0),(1)", checkViolation(name, "copied", "0"))
		checkExecError(t, db, true, "UPDATE "+name+" SET \"A renamed\"=$1", checkViolation(name, "copied", "-1"), -1)
		wantTuples(t, db, `SELECT "A renamed" FROM `+name+` ORDER BY "A renamed" NULLS FIRST`, [][]any{{nil}, {int64(1)}})
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	deparseText(t, db, false, "SELECT pg_get_constraintdef($1)", nil, id)
	if err = stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	deparseText(t, db, false, "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='ck'", `CHECK ("A renamed"::bigint > 0)`)
	for _, script := range []string{"CREATE TABLE bad(a INT CHECK(a))", "CREATE TABLE bad(a INT CONSTRAINT c CHECK(a>0));INSERT INTO bad VALUES(0)"} {
		if err = stuntgres.Restore(context.Background(), db, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore succeeded")
		}
		deparseText(t, db, true, "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='ck'", `CHECK ("A renamed"::bigint > 0)`)
	}
}
