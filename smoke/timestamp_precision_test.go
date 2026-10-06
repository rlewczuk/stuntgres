package smoke_test

import (
	"context"
	"database/sql"
	"strconv"
	"stuntgres"
	"testing"
	"time"
)

func timestampPrecisionSmoke(t *testing.T, ctx context.Context, db *sql.DB,
	exec func(string, int64, ...any), check func(string, []string, []string, [][]any, ...any)) {
	t.Helper()
	fail := func(q string, want stuntgres.Error) {
		t.Helper()
		_, err := db.ExecContext(ctx, q)
		if e := typeCoreDiagnostic(err, want); e != nil {
			t.Fatal(q, e)
		}
		stmt, err := db.PrepareContext(ctx, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed precision prepare exposed handle", q)
		}
		if e := typeCoreDiagnostic(err, want); e != nil {
			t.Fatal(q, e)
		}
	}
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	exec("CREATE TABLE precision_anchor(v TIMESTAMP)", 0)
	exec("INSERT INTO precision_anchor VALUES('epoch')", 1)
	for _, head := range []struct {
		sql      string
		position int
	}{
		{"TIMESTAMP", 30}, {`"timestamp"`, 32}, {`U&"t\0069mestamp"`, 38},
	} {
		for _, tc := range []struct {
			spelling, token, primary string
			delta                    int
		}{
			{"1.5foo", "1.5foo", "trailing junk after numeric literal", 0},
			{".5foo", ".5foo", "trailing junk after numeric literal", 0},
			{"1.5e+", "1.5e+", "trailing junk after numeric literal", 0},
			{"1..2", "..", "syntax error", 1},
			{".5e-", ".5e-", "trailing junk after numeric literal", 0},
			{"1.5e", "1.5e", "trailing junk after numeric literal", 0},
			{"1.5e+3foo", "1.5e+3foo", "trailing junk after numeric literal", 0},
			{"1.5_", "1.5_", "trailing junk after numeric literal", 0},
			{"1.5e+3_", "1.5e+3_", "trailing junk after numeric literal", 0},
			{".5", ".5", "syntax error", 0},
			{"1.5e+3", "1.5e+3", "syntax error", 0},
			{"1e+2", "1e+2", "syntax error", 0},
			{"1e+", "1e+", "trailing junk after numeric literal", 0},
			{"1efoo", "1efoo", "trailing junk after numeric literal", 0},
			{"1.5e3foo", "1.5e3foo", "trailing junk after numeric literal", 0},
			{"1.", "1.", "syntax error", 0},
		} {
			if head.sql != "TIMESTAMP" && tc.primary == "syntax error" && tc.delta == 0 {
				continue
			}
			for _, table := range []string{"bad", `"é"`} {
				fail("CREATE TABLE "+table+"(v "+head.sql+"("+tc.spelling+"))", stuntgres.Error{Code: "42601", Message: tc.primary + ` at or near "` + tc.token + `"`, Position: strconv.Itoa(head.position + tc.delta)})
				exec("CREATE TABLE "+table+"(v TIMESTAMP)", 0)
				check("SELECT v FROM "+table, []string{"v"}, []string{"TIMESTAMP"}, nil)
				exec("DROP TABLE "+table, 0)
			}
			if head.sql == "TIMESTAMP" {
				fail("INSERT INTO precision_anchor VALUES('2000-01-01'),(TIMESTAMP("+tc.spelling+") 'epoch')", stuntgres.Error{Code: "42601", Message: tc.primary + ` at or near "` + tc.token + `"`, Position: strconv.Itoa(62 + tc.delta)})
				check("SELECT v FROM precision_anchor", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{epoch}})
			}
		}
	}
	for _, sign := range []string{"+", "-"} {
		fail("CREATE TABLE bad(v TIMESTAMP("+sign+"1))", stuntgres.Error{Code: "42601", Message: `syntax error at or near "` + sign + `"`, Position: "30"})
	}
	for _, tc := range []struct {
		head     string
		position int
	}{{`"timestamp"`, 33}, {`U&"t\0069mestamp"`, 39}} {
		for _, number := range []string{"+1.5foo", "-1..2"} {
			want := stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "1.5foo"`, Position: strconv.Itoa(tc.position)}
			if number == "-1..2" {
				want.Message = `syntax error at or near ".."`
				want.Position = strconv.Itoa(tc.position + 1)
			}
			fail("CREATE TABLE bad(v "+tc.head+"("+number+"))", want)
		}
	}
	for _, q := range []string{`CREATE TABLE precision_signed(v "timestamp"(3))`, `CREATE TABLE precision_unicode_signed(v U&"t\0069mestamp"(3))`} {
		exec(q, 0)
	}
	exec("INSERT INTO precision_anchor VALUES(TIMESTAMP(3) '2000-01-01 00:00:00.1236')", 1)
	check("SELECT v FROM precision_anchor ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{epoch}, {time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC)}})
}
