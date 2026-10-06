package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

// These expectations are independently specified PostgreSQL semantics, not
// golden values collected from the rebased native implementation.
func defaultConsumerRows(t *testing.T, db *sql.DB, prepared bool, names, types []string, want [][]any) {
	t.Helper()
	q := "SELECT * FROM d ORDER BY id"
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := db.Prepare(q)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		rows, err = s.Query()
	} else {
		rows, err = db.Query(q)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatal(meta, err)
	}
	for i, typ := range types {
		var scan reflect.Type
		switch typ {
		case "INT4", "INT8":
			scan = reflect.TypeFor[int64]()
		case "TEXT", "DATE":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "BYTEA":
			scan = reflect.TypeFor[[]byte]()
		default:
			t.Fatal("unhandled fixture type", typ)
		}
		nullable, known := meta[i].Nullable()
		if meta[i].ScanType() != scan || !known || !nullable {
			t.Fatal("default changed declared metadata", i, meta[i])
		}
	}
	timestampCheckRows(t, rows, names, types, want)
}

func defaultConsumerRoundtrip(t *testing.T, db *sql.DB, witness string) {
	t.Helper()
	ctx := context.Background()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), witness) {
		t.Errorf("lost default structure %q in %s", witness, dump.String())
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("default restore", err, dump.String())
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
		t.Fatal("non-deterministic default redump", err, again.String(), dump.String())
	}
}

func TestDefaultCastControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,s TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT CAST(TRUE AS INT),b BOOL DEFAULT CAST(-2 AS BOOL),n BIGINT DEFAULT CAST(7 AS BIGINT),f INT DEFAULT int4('42'::text),p BOOL DEFAULT ((text(TRUE) = 'true') IS TRUE))", 0)
			names, types := []string{"id", "s", "i", "b", "n", "f", "p"}, []string{"INT4", "TEXT", "INT4", "BOOL", "INT8", "INT4", "BOOL"}
			var want [][]any
			defaultConsumerRows(t, db, prepared, names, types, want)
			defaultConsumerRoundtrip(t, db, "CAST(")
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(1),(2)", 2)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT),(4,NULL,NULL,NULL,NULL,NULL,NULL)", 2)
			want = [][]any{{int64(1), "1", int64(1), true, int64(7), int64(42), true}, {int64(2), "1", int64(1), true, int64(7), int64(42), true}, {int64(3), "1", int64(1), true, int64(7), int64(42), true}, {int64(4), nil, nil, nil, nil, nil, nil}}
			defaultConsumerRows(t, db, prepared, names, types, want)
			defaultConsumerRoundtrip(t, db, "CAST(")
			defaultConsumerRows(t, db, prepared, names, types, want)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(5)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(6,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			want = append(want, []any{int64(5), "1", int64(1), true, int64(7), int64(42), true}, []any{int64(6), "1", int64(1), true, int64(7), int64(42), true})
			defaultConsumerRows(t, db, prepared, names, types, want)
		})
	}
}

func TestDefaultCastDeferredInputControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			// The inner TEXT cast makes the outer conversion execution-demanded,
			// unlike assignment of an unknown 'oops' literal at CREATE time.
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,v INT DEFAULT CAST('oops'::text AS int4))", 0)
			defaultConsumerRoundtrip(t, db, "CAST(")
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(1,9),(2,NULL)", 2)
			defaultConsumerRoundtrip(t, db, "CAST(")
			for _, q := range []string{"INSERT INTO d(id) VALUES(3)", "INSERT INTO d VALUES(4,DEFAULT)"} {
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
				defaultConsumerRows(t, db, prepared, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}})
			}
		})
	}
}

func TestDefaultCastParameterProhibitions(t *testing.T) {
	for _, expression := range []string{"int4($1)", "int8($1)", "bool($1)", "text($1)", "CAST($1 AS INT)", "$1::int4", "CAST(int4($1) AS TEXT)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%s", prepared, expression), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				q := "CREATE TABLE bad(v INT DEFAULT " + expression + ")"
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("utility Prepare must not analyze defaults", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(strings.Index(q, "$1") + 1)})
				_, err = db.Query("SELECT * FROM bad")
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
			})
		}
	}
}

func TestDefaultDateCompoundControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,v DATE DEFAULT DATE '0002-01-01 BC',b BOOL DEFAULT (DATE '0002-01-01 BC' < DATE '0001-01-01 BC'),e BOOL DEFAULT (DATE '0001-01-01 BC' < DATE '0001-01-01'),n BOOL DEFAULT ((DATE '0002-01-01 BC' < DATE '0001-01-01 BC') IS TRUE))", 0)
			names, types := []string{"id", "v", "b", "e", "n"}, []string{"INT4", "DATE", "BOOL", "BOOL", "BOOL"}
			var want [][]any
			defaultConsumerRows(t, db, prepared, names, types, want)
			defaultConsumerRoundtrip(t, db, "DATE '0002-01-01 BC'")
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(1),(2)", 2)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT),(4,NULL,NULL,NULL,NULL)", 2)
			want = [][]any{{int64(1), "0002-01-01 BC", true, true, true}, {int64(2), "0002-01-01 BC", true, true, true}, {int64(3), "0002-01-01 BC", true, true, true}, {int64(4), nil, nil, nil, nil}}
			defaultConsumerRows(t, db, prepared, names, types, want)
			defaultConsumerRoundtrip(t, db, "DATE '0002-01-01 BC'")
			defaultConsumerRows(t, db, prepared, names, types, want)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(5)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(6,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			want = append(want, []any{int64(5), "0002-01-01 BC", true, true, true}, []any{int64(6), "0002-01-01 BC", true, true, true})
			defaultConsumerRows(t, db, prepared, names, types, want)
		})
	}
}

func TestDefaultQuerySourceControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,v INT DEFAULT 7,s TEXT DEFAULT 'x',b BOOL DEFAULT TRUE)", 0)
			tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO src VALUES(1),(2)", 2)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,9,'seed',FALSE)", 1)
			for _, tc := range []struct {
				q     string
				count int64
			}{
				{"INSERT INTO d(id) SELECT id FROM src ORDER BY id", 2},
				{"INSERT INTO d(id) VALUES(3),(4) ORDER BY 1 DESC", 2},
				{"INSERT INTO d(id) (VALUES(5),(6))", 2},
				{"INSERT INTO d(id,v) SELECT 7,NULL", 1},
				{"INSERT INTO d SELECT 8", 1},
				{"INSERT INTO d(id) SELECT id FROM src WHERE id=99", 0},
			} {
				tableCoreExec(t, db, prepared, tc.q, tc.count)
			}
			want := [][]any{{int64(0), int64(9), "seed", false}}
			for id := int64(1); id <= 8; id++ {
				var v any = int64(7)
				if id == 7 {
					v = nil
				}
				want = append(want, []any{id, v, "x", true})
			}
			defaultConsumerRows(t, db, prepared, []string{"id", "v", "s", "b"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, want)
			// Querying command-only INSERT must expose no synthetic RETURNING data.
			q := "INSERT INTO d(id) SELECT 10"
			var rows *sql.Rows
			var err error
			if prepared {
				s, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				rows, err = s.Query()
			} else {
				rows, err = db.Query(q)
			}
			if err != nil {
				t.Fatal(err)
			}
			names, e := rows.Columns()
			meta, me := rows.ColumnTypes()
			if e != nil || me != nil || len(names) != 0 || len(meta) != 0 || rows.Next() || rows.Err() != nil {
				t.Fatal(names, meta, e, me, rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			want = append(want, []any{int64(10), int64(7), "x", true})
			defaultConsumerRoundtrip(t, db, "DEFAULT 7")
			defaultConsumerRows(t, db, prepared, []string{"id", "v", "s", "b"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, want)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) SELECT 11", 1)
			want = append(want, []any{int64(11), int64(7), "x", true})
			defaultConsumerRows(t, db, prepared, []string{"id", "v", "s", "b"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, want)
		})
	}
}

func TestDefaultQuerySourceAtomicityAndCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"SELECT id,n FROM src ORDER BY id", "VALUES(1,9),(2,NULL) ORDER BY 1"} {
			t.Run(fmt.Sprintf("%v/%s", prepared, source), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT,n INT)", 0)
				tableCoreExec(t, db, prepared, "INSERT INTO src VALUES(1,9),(2,NULL)", 2)
				tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,v INT DEFAULT 7,n INT NOT NULL)", 0)
				tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,9,9)", 1)
				q := "INSERT INTO d(id,n) " + source
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "d" violates not-null constraint`, Detail: "Failing row contains (2, 7, null).", Schema: "public", Table: "d", Column: "n"})
				wantTuples(t, db, "SELECT * FROM d", [][]any{{int64(0), int64(9), int64(9)}})
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				q = "INSERT INTO d(id,n) SELECT 3,9"
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.ExecContext(ctx)
					s.Close()
				} else {
					_, err = db.ExecContext(ctx, q)
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation boundary", err)
				}
				wantTuples(t, db, "SELECT * FROM d", [][]any{{int64(0), int64(9), int64(9)}})
				tableCoreExec(t, db, prepared, q, 1)
				wantTuples(t, db, "SELECT * FROM d ORDER BY id", [][]any{{int64(0), int64(9), int64(9)}, {int64(3), int64(7), int64(9)}})
			})
		}
	}
}

func TestDefaultQuerySourceDeferredFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,v INT DEFAULT 2147483648)", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,9)", 1)
			tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
			// PostgreSQL plans an omitted immutable default even for an empty
			// supported table source; this is distinct from per-row evaluation.
			for _, q := range []string{"INSERT INTO d(id) SELECT id FROM src", "INSERT INTO d(id) SELECT 1", "INSERT INTO d(id) VALUES(1),(2) ORDER BY 1"} {
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
				wantTuples(t, db, "SELECT * FROM d", [][]any{{int64(0), int64(9)}})
			}
			tableCoreExec(t, db, prepared, "INSERT INTO d(id,v) SELECT id,9 FROM src", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO src VALUES(1),(2)", 2)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id,v) SELECT id,9 FROM src ORDER BY id", 2)
			wantTuples(t, db, "SELECT * FROM d ORDER BY id", [][]any{{int64(0), int64(9)}, {int64(1), int64(9)}, {int64(2), int64(9)}})
		})
	}
}

func TestDefaultDecoratedValuesBoundary(t *testing.T) {
	for _, q := range []string{"INSERT INTO d VALUES(DEFAULT),(2) ORDER BY 1", "INSERT INTO d (VALUES(1),(DEFAULT) ORDER BY 1)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%s", prepared, q), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, "CREATE TABLE d(v INT DEFAULT 7)", 0)
				tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(DEFAULT)", 1)
				tableCoreExec(t, db, prepared, "INSERT INTO d (VALUES(DEFAULT))", 1)
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e == nil {
						s.Close()
						t.Fatal("decorated DEFAULT must fail at Prepare")
					}
					err = e
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: strconv.Itoa(strings.Index(q, "DEFAULT") + 1)})
				wantTuples(t, db, "SELECT v FROM d", [][]any{{int64(7)}, {int64(7)}})
			})
		}
	}
}

func TestDefaultQuerySourceHeldPrepared(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE src(id INT)")
			execSQL(t, db, "CREATE TABLE d(id INT,v INT DEFAULT 7,b BOOL DEFAULT FALSE)")
			q := "INSERT INTO d(id) SELECT $1 FROM src"
			execute := func(arg any) (sql.Result, error) { return db.Exec(q, arg) }
			if prepared {
				s, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				execute = func(arg any) (sql.Result, error) { return s.Exec(arg) }
			}
			for _, tc := range []struct {
				before               string
				inserted, arg, count int64
			}{
				{"", 0, 7, 0},
				{"INSERT INTO src VALUES(1),(2)", 2, 8, 2},
				{"INSERT INTO src VALUES(3)", 1, 9, 3},
			} {
				if tc.before != "" {
					tableCoreExec(t, db, prepared, tc.before, tc.inserted)
				}
				result, err := execute(tc.arg)
				insertSelectCount(t, result, err, tc.count)
			}
			defaultConsumerRows(t, db, prepared, []string{"id", "v", "b"}, []string{"INT4", "INT4", "BOOL"}, [][]any{
				{int64(8), int64(7), false}, {int64(8), int64(7), false},
				{int64(9), int64(7), false}, {int64(9), int64(7), false}, {int64(9), int64(7), false},
			})
		})
	}
}

func TestDefaultByteaControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, `CREATE TABLE d(id INT,v BYTEA DEFAULT '\x0080ff5c27',e BYTEA DEFAULT '\x')`, 0)
			names, types := []string{"id", "v", "e"}, []string{"INT4", "BYTEA", "BYTEA"}
			var want [][]any
			defaultConsumerRows(t, db, prepared, names, types, want)
			defaultConsumerRoundtrip(t, db, `DEFAULT '\x0080ff5c27'`)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(1),(2)", 2)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(3,DEFAULT,DEFAULT),(4,NULL,NULL)", 2)
			for id := int64(1); id <= 3; id++ {
				want = append(want, []any{id, []byte{0, 128, 255, 92, 39}, []byte{}})
			}
			want = append(want, []any{int64(4), nil, nil})
			defaultConsumerRows(t, db, prepared, names, types, want)
			defaultConsumerRoundtrip(t, db, `DEFAULT '\x0080ff5c27'`)
			defaultConsumerRows(t, db, prepared, names, types, want)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(5)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(6,DEFAULT,DEFAULT)", 1)
			for id := int64(5); id <= 6; id++ {
				want = append(want, []any{id, []byte{0, 128, 255, 92, 39}, []byte{}})
			}
			defaultConsumerRows(t, db, prepared, names, types, want)
		})
	}
}

func TestDefaultQueryPlanningDecisive(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"TRUE OR CAST('oops'::text AS INT)=1", true},
		{"FALSE AND CAST('oops'::text AS INT)=1", false},
		{"CAST(1 AS BOOL) OR CAST('oops'::text AS INT)=1", true},
		{"CAST(0 AS BOOL) AND CAST('oops'::text AS INT)=1", false},
		{"(NOT FALSE) OR CAST('oops'::text AS INT)=1", true},
		{"(1=1) OR CAST('oops'::text AS INT)=1", true},
		{"(NULL IS NULL) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE IS TRUE) OR CAST('oops'::text AS INT)=1", true},
		{"(1 IS NOT DISTINCT FROM 1) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE OR CAST('oops'::text AS INT)=1) OR CAST('oops'::text AS INT)=1", true},
	} {
		for _, prepared := range []bool{false, true} {
			for _, populated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v/populated=%v", tc.expression, prepared, populated), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
					tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,b BOOL DEFAULT ("+tc.expression+"))", 0)
					tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,NULL)", 1)
					count := int64(0)
					if populated {
						tableCoreExec(t, db, prepared, "INSERT INTO src VALUES(1),(2)", 2)
						count = 2
					}
					// Supplied NULL bypasses the bad definition even before its
					// decisive planning behavior is exercised by omitted columns.
					tableCoreExec(t, db, prepared, "INSERT INTO d(id,b) SELECT 9,NULL FROM src", count)
					tableCoreExec(t, db, prepared, "INSERT INTO d(id) SELECT id FROM src ORDER BY id", count)
					want := [][]any{{int64(0), nil}}
					if populated {
						want = append(want, []any{int64(1), tc.want}, []any{int64(2), tc.want}, []any{int64(9), nil}, []any{int64(9), nil})
					}
					defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, want)
					// The execution-only decision must not replace the stored
					// expression, including its still-deferred discarded cast.
					defaultConsumerRoundtrip(t, db, "'oops'")
					defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, want)
					tableCoreExec(t, db, prepared, "INSERT INTO d(id) SELECT id FROM src ORDER BY id", count)
					if populated {
						want = [][]any{{int64(0), nil}, {int64(1), tc.want}, {int64(1), tc.want}, {int64(2), tc.want}, {int64(2), tc.want}, {int64(9), nil}, {int64(9), nil}}
					}
					defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, want)
				})
			}
		}
	}
}

func TestDefaultQueryPlanningRequiredBranches(t *testing.T) {
	for _, expression := range []string{
		"NULL OR CAST('oops'::text AS INT)=1",
		"NULL AND CAST('oops'::text AS INT)=1",
		"FALSE OR CAST('oops'::text AS INT)=1",
		"TRUE AND CAST('oops'::text AS INT)=1",
		"CAST('oops'::text AS INT)=1 OR TRUE",
		"CAST('oops'::text AS INT)=1 AND FALSE",
		"(TRUE OR CAST('discarded'::text AS INT)=1) AND CAST('oops'::text AS INT)=1",
	} {
		for _, prepared := range []bool{false, true} {
			for _, populated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v/populated=%v", expression, prepared, populated), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
					tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,b BOOL DEFAULT ("+expression+"))", 0)
					tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,TRUE)", 1)
					if populated {
						tableCoreExec(t, db, prepared, "INSERT INTO src VALUES(1),(2)", 2)
					}
					q := "INSERT INTO d(id) SELECT id FROM src ORDER BY id"
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if e != nil {
							t.Fatal("conversion is demanded at Execute", e)
						}
						_, err = s.Exec()
						s.Close()
					} else {
						_, err = db.Exec(q)
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
					defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), true}})
				})
			}
		}
	}
}

func TestDefaultQueryPlanningAnalysis(t *testing.T) {
	for _, tc := range []struct{ expression, token, code, message string }{
		{"TRUE OR CAST($1 AS INT)=1", "$1", "42P02", "there is no parameter $1"},
		{"FALSE AND CAST(b AS INT)=1", "b AS", "0A000", "cannot use column reference in DEFAULT expression"},
		{"TRUE OR CAST('oops' AS INT)=1", "'oops'", "22P02", `invalid input syntax for type integer: "oops"`},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.expression, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				q := "CREATE TABLE bad(b BOOL DEFAULT (" + tc.expression + "))"
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("utility Prepare must remain analysis-free", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(q, tc.token) + 1)})
				_, err = db.Query("SELECT * FROM bad")
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
			})
		}
	}
}
