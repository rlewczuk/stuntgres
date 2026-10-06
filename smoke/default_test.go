package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestDefaultSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			ctx := context.Background()
			exec := func(q string, n int64) {
				t.Helper()
				var result sql.Result
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					result, err = s.Exec()
					s.Close()
				} else {
					result, err = db.Exec(q)
				}
				if err != nil {
					t.Fatal(q, err)
				}
				if count, e := result.RowsAffected(); e != nil || count != n {
					t.Fatal(q, count, e)
				}
			}
			query := func(q string) *sql.Rows {
				t.Helper()
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					defer s.Close()
					rows, e := s.Query()
					if e != nil {
						t.Fatal(e)
					}
					return rows
				}
				rows, e := db.Query(q)
				if e != nil {
					t.Fatal(e)
				}
				return rows
			}
			fail := func(q string, want stuntgres.Error) {
				t.Helper()
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("expected execution failure, not Prepare", q, e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				if e := typeCoreDiagnostic(err, want); e != nil {
					t.Fatal(q, e)
				}
			}
			check := func(want [][]any) {
				t.Helper()
				rows := query(`SELECT * FROM d ORDER BY id`)
				types, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				for i, c := range types {
					nullable, known := c.Nullable()
					scan := reflect.TypeFor[int64]()
					if i == 2 {
						scan = reflect.TypeFor[string]()
					}
					if i == 3 {
						scan = reflect.TypeFor[bool]()
					}
					if !known || !nullable || c.ScanType() != scan {
						t.Fatal("default changed declared metadata", i, c, nullable, known)
					}
				}
				alterDropSmokeRows(t, rows, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, want)
			}
			exec(`CREATE TABLE d(id INT,i INT DEFAULT 7,s TEXT DEFAULT 'O''Brien',b BOOL DEFAULT TRUE,n INT)`, 0)
			exec(`INSERT INTO d(id) VALUES(1),(2)`, 2)
			exec(`INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT)`, 1)
			exec(`INSERT INTO d(id,i) VALUES(4,NULL)`, 1)
			exec(`INSERT INTO d VALUES(5)`, 1)
			want := [][]any{{int64(1), int64(7), "O'Brien", true, nil}, {int64(2), int64(7), "O'Brien", true, nil}, {int64(3), int64(7), "O'Brien", true, nil}, {int64(4), nil, "O'Brien", true, nil}, {int64(5), int64(7), "O'Brien", true, nil}}
			check(want)
			exec(`CREATE TABLE logical_defaults(b BOOL DEFAULT (1 < 2 AND NOT FALSE),u BOOL DEFAULT (NOT NULL))`, 0)
			exec(`INSERT INTO logical_defaults DEFAULT VALUES`, 1)
			alterDropSmokeRows(t, query(`SELECT * FROM logical_defaults`), []string{"b", "u"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}})
			exec(`CREATE TABLE predicate_defaults(id INT,b BOOL DEFAULT (NULL IS NULL),t BOOL DEFAULT (NULL IS UNKNOWN),d BOOL DEFAULT 1 IS DISTINCT FROM 2,r BOOL DEFAULT ((NULL IS NULL) IS NOT DISTINCT FROM TRUE))`, 0)
			predicateWant := [][]any{{int64(1), true, true, true, true}, {int64(2), true, true, true, true}, {int64(3), nil, nil, nil, nil}}
			checkPredicates := func() {
				t.Helper()
				rows := query(`SELECT * FROM predicate_defaults ORDER BY id`)
				metadata, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				for _, c := range metadata {
					nullable, known := c.Nullable()
					if !known || !nullable {
						t.Fatal("predicate default changed nullable metadata", c)
					}
				}
				alterDropSmokeRows(t, rows, []string{"id", "b", "t", "d", "r"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, predicateWant)
			}
			exec(`INSERT INTO predicate_defaults(id) VALUES(1)`, 1)
			exec(`INSERT INTO predicate_defaults VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT),(3,NULL,NULL,NULL,NULL)`, 2)
			checkPredicates()
			exec(`CREATE TABLE all_d(i INT DEFAULT 7,n INT)`, 0)
			exec(`INSERT INTO all_d DEFAULT VALUES`, 1)
			alterDropSmokeRows(t, query(`SELECT * FROM all_d`), []string{"i", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}})
			exec(`CREATE TABLE z()`, 0)
			exec(`INSERT INTO z DEFAULT VALUES`, 1)
			alterDropSmokeRows(t, query(`SELECT * FROM z`), []string{}, []string{}, [][]any{{}})
			for _, tc := range []struct{ query, message, position string }{
				{`INSERT INTO all_d(i) DEFAULT VALUES`, `syntax error at or near "DEFAULT"`, "22"},
				{`CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT NOT NULL)`, `syntax error at or near "NOT"`, "125"},
				{`CREATE TABLE bad(b BOOL DEFAULT TRUE AND FALSE)`, `syntax error at or near "AND"`, "38"},
				{`CREATE TABLE bad(b BOOL DEFAULT TRUE OR FALSE)`, `syntax error at or near "OR"`, "38"},
				{`CREATE TABLE bad(b BOOL DEFAULT NULL IS NULL)`, `syntax error at or near "NULL"`, "41"},
				{`CREATE TABLE bad(b BOOL DEFAULT TRUE IS TRUE)`, `syntax error at or near "TRUE"`, "41"},
				{`CREATE TABLE bad(b BOOL DEFAULT NULL ISNULL)`, `syntax error at or near "ISNULL"`, "38"},
				{`CREATE TABLE bad(b BOOL DEFAULT NULL NOTNULL)`, `syntax error at or near "NOTNULL"`, "38"},
				{`INSERT INTO all_d VALUES(DEFAULT) ORDER BY 1`, `DEFAULT is not allowed in this context`, "26"},
			} {
				if prepared {
					s, e := db.Prepare(tc.query)
					err = e
					if s != nil {
						s.Close()
					}
				} else {
					_, err = db.Exec(tc.query)
				}
				if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "42601", Message: tc.message, Position: tc.position}); e != nil {
					t.Fatal(tc.query, e)
				}
			}
			check(want)
			alterDropSmokeRows(t, query(`SELECT * FROM all_d`), []string{"i", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}})
			exec(`CREATE TABLE nn(id INT,v INT NOT NULL)`, 0)
			exec(`INSERT INTO nn VALUES(1,9)`, 1)
			_, err = db.Exec(`INSERT INTO nn VALUES(2,9),(3,DEFAULT)`)
			if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "nn", Column: "v"}); e != nil {
				t.Fatal(e)
			}
			alterDropSmokeRows(t, query(`SELECT * FROM nn`), []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}})
			stmt, err := db.Prepare(`CREATE TABLE bad (i integer DEFAULT TRUE)`)
			if err != nil {
				t.Fatal("utility preparation", err)
			}
			_, err = stmt.Exec()
			stmt.Close()
			if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}); e != nil {
				t.Fatal(e)
			}
			exec(`CREATE TABLE endpoint(id INT,v TIMESTAMP(6) DEFAULT TIMESTAMP(0) '294276-12-31 23:59:59.999999',b BOOL DEFAULT (TIMESTAMP(0) '294276-12-31 23:59:59.999999' = TIMESTAMP(0) '294276-12-31 23:59:59.999999'))`, 0)
			endpoint := time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)
			checkEndpoint := func(want [][]any) {
				t.Helper()
				rows := query(`SELECT * FROM endpoint ORDER BY id`)
				metadata, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				if metadata[1].ScanType() != reflect.TypeFor[time.Time]() {
					t.Fatal("endpoint ScanType", metadata[1])
				}
				if e := identifierCheckRows(rows, nil, []string{"id", "v", "b"}, []string{"INT4", "TIMESTAMP", "BOOL"}, want); e != nil {
					t.Fatal(e)
				}
			}
			exec(`INSERT INTO endpoint(id) VALUES(1)`, 1)
			checkEndpoint([][]any{{int64(1), endpoint, true}})
			exec(`CREATE TABLE consumer_defaults(id INT,s TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT int4('42'::text),b BOOL DEFAULT (DATE '0001-01-01 BC' < DATE '0001-01-01'),d DATE DEFAULT DATE '2000-02-29',v BYTEA DEFAULT '\x0080ff5c27')`, 0)
			exec(`CREATE TABLE consumer_src(id INT)`, 0)
			exec(`INSERT INTO consumer_src VALUES(1),(2)`, 2)
			exec(`INSERT INTO consumer_defaults(id) VALUES(1)`, 1)
			exec(`INSERT INTO consumer_defaults VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT),(3,NULL,NULL,NULL,NULL,NULL)`, 2)
			exec(`INSERT INTO consumer_defaults(id) SELECT 4`, 1)
			exec(`INSERT INTO consumer_defaults(id) VALUES(5) ORDER BY 1`, 1)
			exec(`INSERT INTO consumer_defaults(id,i) SELECT 6,NULL`, 1)
			exec(`INSERT INTO consumer_defaults(id) SELECT id FROM consumer_src WHERE id=99`, 0)
			consumerRow := func(id int64) []any {
				return []any{id, "1", int64(42), true, "2000-02-29", []byte{0, 128, 255, 92, 39}}
			}
			consumerWant := [][]any{consumerRow(1), consumerRow(2), {int64(3), nil, nil, nil, nil, nil}, consumerRow(4), consumerRow(5), {int64(6), "1", nil, true, "2000-02-29", []byte{0, 128, 255, 92, 39}}}
			checkConsumers := func() {
				t.Helper()
				rows := query(`SELECT * FROM consumer_defaults ORDER BY id`)
				metadata, e := rows.ColumnTypes()
				if e != nil || len(metadata) != 6 {
					t.Fatal(metadata, e)
				}
				scans := []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[string](), reflect.TypeFor[int64](), reflect.TypeFor[bool](), reflect.TypeFor[string](), reflect.TypeFor[[]byte]()}
				for i, c := range metadata {
					nullable, known := c.Nullable()
					if !known || !nullable || c.ScanType() != scans[i] {
						t.Fatal("incoming default metadata", i, c)
					}
				}
				if e := identifierCheckRows(rows, nil, []string{"id", "s", "i", "b", "d", "v"}, []string{"INT4", "TEXT", "INT4", "BOOL", "DATE", "BYTEA"}, consumerWant); e != nil {
					t.Fatal(e)
				}
			}
			checkConsumers()
			exec(`CREATE TABLE cast_deferred(id INT,v INT DEFAULT CAST('oops'::text AS INT))`, 0)
			exec(`INSERT INTO cast_deferred VALUES(1,9)`, 1)
			castError := stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
			fail(`INSERT INTO cast_deferred(id) VALUES(2)`, castError)
			exec(`CREATE TABLE query_bad(id INT,v INT DEFAULT 2147483648)`, 0)
			exec(`INSERT INTO query_bad VALUES(0,9)`, 1)
			fail(`INSERT INTO query_bad(id) SELECT id FROM consumer_src WHERE id=99`, stuntgres.Error{Code: "22003", Message: "integer out of range"})
			exec(`INSERT INTO query_bad(id,v) SELECT id,9 FROM consumer_src ORDER BY id`, 2)
			exec(`CREATE TABLE query_nn(id INT,v INT DEFAULT 7,n INT NOT NULL)`, 0)
			exec(`INSERT INTO query_nn VALUES(0,9,9)`, 1)
			fail(`INSERT INTO query_nn(id,n) VALUES(1,9),(2,NULL) ORDER BY 1`, stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "query_nn" violates not-null constraint`, Detail: "Failing row contains (2, 7, null).", Schema: "public", Table: "query_nn", Column: "n"})
			alterDropSmokeRows(t, query(`SELECT * FROM query_nn`), []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(9), int64(9)}})
			parameterSQL := `CREATE TABLE cast_bad(v INT DEFAULT int4($1))`
			fail(parameterSQL, stuntgres.Error{Code: "42P02", Message: "there is no parameter $1", Position: fmt.Sprint(strings.Index(parameterSQL, "$1") + 1)})
			for _, declaration := range []struct{ sql, code, message, token string }{
				{`CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2 NULL NOT NULL)`, "42601", `multiple default values specified for column "i" of table "bad"`, "DEFAULT 2"},
				{`CREATE TABLE bad(i INT DEFAULT 'oops',j INT DEFAULT 1 DEFAULT 2)`, "42601", `multiple default values specified for column "j" of table "bad"`, "DEFAULT 2"},
				{`CREATE TABLE bad(i INT NULL NOT NULL DEFAULT 1 DEFAULT 2)`, "42601", `conflicting NULL/NOT NULL declarations for column "i" of table "bad"`, "NOT NULL"},
				{`CREATE TABLE bad(j INT DEFAULT 1 DEFAULT 2,i INT DEFAULT 'oops')`, "42601", `multiple default values specified for column "j" of table "bad"`, "DEFAULT 2"},
				{`CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2,j missing_type)`, "42601", `multiple default values specified for column "i" of table "bad"`, "DEFAULT 2"},
				{`CREATE TABLE bad(i missing_type,j INT DEFAULT 1 DEFAULT 2)`, "42704", `type "missing_type" does not exist`, "missing_type"},
			} {
				diagnostic := stuntgres.Error{Code: declaration.code, Message: declaration.message, Position: fmt.Sprint(strings.Index(declaration.sql, declaration.token) + 1)}
				fail(declaration.sql, diagnostic)
				_, missing := db.Query(`SELECT * FROM bad`)
				if e := typeCoreDiagnostic(missing, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}); e != nil {
					t.Fatal(e)
				}
				var before, after bytes.Buffer
				if e := stuntgres.Dump(ctx, db, &before); e != nil {
					t.Fatal(e)
				}
				if e := typeCoreDiagnostic(stuntgres.Restore(ctx, db, strings.NewReader(declaration.sql)), diagnostic); e != nil {
					t.Fatal(e)
				}
				if e := stuntgres.Dump(ctx, db, &after); e != nil || before.String() != after.String() {
					t.Fatal("failed declaration restore changed persisted state", e, after.String())
				}
			}
			exec(`CREATE TABLE planning_src(id INT)`, 0)
			exec(`CREATE TABLE planning_d(id INT,t BOOL DEFAULT (TRUE OR CAST('oops'::text AS INT)=1),f BOOL DEFAULT (FALSE AND CAST('oops'::text AS INT)=1),n BOOL DEFAULT ((NOT FALSE) OR CAST('oops'::text AS INT)=1))`, 0)
			exec(`INSERT INTO planning_d VALUES(0,NULL,NULL,NULL)`, 1)
			exec(`INSERT INTO planning_d(id) SELECT id FROM planning_src`, 0)
			exec(`INSERT INTO planning_d(id) VALUES(3),(4)`, 2)
			exec(`INSERT INTO planning_d VALUES(5,DEFAULT,DEFAULT,DEFAULT)`, 1)
			exec(`INSERT INTO planning_d (VALUES(6,DEFAULT,DEFAULT,DEFAULT))`, 1)
			exec(`INSERT INTO planning_d VALUES(7,NULL,NULL,NULL),(8,FALSE,TRUE,NULL),(10,DEFAULT,DEFAULT,DEFAULT)`, 3)
			exec(`INSERT INTO planning_d DEFAULT VALUES`, 1)
			exec(`CREATE TABLE planning_required(id INT,b BOOL DEFAULT (NULL OR CAST('oops'::text AS INT)=1))`, 0)
			exec(`INSERT INTO planning_required VALUES(0,TRUE)`, 1)
			fail(`INSERT INTO planning_required(id) SELECT id FROM planning_src`, castError)
			for _, q := range []string{
				`INSERT INTO planning_required(id) VALUES(1),(2)`,
				`INSERT INTO planning_required VALUES(1,DEFAULT)`,
				`INSERT INTO planning_required (VALUES(1,DEFAULT))`,
				`INSERT INTO planning_required DEFAULT VALUES`,
				`INSERT INTO planning_required VALUES(1,NULL),(2,FALSE),(3,DEFAULT)`,
			} {
				fail(q, castError)
			}
			exec(`CREATE TABLE planning_left(id INT,b BOOL DEFAULT (CAST('oops'::text AS INT)=1 OR TRUE))`, 0)
			exec(`INSERT INTO planning_left VALUES(0,FALSE)`, 1)
			fail(`INSERT INTO planning_left VALUES(1,DEFAULT)`, castError)
			alterDropSmokeRows(t, query(`SELECT * FROM planning_left`), []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}})
			exec(`CREATE TABLE planning_selective(id INT,b BOOL DEFAULT (TRUE OR CAST('oops'::text AS INT)=1),v INT DEFAULT int4('never'::text))`, 0)
			exec(`INSERT INTO planning_selective VALUES(0,NULL,9),(1,NULL,9),(2,TRUE,9),(3,DEFAULT,9)`, 4)
			exec(`INSERT INTO planning_selective(id,v) VALUES(4,9)`, 1)
			selectiveWant := [][]any{{int64(0), nil, int64(9)}, {int64(1), nil, int64(9)}, {int64(2), true, int64(9)}, {int64(3), true, int64(9)}, {int64(4), true, int64(9)}}
			checkSelective := func() {
				t.Helper()
				alterDropSmokeRows(t, query(`SELECT * FROM planning_selective ORDER BY id`), []string{"id", "b", "v"}, []string{"INT4", "BOOL", "INT4"}, selectiveWant)
			}
			checkSelective()
			alterDropSmokeRows(t, query(`SELECT * FROM planning_required`), []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), true}})
			exec(`INSERT INTO planning_d(id,t,f,n) SELECT 9,NULL,NULL,NULL FROM consumer_src`, 2)
			exec(`INSERT INTO planning_src VALUES(1),(2)`, 2)
			exec(`INSERT INTO planning_d(id) SELECT id FROM planning_src ORDER BY id`, 2)
			planningWant := [][]any{
				{int64(0), nil, nil, nil}, {int64(1), true, false, true}, {int64(2), true, false, true},
				{int64(3), true, false, true}, {int64(4), true, false, true}, {int64(5), true, false, true}, {int64(6), true, false, true},
				{int64(7), nil, nil, nil}, {int64(8), false, true, nil}, {int64(9), nil, nil, nil}, {int64(9), nil, nil, nil},
				{int64(10), true, false, true}, {nil, true, false, true},
			}
			checkPlanning := func() {
				t.Helper()
				alterDropSmokeRows(t, query(`SELECT * FROM planning_d ORDER BY id`), []string{"id", "t", "f", "n"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, planningWant)
			}
			checkPlanning()
			var dump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			for _, witness := range []string{"CAST(CAST('oops' AS TEXT) AS ", "DATE '0001-01-01 BC'", `DEFAULT '\x0080ff5c27'`} {
				if !strings.Contains(dump.String(), witness) {
					t.Fatal("incoming default structure missing", witness, dump.String())
				}
			}
			if err = stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
				t.Fatal("default consumer redump", err, again.String())
			}
			checkConsumers()
			exec(`INSERT INTO consumer_defaults(id) SELECT 7`, 1)
			exec(`INSERT INTO consumer_defaults VALUES(8,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)`, 1)
			consumerWant = append(consumerWant, consumerRow(7), consumerRow(8))
			checkConsumers()
			fail(`INSERT INTO cast_deferred(id) SELECT 2`, castError)
			alterDropSmokeRows(t, query(`SELECT * FROM cast_deferred`), []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}})
			checkPlanning()
			checkSelective()
			exec(`INSERT INTO planning_selective VALUES(5,DEFAULT,9)`, 1)
			selectiveWant = append(selectiveWant, []any{int64(5), true, int64(9)})
			checkSelective()
			exec(`INSERT INTO planning_d(id) VALUES(11)`, 1)
			exec(`INSERT INTO planning_d (VALUES(12,DEFAULT,DEFAULT,DEFAULT))`, 1)
			exec(`INSERT INTO planning_d(id) SELECT id FROM planning_src ORDER BY id`, 2)
			planningWant = [][]any{
				{int64(0), nil, nil, nil}, {int64(1), true, false, true}, {int64(1), true, false, true},
				{int64(2), true, false, true}, {int64(2), true, false, true},
				{int64(3), true, false, true}, {int64(4), true, false, true}, {int64(5), true, false, true}, {int64(6), true, false, true},
				{int64(7), nil, nil, nil}, {int64(8), false, true, nil}, {int64(9), nil, nil, nil}, {int64(9), nil, nil, nil},
				{int64(10), true, false, true}, {int64(11), true, false, true}, {int64(12), true, false, true}, {nil, true, false, true},
			}
			checkPlanning()
			check(want)
			checkPredicates()
			exec(`INSERT INTO predicate_defaults(id) VALUES(4)`, 1)
			exec(`INSERT INTO predicate_defaults VALUES(5,DEFAULT,DEFAULT,DEFAULT,DEFAULT)`, 1)
			predicateWant = append(predicateWant, []any{int64(4), true, true, true, true}, []any{int64(5), true, true, true, true})
			checkPredicates()
			checkEndpoint([][]any{{int64(1), endpoint, true}})
			exec(`INSERT INTO endpoint VALUES(2,DEFAULT,DEFAULT)`, 1)
			checkEndpoint([][]any{{int64(1), endpoint, true}, {int64(2), endpoint, true}})
			exec(`INSERT INTO d(id) VALUES(6)`, 1)
			want = append(want, []any{int64(6), int64(7), "O'Brien", true, nil})
			check(want)
		})
	}
}
