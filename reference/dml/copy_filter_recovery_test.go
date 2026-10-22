package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterIgnore(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		for _, format := range []string{"text", "csv"} {
			t.Run(format, func(t *testing.T) {
				c.exec(t, "CREATE TABLE copy_ignore(id int, other int)")
				defer c.exec(t, "DROP TABLE copy_ignore")
				input := "1\t10\nbad\talso_bad\n2\t20\n2147483648\tbad\n3\t30\n"
				if format == "csv" {
					input = "1,10\nbad,also_bad\n2,20\n2147483648,bad\n3,30\n"
				}
				copyFilterImport(t, c, "COPY copy_ignore FROM STDIN WITH (FORMAT "+format+",ON_ERROR ignore,REJECT_LIMIT 2,LOG_VERBOSITY verbose)", input, 3)
				c.selectRows(t, "SELECT id,other FROM copy_ignore ORDER BY id", []string{"id", "other"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}})
				copyFilterNotices(t, *notices, copyFilterRowNotice("copy_ignore", "id", "\"bad\"", 2), copyFilterRowNotice("copy_ignore", "id", "\"2147483648\"", 4), copyFilterSummary(2))
				*notices = (*notices)[:0]
			})
		}
		for _, tc := range []struct {
			name, typ, good, bad, databaseType string
			value                              any
		}{
			{"array", "int[]", "{1,2}", "{bad}", "_INT4", "{1,2}"},
			{"typmod", "varchar(2)", "ok", "long", "VARCHAR", "ok"},
			{"range", "int4range", "[1,3)", "[bad,3)", "INT4RANGE", "[1,3)"},
			{"numeric", "numeric(3,1)", "1.2", "1234.5", "NUMERIC", "1.2"},
			{"bool", "bool", "true", "perhaps", "BOOL", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				table := "copy_soft_" + tc.name
				c.exec(t, "CREATE TABLE "+table+"(id int,v "+tc.typ+")")
				defer c.exec(t, "DROP TABLE "+table)
				copyFilterImport(t, c, "COPY "+table+" FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY silent)", "1\t"+tc.good+"\n2\t"+tc.bad+"\n3\t"+tc.good+"\n", 2)
				c.selectRows(t, "SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", tc.databaseType}, [][]any{{int64(1), tc.value}, {int64(3), tc.value}})
				copyFilterNotices(t, *notices)
			})
		}
	})
}

func TestReferenceCopyFilterRejectLimit(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		for _, format := range []string{"text", "csv"} {
			for _, transaction := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/transaction_%t", format, transaction), func(t *testing.T) {
					c.exec(t, "CREATE TABLE copy_limit(id int)")
					defer c.exec(t, "DROP TABLE copy_limit")
					c.exec(t, "INSERT INTO copy_limit VALUES(99)")
					q := "COPY copy_limit FROM STDIN WITH (FORMAT " + format + ",ON_ERROR ignore,REJECT_LIMIT 2,LOG_VERBOSITY verbose)"
					copyFilterImport(t, c, q, "1\nbad\n2\nworse\n3\n", 3)
					copyFilterIDs(t, c, "copy_limit", 1, 2, 3, 99)
					copyFilterNotices(t, *notices, copyFilterRowNotice("copy_limit", "id", "\"bad\"", 2), copyFilterRowNotice("copy_limit", "id", "\"worse\"", 4), copyFilterSummary(2))
					*notices = (*notices)[:0]
					c.exec(t, "DELETE FROM copy_limit WHERE id<>99")
					if transaction {
						c.exec(t, "BEGIN")
						defer c.exec(t, "ROLLBACK")
						c.exec(t, "SAVEPOINT before_copy")
					}
					copyFilterFailure(t, c, q, "1\nbad\n2\nworse\n3\nworst\n4\n", copyFilterLimitError("copy_limit", "worst", 6, 2))
					// The over-limit row emits its verbose record before the limit test.
					copyFilterNotices(t, *notices, copyFilterRowNotice("copy_limit", "id", "\"bad\"", 2), copyFilterRowNotice("copy_limit", "id", "\"worse\"", 4), copyFilterRowNotice("copy_limit", "id", "\"worst\"", 6))
					*notices = (*notices)[:0]
					if transaction {
						_, err := c.conn.ExecContext(c.ctx, "SELECT 1")
						fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						c.exec(t, "ROLLBACK TO before_copy")
					}
					copyFilterIDs(t, c, "copy_limit", 99)
					copyFilterImport(t, c, "COPY copy_limit FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY silent)", "bad\nworse\nworst\n1\n", 1)
					copyFilterIDs(t, c, "copy_limit", 1, 99)
					copyFilterNotices(t, *notices)
				})
			}
		}
	})
}

func TestReferenceCopyFilterDefaults(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		t.Run("omitted_on_rejected_and_filtered", func(t *testing.T) {
			c.exec(t, "CREATE SEQUENCE copy_default_seq")
			defer c.exec(t, "DROP SEQUENCE copy_default_seq")
			c.exec(t, "CREATE TABLE copy_default(id int,stamp bigint DEFAULT nextval('copy_default_seq'))")
			defer c.exec(t, "DROP TABLE copy_default")
			copyFilterImport(t, c, "COPY copy_default(id) FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY silent) WHERE id<>2", "1\nbad\n2\n3\n", 2)
			c.selectRows(t, "SELECT id,stamp FROM copy_default ORDER BY id", []string{"id", "stamp"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(3), int64(4)}})
			c.selectRows(t, "SELECT last_value,is_called FROM copy_default_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(4), true}})
			copyFilterImport(t, c, "COPY copy_default(id) FROM STDIN WITH (ON_ERROR ignore)", "", 0)
			copyFilterImport(t, c, "COPY copy_default(id) FROM STDIN WITH (FORMAT csv,HEADER MATCH,ON_ERROR ignore)", "id\n", 0)
			c.selectRows(t, "SELECT last_value FROM copy_default_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(4)}})
			copyFilterFailure(t, c, "COPY copy_default(id) FROM STDIN WITH (ON_ERROR ignore,REJECT_LIMIT 1,LOG_VERBOSITY silent)", "4\nbad\nworse\n", copyFilterLimitError("copy_default", "worse", 3, 1))
			c.selectRows(t, "SELECT last_value FROM copy_default_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(7)}})
			copyFilterIDs(t, c, "copy_default", 1, 3)
			copyFilterNotices(t, *notices)
		})
		t.Run("selected_marker_demand", func(t *testing.T) {
			c.exec(t, "CREATE SEQUENCE copy_marker_seq")
			defer c.exec(t, "DROP SEQUENCE copy_marker_seq")
			c.exec(t, "CREATE TABLE copy_marker(id int,stamp bigint DEFAULT nextval('copy_marker_seq'))")
			defer c.exec(t, "DROP TABLE copy_marker")
			copyFilterImport(t, c, "COPY copy_marker(stamp,id) FROM STDIN WITH (DEFAULT 'D',ON_ERROR ignore,LOG_VERBOSITY silent)", "D\tbad\nD\t1\n", 1)
			copyFilterImport(t, c, "COPY copy_marker(id,stamp) FROM STDIN WITH (DEFAULT 'D',ON_ERROR ignore,LOG_VERBOSITY silent)", "bad\tD\n2\tD\n", 1)
			c.selectRows(t, "SELECT id,stamp FROM copy_marker ORDER BY id", []string{"id", "stamp"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}})
			c.selectRows(t, "SELECT last_value FROM copy_marker_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}})
			// Soft failure stops later missing-field checks; excess-field checks run first.
			copyFilterImport(t, c, "COPY copy_marker FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY silent)", "bad\n", 0)
			copyFilterFailure(t, c, "COPY copy_marker FROM STDIN WITH (ON_ERROR ignore)", "bad\t1\textra\n", fixture.Diagnostic{Code: "22P04", Message: "extra data after last expected column", Context: "COPY copy_marker, line 1: \"bad\t1\textra\""})
			copyFilterNotices(t, *notices)
		})
		t.Run("selected_marker_copy_order", func(t *testing.T) {
			c.exec(t, "CREATE SEQUENCE copy_order_seq")
			defer c.exec(t, "DROP SEQUENCE copy_order_seq")
			c.exec(t, "CREATE TABLE copy_order_default(id int,a bigint DEFAULT nextval('copy_order_seq'),b bigint DEFAULT nextval('copy_order_seq'))")
			defer c.exec(t, "DROP TABLE copy_order_default")
			copyFilterImport(t, c, "COPY copy_order_default(b,a,id) FROM STDIN WITH (DEFAULT 'D',ON_ERROR ignore,LOG_VERBOSITY silent)", "D\tD\t1\n", 1)
			c.selectRows(t, "SELECT id,a,b FROM copy_order_default", []string{"id", "a", "b"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(1)}})
			copyFilterNotices(t, *notices)
		})
		t.Run("selected_default_before_later_field", func(t *testing.T) {
			c.exec(t, "CREATE SEQUENCE copy_selected_seq MINVALUE 0 START 1 MAXVALUE 1 NO CYCLE")
			defer c.exec(t, "DROP SEQUENCE copy_selected_seq")
			c.exec(t, "CREATE TABLE copy_selected_default(id int,stamp bigint DEFAULT nextval('copy_selected_seq'))")
			defer c.exec(t, "DROP TABLE copy_selected_default")
			c.exec(t, "INSERT INTO copy_selected_default VALUES(99,99)")
			c.selectRows(t, "SELECT nextval('copy_selected_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
			for _, input := range []string{"D\tbad\n", "D\n"} {
				copyFilterFailure(t, c, "COPY copy_selected_default(stamp,id) FROM STDIN WITH (DEFAULT 'D',ON_ERROR ignore,LOG_VERBOSITY verbose)", input, fixture.Diagnostic{Code: "2200H", Message: "nextval: reached maximum value of sequence \"copy_selected_seq\" (1)", Context: "COPY copy_selected_default, line 1, column stamp: \"D\""})
				copyFilterIDs(t, c, "copy_selected_default", 99)
				copyFilterNotices(t, *notices)
			}
		})
		t.Run("hard_default_overrides_rejection", func(t *testing.T) {
			c.exec(t, "CREATE SEQUENCE copy_hard_seq MINVALUE 0 START 1 MAXVALUE 1 NO CYCLE")
			defer c.exec(t, "DROP SEQUENCE copy_hard_seq")
			c.exec(t, "CREATE TABLE copy_hard_default(id int,stamp bigint DEFAULT nextval('copy_hard_seq'))")
			defer c.exec(t, "DROP TABLE copy_hard_default")
			c.exec(t, "INSERT INTO copy_hard_default VALUES(99,99)")
			copyFilterFailure(t, c, "COPY copy_hard_default(id) FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose)", "1\nbad\n", fixture.Diagnostic{Code: "2200H", Message: "nextval: reached maximum value of sequence \"copy_hard_seq\" (1)", Context: "COPY copy_hard_default, line 2, column id: \"bad\""})
			copyFilterIDs(t, c, "copy_hard_default", 99)
			copyFilterNotices(t, *notices, copyFilterRowNotice("copy_hard_default", "id", "\"bad\"", 2))
		})
	})
}
