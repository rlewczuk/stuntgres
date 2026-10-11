package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnReviewDeclarations(t *testing.T) {
	cases := []struct {
		name, query, code, message, token string
		syntax                            bool
	}{
		{"unknown_type", "ALTER TABLE t ADD b missing_type", "42704", `type "missing_type" does not exist`, "missing_type", false},
		{"type_first", "ALTER TABLE t ADD b missing_type DEFAULT 1 DEFAULT 2", "42704", `type "missing_type" does not exist`, "missing_type", false},
		{"duplicate_default", "ALTER TABLE t ADD b INT DEFAULT 1 DEFAULT 2", "42601", `multiple default values specified for column "b" of table "t"`, "DEFAULT 2", false},
		{"conflicting_null", "ALTER TABLE t ADD b INT NULL NOT NULL", "42601", `conflicting NULL/NOT NULL declarations for column "b" of table "t"`, "NOT NULL", false},
		{"unicode_position", "/*é*/ ALTER TABLE t ADD b INT DEFAULT 1 DEFAULT 2", "42601", `multiple default values specified for column "b" of table "t"`, "DEFAULT 2", false},
		{"trailing_token", "ALTER TABLE t ADD b INT trailing", "42601", `syntax error at or near "trailing"`, "trailing", true},
		{"invalid_suffix_after_prefix", "ALTER TABLE t ADD b INT; ALTER TABLE t ADD c INT )", "42601", `syntax error at or near ")"`, ")", true},
		{"check_check_collision", "ALTER TABLE t ADD b INT CONSTRAINT old_check CHECK(b>0)", "42710", `constraint "old_check" for relation "t" already exists`, "", false},
		{"check_nn_collision", "ALTER TABLE t ADD b INT CONSTRAINT old_nn CHECK(b>0)", "42710", `constraint "old_nn" for relation "t" already exists`, "", false},
		{"nn_check_collision", "ALTER TABLE t ADD b INT CONSTRAINT old_check NOT NULL DEFAULT 7", "42710", `constraint "old_check" for relation "t" already exists`, "", false},
		{"nn_nn_collision", "ALTER TABLE t ADD b INT CONSTRAINT old_nn NOT NULL DEFAULT 7", "42710", `constraint "old_nn" for relation "t" already exists`, "", false},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(id INT CONSTRAINT old_nn NOT NULL CONSTRAINT old_check CHECK(id>0), existing INT DEFAULT 3)", 0))
					run(fixture.Exec("INSERT INTO t(id) VALUES(1),(2)", 2))
					position := ""
					if tc.token != "" {
						position = fixture.Position(len([]rune(tc.query[:strings.Index(tc.query, tc.token)])) + 1)
					}
					failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position})
					failure.Prepared = prepared
					failure.ErrorStage = "execute"
					if tc.syntax && prepared {
						failure.ErrorStage = "prepare"
					}
					run(failure)
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "existing"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(3)}}))
					run(fixture.Exec("ALTER TABLE t ADD b INT DEFAULT 7 CONSTRAINT fresh CHECK(b>0)", 0))
					run(fixture.Select("SELECT b FROM t ORDER BY id", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
				})
			})
		}
	}
}
