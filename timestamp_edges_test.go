package stuntgres_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestTimestampDeclarationsOccurrenceSizingAndOwnership(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	create, e := db.Prepare(`CREATE TABLE t(v TIMESTAMP,p0 TIMESTAMP(0),p1 TIMESTAMP(1),p2 TIMESTAMP(2),p3 TIMESTAMP(3),p4 TIMESTAMP(4),p5 TIMESTAMP(5),p6 TIMESTAMP(6),q "timestamp"(0))`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = create.Exec()
	create.Close()
	if e != nil {
		t.Fatal(e)
	}
	stmt, e := db.Prepare("INSERT INTO t VALUES($1,$1,$1,$1,$1,$1,$1,$1,$1)")
	if e != nil {
		t.Fatal(e)
	}
	defer stmt.Close()
	raw := []byte("2000-01-01 00:00:00.123456")
	if _, e = stmt.Exec(raw); e != nil {
		t.Fatal(e)
	}
	raw[0] = 'X'
	names := []string{"v", "p0", "p1", "p2", "p3", "p4", "p5", "p6", "q"}
	types := []string{}
	want := []any{}
	for _, ns := range []int{123456000, 0, 100000000, 120000000, 123000000, 123500000, 123460000, 123456000, 0} {
		types = append(types, "TIMESTAMP")
		want = append(want, time.Date(2000, 1, 1, 0, 0, 0, ns, time.UTC))
	}
	timestampQuery(t, db, "SELECT * FROM t", names, types, [][]any{want})
	execSQL(t, db, "CREATE TABLE clone(LIKE t)")
	execSQL(t, db, "INSERT INTO clone VALUES($1,$1,$1,$1,$1,$1,$1,$1,$1)", "2000-01-01 00:00:00.123456")
	timestampQuery(t, db, "SELECT * FROM clone", names, types, [][]any{want})
	// Accepted integer lexical families belong to Iconst even in precision.
	execSQL(t, db, "CREATE TABLE radix(v TIMESTAMP(0x3),w TIMESTAMP(0b11),x TIMESTAMP(0o3))")
	execSQL(t, db, "INSERT INTO radix VALUES('epoch','epoch','epoch')")
}
func TestTimestampEmptyDiagnosticsPrecisionAndTransactionAbort(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE empty(v TIMESTAMP)")
	for _, q := range []string{"SELECT TIMESTAMP 'nonsense' FROM empty", "/*é*/ SELECT TIMESTAMP 'nonsense' FROM empty"} {
		pos := strconv.Itoa(len([]rune(q[:strings.Index(q, "'")])) + 1)
		want := stuntgres.Error{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: pos}
		rows, e := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor")
		}
		typeCoreDiagnostic(t, e, want)
		stmt, e := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed handle")
		}
		typeCoreDiagnostic(t, e, want)
	}
	for _, tc := range []struct{ p, token string }{{"+1", "+"}, {"1.5", "1.5"}, {"1e2", "1e2"}, {".5", ".5"}, {"1,2", ","}, {"2147483648", "2147483648"}} {
		q := "CREATE TABLE bad (v TIMESTAMP(" + tc.p + "))"
		_, e := db.Exec(q)
		typeCoreDiagnostic(t, e, stuntgres.Error{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: strconv.Itoa(strings.Index(q, tc.token) + 1)})
	}
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	_, e := db.Exec("INSERT INTO t VALUES('2000-01-01'),(NULL)")
	typeCoreDiagnostic(t, e, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"})
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, "INSERT INTO t VALUES('2000-01-01'),('nonsense')")
	var d *stuntgres.Error
	if !errors.As(e, &d) || d.Code != "22007" {
		t.Fatal(e)
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO t VALUES('now')")
	typeCoreDiagnostic(t, e, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
}
func TestTimestampPublicDescriptionAndPreparedRelatives(t *testing.T) {
	// database/sql metadata does not expose typmods; native Dump and engine
	// invariant tests cover them rather than pretending ColumnType has that API.
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP(3) NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	rows, e := db.Query("SELECT v,TIMESTAMP(0) 'epoch' FROM t")
	if e != nil {
		t.Fatal(e)
	}
	types, e := rows.ColumnTypes()
	rows.Close()
	if e != nil {
		t.Fatal(e)
	}
	n, k := types[0].Nullable()
	if !k || n {
		t.Fatal(types[0])
	}
	_, known := types[1].Nullable()
	if known {
		t.Fatal("invented scalar nullability")
	}
	if types[1].ScanType() != reflect.TypeFor[time.Time]() {
		t.Fatal(types[1])
	}
	conn, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	for _, special := range []string{"today", "tomorrow", "yesterday"} {
		stmt, e := conn.PrepareContext(context.Background(), "SELECT TIMESTAMP '"+special+"' FROM t")
		if e != nil {
			t.Fatal(e)
		}
		var initial, later time.Time
		if e = stmt.QueryRowContext(context.Background()).Scan(&initial); e != nil {
			t.Fatal(e)
		}
		tx, e := conn.BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		e = stmt.QueryRowContext(context.Background()).Scan(&later)
		tx.Rollback()
		stmt.Close()
		if e != nil || !later.Equal(initial) || initial.Hour() != 0 || initial.Nanosecond() != 0 {
			t.Fatal(special, initial, later, e)
		}
	}
}

func TestTimestampSettingArgumentsUseScopeEnvironmentWithoutNewCasts(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct{ input, code, message string }{
		{"nonsense", "22007", `invalid input syntax for type timestamp: "nonsense"`},
		{"now", "42804", "incompatible source and destination types"},
	} {
		for _, q := range []string{"SELECT current_setting(TIMESTAMP '" + tc.input + "')", "SELECT set_config('application_name',TIMESTAMP '" + tc.input + "',FALSE)"} {
			want := stuntgres.Error{Code: tc.code, Message: tc.message}
			if tc.code == "22007" {
				want.Position = strconv.Itoa(strings.LastIndex(q, "'"+tc.input+"'") + 1)
			}
			rows, err := db.Query(q)
			if rows != nil {
				rows.Close()
				t.Fatal("failed binding exposed cursor")
			}
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed binding exposed handle")
			}
			typeCoreDiagnostic(t, err, want)
		}
	}
}
func TestTimestampInvalidDeclarationNeverPublishesAndAbortsTransaction(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	q := `CREATE TABLE bad (v "timestamp"(-1))`
	want := stuntgres.Error{Code: "22023", Message: "TIMESTAMP(-1) precision must not be negative", Position: "21"}
	_, err := db.Exec(q)
	typeCoreDiagnostic(t, err, want)
	utilityTx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = utilityTx.Exec(q)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
	if err = utilityTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	invalid := "INSERT INTO t VALUES('nonsense')"
	_, err = tx.Exec(invalid)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: strconv.Itoa(strings.Index(invalid, "'") + 1)})
	_, err = tx.Exec("INSERT INTO t VALUES('now')")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT v FROM bad")
	if rows != nil {
		rows.Close()
		t.Fatal("rejected declaration published table")
	}
	var d *stuntgres.Error
	if !errors.As(err, &d) || d.Code != "42P01" {
		t.Fatal(err)
	}
	timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, nil)
}
func TestTimestampQuotedUnicodeDeclarationsAndPublicCastBoundary(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE timestamp(without U&"time\0073tamp"(3),time TIMESTAMP,zone TIMESTAMP,timestamp TIMESTAMP)`)
	execSQL(t, db, `INSERT INTO timestamp VALUES('2000-01-01 00:00:00.1236','epoch','epoch','epoch')`)
	timestampQuery(t, db, "SELECT without,time,zone,timestamp FROM timestamp", []string{"without", "time", "zone", "timestamp"}, []string{"TIMESTAMP", "TIMESTAMP", "TIMESTAMP", "TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
	q := "SELECT TIMESTAMP 'epoch'::timestamp FROM timestamp"
	want := stuntgres.Error{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: strconv.Itoa(strings.Index(q, "::") + 1)}
	rows, err := db.Query(q)
	if rows != nil {
		rows.Close()
		t.Fatal("cast boundary exposed cursor")
	}
	typeCoreDiagnostic(t, err, want)
	stmt, err := db.Prepare(q)
	if stmt != nil {
		stmt.Close()
		t.Fatal("cast boundary exposed handle")
	}
	typeCoreDiagnostic(t, err, want)
}
