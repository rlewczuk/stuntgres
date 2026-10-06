package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestDefaultValuesPlanningDecisive(t *testing.T) {
	for _, expression := range []struct {
		sql   string
		value bool
	}{
		{"TRUE OR CAST('oops'::text AS INT)=1", true},
		{"FALSE AND CAST('oops'::text AS INT)=1", false},
		{"(NOT FALSE) OR CAST('oops'::text AS INT)=1", true},
		{"(1=1) OR CAST('oops'::text AS INT)=1", true},
	} {
		for _, prepared := range []bool{false, true} {
			for _, form := range []struct {
				name, sql string
				ids       []any
				mixed     bool
			}{
				{"omission-single", "INSERT INTO d(id) VALUES(1)", []any{int64(1)}, false},
				{"omission-multiple", "INSERT INTO d(id) VALUES(1),(2)", []any{int64(1), int64(2)}, false},
				{"requests", "INSERT INTO d VALUES(1,DEFAULT),(2,DEFAULT)", []any{int64(1), int64(2)}, false},
				{"parenthesized-requests", "INSERT INTO d (VALUES(1,DEFAULT),(2,DEFAULT))", []any{int64(1), int64(2)}, false},
				{"default-values", "INSERT INTO d DEFAULT VALUES", []any{nil}, false},
				{"mixed", "INSERT INTO d VALUES(1,NULL),(2,FALSE),(3,DEFAULT)", []any{int64(1), int64(2), int64(3)}, true},
			} {
				t.Run(fmt.Sprintf("%s/prepared=%v/%s", expression.sql, prepared, form.name), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,b BOOL DEFAULT ("+expression.sql+"))", 0)
					defaultConsumerRoundtrip(t, db, "'oops'")
					defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, nil)
					tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,NULL)", 1)
					tableCoreExec(t, db, prepared, form.sql, int64(len(form.ids)))
					want := [][]any{{int64(0), nil}}
					for _, id := range form.ids {
						want = append(want, []any{id, expression.value})
					}
					if form.mixed {
						want = [][]any{{int64(0), nil}, {int64(1), nil}, {int64(2), false}, {int64(3), expression.value}}
					}
					check := func() { defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, want) }
					check()
					defaultConsumerRoundtrip(t, db, "'oops'")
					check()
					tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(9)", 1)
					tableCoreExec(t, db, prepared, "INSERT INTO d (VALUES(10,DEFAULT))", 1)
					// ORDER BY id puts the targetless DEFAULT VALUES occurrence last.
					if form.name == "default-values" {
						want = [][]any{{int64(0), nil}, {int64(9), expression.value}, {int64(10), expression.value}, {nil, expression.value}}
					} else {
						want = append(want, []any{int64(9), expression.value}, []any{int64(10), expression.value})
					}
					check()
				})
			}
		}
	}
}

func TestDefaultValuesPlanningRequiredBranches(t *testing.T) {
	for _, expression := range []string{
		"NULL OR CAST('oops'::text AS INT)=1", "NULL AND CAST('oops'::text AS INT)=1",
		"FALSE OR CAST('oops'::text AS INT)=1", "TRUE AND CAST('oops'::text AS INT)=1",
		"CAST('oops'::text AS INT)=1 OR TRUE", "CAST('oops'::text AS INT)=1 AND FALSE",
		"(TRUE OR CAST('discarded'::text AS INT)=1) AND CAST('oops'::text AS INT)=1",
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", expression, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,b BOOL DEFAULT ("+expression+"))", 0)
				// Neither NULL nor a safe supplied BOOL may demand the bad definition.
				tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,TRUE),(1,NULL),(2,FALSE)", 3)
				seed := [][]any{{int64(0), true}, {int64(1), nil}, {int64(2), false}}
				for _, q := range []string{
					"INSERT INTO d(id) VALUES(3),(4)", "INSERT INTO d VALUES(3,DEFAULT)",
					"INSERT INTO d (VALUES(3,DEFAULT))", "INSERT INTO d DEFAULT VALUES",
					"INSERT INTO d VALUES(3,NULL),(4,TRUE),(5,DEFAULT)",
				} {
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if e != nil {
							t.Fatal("default planning must occur at Execute", e)
						}
						_, err = s.Exec()
						s.Close()
					} else {
						_, err = db.Exec(q)
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
					defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, seed)
				}
			})
		}
	}
}

func TestDefaultValuesPlanningSelectiveColumns(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,b BOOL DEFAULT (TRUE OR CAST('oops'::text AS INT)=1),v INT DEFAULT int4('never'::text))", 0)
			defaultConsumerRoundtrip(t, db, "'never'")
			defaultConsumerRows(t, db, prepared, []string{"id", "b", "v"}, []string{"INT4", "BOOL", "INT4"}, nil)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,NULL,9)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(1,NULL,9),(2,TRUE,9),(3,DEFAULT,9)", 3)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id,v) VALUES(4,9)", 1)
			want := [][]any{{int64(0), nil, int64(9)}, {int64(1), nil, int64(9)}, {int64(2), true, int64(9)}, {int64(3), true, int64(9)}, {int64(4), true, int64(9)}}
			defaultConsumerRows(t, db, prepared, []string{"id", "b", "v"}, []string{"INT4", "BOOL", "INT4"}, want)
			defaultConsumerRoundtrip(t, db, "'never'")
			defaultConsumerRows(t, db, prepared, []string{"id", "b", "v"}, []string{"INT4", "BOOL", "INT4"}, want)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(5,DEFAULT,9)", 1)
			want = append(want, []any{int64(5), true, int64(9)})
			defaultConsumerRows(t, db, prepared, []string{"id", "b", "v"}, []string{"INT4", "BOOL", "INT4"}, want)
		})
	}
}
