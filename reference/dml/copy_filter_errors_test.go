package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterHardErrors(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		for _, tc := range []struct {
			name, ddl, options, input string
			want                      fixture.Diagnostic
		}{
			{"missing", "id int,v int", "", "1\t1\n2\n", fixture.Diagnostic{Code: "22P04", Message: "missing data for column \"v\"", Context: "COPY copy_hard, line 2: \"2\""}},
			{"extra", "id int,v int", "", "1\t1\n2\t2\t3\n", fixture.Diagnostic{Code: "22P04", Message: "extra data after last expected column", Context: "COPY copy_hard, line 2: \"2\t2\t3\""}},
			{"csv", "id int,v text", ",FORMAT csv", "1,ok\n2,\"unterminated\n", fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_hard, line 3: \"2,\"unterminated\n\""}},
			{"header", "id int,v int", ",FORMAT csv,HEADER MATCH", "id,wrong\n1,1\n", fixture.Diagnostic{Code: "22P04", Message: "column name mismatch in header line field 2: got \"wrong\", expected \"v\"", Context: "COPY copy_hard, line 1: \"id,wrong\""}},
			{"encoding", "id int,v text", "", "1\tok\n2\t\xff\n", fixture.Diagnostic{Code: "22021", Message: "invalid byte sequence for encoding \"UTF8\": 0xff", Context: "COPY copy_hard, line 2"}},
			{"not_null", "id int,v int NOT NULL", "", "1\t1\n2\t\\N\n", fixture.Diagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"copy_hard\" violates not-null constraint", Detail: "Failing row contains (2, null).", Context: "COPY copy_hard, line 2: \"2\t\\N\"", Schema: "public", Table: "copy_hard", Column: "v"}},
			{"check", "id int,v int CONSTRAINT positive CHECK(v>0)", "", "1\t1\n2\t-1\n", fixture.Diagnostic{Code: "23514", Message: "new row for relation \"copy_hard\" violates check constraint \"positive\"", Detail: "Failing row contains (2, -1).", Context: "COPY copy_hard, line 2: \"2\t-1\"", Schema: "public", Table: "copy_hard", Constraint: "positive"}},
			{"unique", "id int PRIMARY KEY,v int", "", "1\t1\n99\t2\n", fixture.Diagnostic{Code: "23505", Message: "duplicate key value violates unique constraint \"copy_hard_pkey\"", Detail: "Key (id)=(99) already exists.", Context: "COPY copy_hard, line 2", Schema: "public", Table: "copy_hard", Constraint: "copy_hard_pkey"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c.exec(t, "CREATE TABLE copy_hard("+tc.ddl+")")
				defer c.exec(t, "DROP TABLE copy_hard")
				c.exec(t, "INSERT INTO copy_hard VALUES(99,99)")
				copyFilterFailure(t, c, "COPY copy_hard FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose"+tc.options+")", tc.input, tc.want)
				copyFilterIDs(t, c, "copy_hard", 99)
				copyFilterNotices(t, *notices)
			})
		}
	})
}

func TestReferenceCopyFilterDomains(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE DOMAIN copy_positive AS int CONSTRAINT positive CHECK(VALUE>0)")
		c.exec(t, "CREATE DOMAIN copy_required AS int NOT NULL")
		c.exec(t, "CREATE DOMAIN copy_nested AS copy_positive")
		c.exec(t, "CREATE DOMAIN copy_cast AS text CONSTRAINT cast_positive CHECK(VALUE::int>0)")
		c.exec(t, "CREATE DOMAIN copy_divide AS int CONSTRAINT divide_positive CHECK(10/VALUE>0)")
		for _, tc := range []struct {
			name, typ, input string
			count            int64
			ids              []int64
		}{
			{"own_check", "copy_positive", "1\t1\n2\t-1\n3\t2\n", 2, []int64{1, 3, 99}},
			{"own_null", "copy_required", "1\t1\n2\t\\N\n3\t2\n", 2, []int64{1, 3, 99}},
			{"nested", "copy_nested", "1\t1\n2\t-1\n3\t2\n", 2, []int64{1, 3, 99}},
			{"array", "copy_positive[]", "1\t{1,2}\n2\t{1,-1}\n3\t{2,3}\n", 2, []int64{1, 3, 99}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c.exec(t, "CREATE TABLE copy_domain(id int,v "+tc.typ+")")
				defer c.exec(t, "DROP TABLE copy_domain")
				if tc.name == "array" {
					c.exec(t, "INSERT INTO copy_domain VALUES(99,'{99}')")
				} else {
					c.exec(t, "INSERT INTO copy_domain VALUES(99,99)")
				}
				copyFilterImport(t, c, "COPY copy_domain FROM STDIN WITH (ON_ERROR ignore)", tc.input, tc.count)
				copyFilterIDs(t, c, "copy_domain", tc.ids...)
				copyFilterNotices(t, *notices, copyFilterSummary(1))
				*notices = (*notices)[:0]
			})
		}
		for _, tc := range []struct {
			name, typ, bad string
			want           fixture.Diagnostic
		}{
			{"stop_check", "copy_positive", "-1", fixture.Diagnostic{Code: "23514", Message: "value for domain copy_positive violates check constraint \"positive\"", Schema: "public", DataType: "copy_positive", Constraint: "positive", Context: "COPY copy_domain, line 2, column v: \"-1\""}},
			{"stop_null", "copy_required", "\\N", fixture.Diagnostic{Code: "23502", Message: "domain copy_required does not allow null values", Schema: "public", DataType: "copy_required", Context: "COPY copy_domain, line 2, column v: null input"}},
			{"stop_nested", "copy_nested", "-1", fixture.Diagnostic{Code: "23514", Message: "value for domain copy_nested violates check constraint \"positive\"", Schema: "public", DataType: "copy_nested", Constraint: "positive", Context: "COPY copy_domain, line 2, column v: \"-1\""}},
			{"stop_array", "copy_positive[]", "{1,-1}", fixture.Diagnostic{Code: "23514", Message: "value for domain copy_positive violates check constraint \"positive\"", Schema: "public", DataType: "copy_positive", Constraint: "positive", Context: "COPY copy_domain, line 2, column v: \"{1,-1}\""}},
			{"hard_cast", "copy_cast", "bad", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Context: "COPY copy_domain, line 2, column v: \"bad\""}},
			{"hard_divide", "copy_divide", "0", fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "COPY copy_domain, line 2, column v: \"0\""}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c.exec(t, "CREATE TABLE copy_domain(id int,v "+tc.typ+")")
				defer c.exec(t, "DROP TABLE copy_domain")
				good := "1"
				if tc.name == "stop_array" {
					good = "{1}"
				}
				c.exec(t, "INSERT INTO copy_domain VALUES(99,'"+good+"')")
				action := "ignore"
				if tc.name != "hard_cast" && tc.name != "hard_divide" {
					action = "stop"
				}
				copyFilterFailure(t, c, "COPY copy_domain FROM STDIN WITH (ON_ERROR "+action+",LOG_VERBOSITY verbose)", "1\t"+good+"\n2\t"+tc.bad+"\n", tc.want)
				copyFilterIDs(t, c, "copy_domain", 99)
				copyFilterNotices(t, *notices)
			})
		}
		c.exec(t, "CREATE TABLE copy_domain_null(id int,v copy_required)")
		copyFilterImport(t, c, "COPY copy_domain_null FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose)", "1\t\\N\n", 0)
		copyFilterNotices(t, *notices, copyFilterRowNotice("copy_domain_null", "v", "null input", 1), copyFilterSummary(1))
	})
}
