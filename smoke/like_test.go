package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

func TestLikeSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, count int64) {
		t.Helper()
		r, err := db.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != count {
			t.Fatal(q, n, err)
		}
	}
	check := func(rows *sql.Rows, err error, want [][]any) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range meta {
			if m.DatabaseTypeName() != "BOOL" || m.ScanType() != reflect.TypeFor[bool]() {
				t.Fatal(m)
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			v, dst := make([]any, len(meta)), make([]any, len(meta))
			for i := range v {
				dst[i] = &v[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if rows.Err() != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, rows.Err())
		}
	}
	exec("CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,c CHAR(3),b BYTEA,cc TEXT COLLATE \"C\",pp TEXT COLLATE \"POSIX\")", 0)
	exec("INSERT INTO t VALUES(1,'AbC','A%','!','a','\\xc3a9','a','a'),(2,'É','é','!','a','\\xc3a9','a','a'),(3,NULL,NULL,NULL,NULL,NULL,NULL,NULL)", 3)
	query := "SELECT v LIKE p,v NOT LIKE p,v ILIKE p,v NOT ILIKE p,v ~~ p,v !~~ p,v ~~* p,v !~~* p,v OPERATOR(pg_catalog.~~) p FROM t ORDER BY id"
	rows, err := db.QueryContext(ctx, query)
	check(rows, err, [][]any{{true, false, true, false, true, false, true, false, true}, {false, true, false, true, false, true, false, true, false}, {nil, nil, nil, nil, nil, nil, nil, nil, nil}})
	rows, err = db.QueryContext(ctx, "SELECT c LIKE 'a',c LIKE 'a__',b LIKE '\\x5f',b LIKE '\\x5f5f','a%b' LIKE 'a!%b' ESCAPE '!' FROM t WHERE id=1")
	check(rows, err, [][]any{{false, true, false, true, true}})
	rows, err = db.QueryContext(ctx, "SELECT 'x' LIKE '%__\\','' LIKE '%_\\',CAST('\\x78' AS BYTEA) LIKE CAST('\\x255f5f5c' AS BYTEA) FROM t WHERE id=1")
	check(rows, err, [][]any{{false, false, false}})
	for _, q := range []string{"SELECT 'x' LIKE '%_\\' FROM t WHERE id=1", "SELECT CAST('\\x78' AS BYTEA) LIKE CAST('\\x255f5c' AS BYTEA) FROM t WHERE id=1"} {
		rows, err = db.QueryContext(ctx, q)
		if rows != nil {
			rows.Close()
			t.Fatal("terminal escape exposed cursor")
		}
		if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22025", Message: "LIKE pattern must not end with escape character"}); err != nil {
			t.Fatal(err)
		}
	}
	stmt, err := db.PrepareContext(ctx, "SELECT v ILIKE $1 ESCAPE $2 FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.QueryContext(ctx, "a%", "ab")
	if rows != nil {
		rows.Close()
	}
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		escape any
		want   any
	}{{"!", true}, {nil, nil}, {"!", true}} {
		rows, err := stmt.QueryContext(ctx, "a%", tc.escape)
		check(rows, err, [][]any{{tc.want}})
	}
	for _, q := range []string{
		"SELECT cc LIKE pp FROM t WHERE id=1",
		"SELECT cc LIKE CAST(pp AS TEXT) FROM t WHERE id=1",
		"SELECT cc LIKE COALESCE('a'::text,pp) FROM t WHERE id=1",
		"SELECT 'a' LIKE COALESCE(pg_catalog.like_escape(cc,pp),'a') FROM t WHERE id=1",
	} {
		_, err = db.QueryContext(ctx, q)
		if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."}); err != nil {
			t.Fatal(err)
		}
	}
	explicitQuery := `SELECT cc COLLATE "default" LIKE pp,cc LIKE COALESCE(pp,cc) COLLATE "C",cc LIKE btrim(pp COLLATE "C") FROM t ORDER BY id`
	rows, err = db.QueryContext(ctx, explicitQuery)
	check(rows, err, [][]any{{true, true, true}, {true, true, true}, {nil, nil, nil}})
	explicit, err := db.PrepareContext(ctx, explicitQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer explicit.Close()
	rows, err = explicit.QueryContext(ctx)
	check(rows, err, [][]any{{true, true, true}, {true, true, true}, {nil, nil, nil}})
	_, err = db.PrepareContext(ctx, `SELECT cc COLLATE "C" LIKE pp COLLATE "POSIX" FROM t`)
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: "31"}); err != nil {
		t.Fatal(err)
	}
	_, err = db.QueryContext(ctx, "SELECT cc LIKE btrim(pp) FROM t WHERE id=1")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."}); err != nil {
		t.Fatal(err)
	}
	_, err = db.PrepareContext(ctx, "SELECT id LIKE 'x' FROM t")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer ~~ unknown", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "11"}); err != nil {
		t.Fatal(err)
	}
	exec("CREATE TABLE guarded(v TEXT CONSTRAINT starts_a CHECK(v LIKE 'a%' ESCAPE '!'))", 0)
	exec("INSERT INTO guarded VALUES('a'),(NULL)", 2)
	if _, err := db.ExecContext(ctx, "INSERT INTO guarded VALUES('ab'),('b')"); err == nil {
		t.Fatal("CHECK accepted failing tuple")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM guarded").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	exec("UPDATE guarded SET v='ab' WHERE v LIKE 'a%' ESCAPE '!'", 1)
	rows, err = db.QueryContext(ctx, "SELECT v LIKE 'a%' FROM guarded WHERE v LIKE 'a%'")
	check(rows, err, [][]any{{true}})
	exec("CREATE VIEW matched AS SELECT id,v ILIKE p ESCAPE e AS result FROM t", 0)
	exec("CREATE VIEW wrapped AS SELECT id,cc LIKE CAST(pp AS TEXT) AS result FROM t", 0)
	exec(`CREATE VIEW annotated_match AS SELECT id,cc LIKE btrim(pp COLLATE "C") AS result FROM t`, 0)
	invalid, err := db.PrepareContext(ctx, "CREATE VIEW invalid_pattern AS SELECT COALESCE(pg_catalog.like_escape(cc,pp),'a') AS pattern FROM t")
	if err != nil {
		t.Fatal("view conflict rejected Prepare", err)
	}
	_, err = invalid.ExecContext(ctx)
	invalid.Close()
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42P22", Message: `could not determine which collation to use for view column "pattern"`, Hint: "Use the COLLATE clause to set the collation explicitly."}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT result FROM matched ORDER BY id")
	check(rows, err, [][]any{{true}, {false}, {nil}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT result FROM matched ORDER BY id")
	check(rows, err, [][]any{{true}, {false}, {nil}})
	rows, err = db.QueryContext(ctx, "SELECT result FROM annotated_match ORDER BY id")
	check(rows, err, [][]any{{true}, {true}, {nil}})
	rows, err = explicit.QueryContext(ctx)
	check(rows, err, [][]any{{true, true, true}, {true, true, true}, {nil, nil, nil}})
	_, err = db.QueryContext(ctx, "SELECT result FROM wrapped WHERE id=1")
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."}); err != nil {
		t.Fatal("wrapper replay lost collation", err)
	}
	var char string
	var binary []byte
	if err := db.QueryRowContext(ctx, "SELECT c,b FROM t WHERE id=1").Scan(&char, &binary); err != nil || char != "a  " || !bytes.Equal(binary, []byte{0xc3, 0xa9}) {
		t.Fatal(char, binary, err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable redump", err)
	}
}
