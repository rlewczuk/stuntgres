package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// alterDefaultListScenario runs one shared body per backend and per prepared
// mode. No expectation here is backend-specific except the documented native
// partition-hierarchy boundary in TestReferenceAlterDefaultListPartitionBoundary.
func alterDefaultListScenario(t *testing.T, prepared bool, scenario func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			scenario(t, p, b, run)
		})
	}
}

func alterDefaultListAttrs(table string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT attname,atthasdef FROM pg_attribute WHERE attrelid='"+table+"'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atthasdef"}, []string{"NAME", "BOOL"}, rows)
}

func alterDefaultListExpressions(table string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='"+table+"'::regclass ORDER BY adnum", []string{"pg_get_expr"}, []string{"TEXT"}, rows)
}

func alterDefaultListSyntaxStage(s fixture.Step, prepared bool) fixture.Step {
	s.ErrorStage = "execute"
	if prepared {
		s.ErrorStage = "prepare"
	}
	return s
}

func TestReferenceAlterDefaultListLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			alterDefaultListScenario(t, prepared, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN b SET DEFAULT 2", 0))
				run(alterDefaultListAttrs("t", [][]any{{"a", true}, {"b", true}}))
				run(alterDefaultListExpressions("t", [][]any{{"1"}, {"2"}}))
				run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
				run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a DROP DEFAULT, ALTER b DROP DEFAULT", 0))
				run(alterDefaultListAttrs("t", [][]any{{"a", false}, {"b", false}}))
				run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='t'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
				run(fixture.Select("SELECT a,b FROM t ORDER BY a NULLS LAST", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {nil, nil}}))
				run(fixture.Exec("ALTER TABLE ONLY t ALTER COLUMN a SET DEFAULT 5, ALTER COLUMN a SET DEFAULT 6", 0))
				run(alterDefaultListExpressions("t", [][]any{{"6"}}))
				run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
				// The valid mixed shape: the add pass fills existing rows with 7,
				// then the SET pass stores 9 for future rows without rewriting them.
				run(fixture.Exec("ALTER TABLE t ADD COLUMN c INT DEFAULT 7, ALTER COLUMN c SET DEFAULT 9", 0))
				run(alterDefaultListAttrs("t", [][]any{{"a", true}, {"b", false}, {"c", true}}))
				run(alterDefaultListExpressions("t", [][]any{{"6"}, {"9"}}))
				run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
				run(fixture.Select("SELECT a,c FROM t ORDER BY a NULLS LAST, c", []string{"a", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(6), int64(7)}, {int64(6), int64(9)}, {nil, int64(7)}}))
			})
		})
	}
}

func TestReferenceAlterDefaultListPassOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			alterDefaultListScenario(t, prepared, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(a INT NOT NULL,b INT DEFAULT 3,c INT)", 0))
				// DROP DEFAULT runs in PostgreSQL's first pass, so even a textual
				// SET-then-DROP list keeps the SET value.
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN a DROP DEFAULT", 0))
				run(alterDefaultListExpressions("t", [][]any{{"1"}, {"3"}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a DROP DEFAULT, ALTER COLUMN a SET DEFAULT 3", 0))
				run(alterDefaultListExpressions("t", [][]any{{"3"}, {"3"}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a SET DEFAULT 4, ALTER COLUMN a SET DEFAULT 5", 0))
				run(alterDefaultListExpressions("t", [][]any{{"5"}, {"3"}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN a SET DEFAULT 2, ALTER COLUMN a DROP DEFAULT", 0))
				run(alterDefaultListExpressions("t", [][]any{{"2"}, {"3"}}))
				// PostgreSQL 18.6 rejects this witness: DROP DEFAULT is AT_PASS_DROP
				// and runs before ADD COLUMN, so the column does not exist yet and
				// nothing is applied. This is the oracle behavior, not "both apply".
				dropAfterAdd := fixture.ImplicitError("ALTER TABLE t ADD COLUMN d INT DEFAULT 1, ALTER COLUMN d DROP DEFAULT", fixture.Diagnostic{Code: "42703", Message: `column "d" of relation "t" does not exist`})
				dropAfterAdd.ErrorStage = "execute"
				run(dropAfterAdd)
				run(fixture.Select("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='t'::regclass AND attname='d'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(alterDefaultListExpressions("t", [][]any{{"2"}, {"3"}}))
				addAfterDrop := fixture.ImplicitError("ALTER TABLE t ALTER COLUMN d DROP DEFAULT, ADD COLUMN d INT DEFAULT 1", fixture.Diagnostic{Code: "42703", Message: `column "d" of relation "t" does not exist`})
				addAfterDrop.ErrorStage = "execute"
				run(addAfterDrop)
				run(fixture.Select("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='t'::regclass AND attname='d'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(alterDefaultListExpressions("t", [][]any{{"2"}, {"3"}}))
				// SET DEFAULT is after ADD COLUMN, so a list can add a column and
				// retain an independent SET on an existing one.
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a SET DEFAULT 9, ADD COLUMN e INT", 0))
				run(fixture.Select("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='t'::regclass AND attname='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(alterDefaultListExpressions("t", [][]any{{"9"}, {"3"}}))
				// DROP COLUMN runs before SET DEFAULT, so a SET naming the dropped
				// column fails and both the column and prior defaults survive.
				dropSet := fixture.ImplicitError("ALTER TABLE t ALTER COLUMN a SET DEFAULT 1, DROP COLUMN a", fixture.Diagnostic{Code: "42703", Message: `column "a" of relation "t" does not exist`})
				dropSet.ErrorStage = "execute"
				run(dropSet)
				run(fixture.Select("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='t'::regclass AND attname='a'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(alterDefaultListExpressions("t", [][]any{{"9"}, {"3"}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a TYPE bigint, ALTER COLUMN a SET DEFAULT 5", 0))
				run(fixture.Select("SELECT format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid='t'::regclass AND attname='a'", []string{"format_type"}, []string{"TEXT"}, [][]any{{"bigint"}}))
				run(alterDefaultListExpressions("t", [][]any{{"5"}, {"3"}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a SET DEFAULT 5, ALTER COLUMN a TYPE bigint", 0))
				run(alterDefaultListExpressions("t", [][]any{{"5"}, {"3"}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a DROP NOT NULL, ALTER COLUMN a SET DEFAULT 6", 0))
				run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='t'::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{false}}))
				run(alterDefaultListExpressions("t", [][]any{{"6"}, {"3"}}))
				// No-op default actions keep the candidate unchanged.
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN e DROP DEFAULT, ALTER COLUMN b SET DEFAULT 3", 0))
				run(alterDefaultListExpressions("t", [][]any{{"6"}, {"3"}}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN e SET DEFAULT NULL, ALTER COLUMN a SET DEFAULT 6", 0))
				run(alterDefaultListExpressions("t", [][]any{{"6"}, {"3"}}))
				// SET DEFAULT NULL over an existing default removes it, like DROP.
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN a SET DEFAULT NULL, ALTER COLUMN b DROP DEFAULT", 0))
				run(alterDefaultListExpressions("t", nil))
			})
		})
	}
}

func TestReferenceAlterDefaultListDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			alterDefaultListScenario(t, prepared, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(id INT,i INT DEFAULT 7,b BOOL DEFAULT FALSE,v VARCHAR(2))", 0))
				run(fixture.Exec("INSERT INTO t VALUES(0,11,FALSE,'ok')", 1))
				unchanged := func() {
					run(alterDefaultListExpressions("t", [][]any{{"7"}, {"false"}}))
					run(fixture.Exec("INSERT INTO t(id) VALUES(1)", 1))
					run(fixture.Select("SELECT i,b FROM t WHERE id=1", []string{"i", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(7), false}}))
					run(fixture.Exec("DELETE FROM t WHERE id=1", 1))
				}
				for _, q := range []string{
					"ALTER TABLE t ALTER COLUMN nope SET DEFAULT 1, ALTER COLUMN i SET DEFAULT 2",
					"ALTER TABLE t ALTER COLUMN i SET DEFAULT 2, ALTER COLUMN nope DROP DEFAULT, ALTER COLUMN b SET DEFAULT TRUE",
					"ALTER TABLE t ALTER COLUMN i SET DEFAULT 2, ALTER COLUMN b SET DEFAULT TRUE, ALTER COLUMN nope DROP DEFAULT",
				} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "nope" of relation "t" does not exist`})
					s.ErrorStage = "execute"
					run(s)
					unchanged()
				}
				for _, tc := range []struct {
					q    string
					want fixture.Diagnostic
				}{
					{"ALTER TABLE t ALTER COLUMN i SET DEFAULT TRUE, ALTER COLUMN b SET DEFAULT TRUE", fixture.Diagnostic{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
					{"ALTER TABLE t ALTER COLUMN i SET DEFAULT 'oops', ALTER COLUMN b SET DEFAULT TRUE", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
					{"ALTER TABLE t ALTER COLUMN i SET DEFAULT '2147483648', ALTER COLUMN b SET DEFAULT TRUE", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`}},
					{"ALTER TABLE t ALTER COLUMN i SET DEFAULT (SELECT 1), ALTER COLUMN b SET DEFAULT TRUE", fixture.Diagnostic{Code: "0A000", Message: "cannot use subquery in DEFAULT expression"}},
					{"ALTER TABLE t ALTER COLUMN i SET DEFAULT i, ALTER COLUMN b SET DEFAULT TRUE", fixture.Diagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression"}},
					{"ALTER TABLE t ALTER COLUMN i SET DEFAULT $1, ALTER COLUMN b SET DEFAULT TRUE", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1"}},
					{"ALTER TABLE t ALTER COLUMN tableoid SET DEFAULT 7, ALTER COLUMN i SET DEFAULT 2", fixture.Diagnostic{Code: "0A000", Message: `cannot alter system column "tableoid"`}},
				} {
					s := fixture.ImplicitError(tc.q, tc.want)
					s.ErrorStage = "execute"
					run(s)
					unchanged()
				}
				// The engine's standalone form reports this construct as a syntax
				// error at the token; only the SQLSTATE is shared with PostgreSQL's
				// "DEFAULT is not allowed in this context". The two backends reject it
				// at raw parse versus parse analysis, so no error stage is pinned.
				run(fixture.Error("ALTER TABLE t ALTER COLUMN i SET DEFAULT DEFAULT, ALTER COLUMN b SET DEFAULT TRUE", "42601"))
				unchanged()
				syntax := "ALTER TABLE t ALTER COLUMN i SET DEFAULT 1, ALTER COLUMN b DROP DEFAULT 7"
				run(alterDefaultListSyntaxStage(fixture.ImplicitError(syntax, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "7"`, Position: fixture.Position(len([]rune(syntax[:strings.Index(syntax, "7")])) + 1)}), prepared))
				unchanged()
				run(fixture.Exec("CREATE SEQUENCE s", 0))
				run(fixture.ImplicitError("ALTER TABLE s ALTER i SET DEFAULT 1, ALTER j SET DEFAULT 2", fixture.Diagnostic{Code: "42809", Message: `ALTER action ALTER COLUMN ... SET DEFAULT cannot be performed on relation "s"`, Detail: "This operation is not supported for sequences."}))
				run(fixture.Exec("ALTER TABLE t ALTER COLUMN i SET DEFAULT 8, ALTER COLUMN b SET DEFAULT TRUE", 0))
				run(alterDefaultListAttrs("t", [][]any{{"id", false}, {"i", true}, {"b", true}, {"v", false}}))
				run(alterDefaultListExpressions("t", [][]any{{"8"}, {"true"}}))
				run(fixture.Exec("INSERT INTO t(id) VALUES(2)", 1))
				run(fixture.Select("SELECT i,b FROM t WHERE id=2", []string{"i", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(8), true}}))
				run(fixture.Select("SELECT * FROM t WHERE id=0", []string{"id", "i", "b", "v"}, []string{"INT4", "INT4", "BOOL", "VARCHAR"}, [][]any{{int64(0), int64(11), false, "ok"}}))
			})
		})
	}
}

func TestReferenceAlterDefaultListCommandRecord(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			alterDefaultListScenario(t, prepared, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE adl_audit(command_tag TEXT,object_type TEXT,object_identity TEXT)", 0))
				run(fixture.Exec("CREATE FUNCTION adl_record() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO adl_audit SELECT command_tag,object_type,object_identity FROM pg_event_trigger_ddl_commands(); END$$", 0))
				run(fixture.Exec("CREATE EVENT TRIGGER adl_end ON ddl_command_end WHEN TAG IN ('ALTER TABLE') EXECUTE FUNCTION adl_record()", 0))
				run(fixture.Exec("CREATE TABLE adl_target(a INT,b INT)", 0))
				run(fixture.Exec("ALTER TABLE adl_target ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN b SET DEFAULT 2", 0))
				run(fixture.Select("SELECT command_tag,object_type,object_identity FROM adl_audit", []string{"command_tag", "object_type", "object_identity"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"ALTER TABLE", "table", "public.adl_target"}}))
			})
		})
	}
}

func TestReferenceAlterDefaultListInheritance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			alterDefaultListScenario(t, prepared, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT,i INT DEFAULT 1,j INT)", 0))
				run(fixture.Exec("CREATE TABLE c(i INT DEFAULT 9) INHERITS(p)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(0,1,10)", 1))
				run(fixture.Exec("INSERT INTO c VALUES(10,9,100)", 1))
				run(fixture.Exec("ALTER TABLE p ALTER COLUMN i SET DEFAULT 7, ALTER COLUMN j SET DEFAULT 8", 0))
				run(fixture.Exec("INSERT INTO p(id) VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO c(id) VALUES(11)", 1))
				run(fixture.Exec("ALTER TABLE ONLY p ALTER COLUMN i SET DEFAULT 6, ALTER COLUMN i SET DEFAULT 5", 0))
				run(fixture.Exec("INSERT INTO p(id) VALUES(2)", 1))
				run(fixture.Exec("INSERT INTO c(id) VALUES(12)", 1))
				run(fixture.Exec("ALTER TABLE p ALTER COLUMN i DROP DEFAULT, ALTER COLUMN j DROP DEFAULT", 0))
				run(fixture.Exec("INSERT INTO p(id) VALUES(3)", 1))
				run(fixture.Exec("INSERT INTO c(id) VALUES(13)", 1))
				run(fixture.Exec("ALTER TABLE p ALTER COLUMN i SET DEFAULT 6, ALTER COLUMN j SET DEFAULT 4", 0))
				run(fixture.Exec("ALTER TABLE ONLY p ALTER COLUMN i DROP DEFAULT, ALTER COLUMN j SET DEFAULT 3", 0))
				run(fixture.Exec("INSERT INTO p(id) VALUES(4)", 1))
				run(fixture.Exec("INSERT INTO c(id) VALUES(14)", 1))
				run(fixture.Select("SELECT * FROM ONLY p ORDER BY id", []string{"id", "i", "j"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(1), int64(10)}, {int64(1), int64(7), int64(8)}, {int64(2), int64(5), int64(8)}, {int64(3), nil, nil}, {int64(4), nil, int64(3)}}))
				run(fixture.Select("SELECT * FROM ONLY c ORDER BY id", []string{"id", "i", "j"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(9), int64(100)}, {int64(11), int64(7), int64(8)}, {int64(12), int64(7), int64(8)}, {int64(13), nil, nil}, {int64(14), int64(6), int64(4)}}))
			})
		})
	}
}

func TestReferenceAlterDefaultListPartitionBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			alterDefaultListScenario(t, prepared, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE pp(a INT,b INT) PARTITION BY RANGE(a)", 0))
				run(fixture.Exec("CREATE TABLE pp1 PARTITION OF pp FOR VALUES FROM (0) TO (10)", 0))
				if b.PG {
					run(fixture.Exec("ALTER TABLE pp ALTER COLUMN b SET DEFAULT 7, ALTER COLUMN a SET DEFAULT 0", 0))
					run(alterDefaultListAttrs("pp", [][]any{{"a", true}, {"b", true}}))
					run(alterDefaultListAttrs("pp1", [][]any{{"a", true}, {"b", true}}))
					run(fixture.Exec("INSERT INTO pp DEFAULT VALUES", 1))
					run(fixture.Select("SELECT a,b FROM pp", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(7)}}))
					run(fixture.Exec("ALTER TABLE pp ALTER COLUMN a DROP DEFAULT, ALTER COLUMN b DROP DEFAULT", 0))
					run(alterDefaultListAttrs("pp", [][]any{{"a", false}, {"b", false}}))
					run(alterDefaultListAttrs("pp1", [][]any{{"a", false}, {"b", false}}))
					return
				}
				// Documented native divergence: default actions stay outside the
				// partition-hierarchy contract even inside an action list.
				failure := fixture.ImplicitError("ALTER TABLE pp ALTER COLUMN b SET DEFAULT 7, ALTER COLUMN a SET DEFAULT 0", fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"})
				failure.ErrorStage = "execute"
				run(failure)
				run(alterDefaultListAttrs("pp", [][]any{{"a", false}, {"b", false}}))
				run(alterDefaultListAttrs("pp1", [][]any{{"a", false}, {"b", false}}))
				run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid IN ('pp'::regclass,'pp1'::regclass)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("INSERT INTO pp VALUES(1,2)", 1))
				run(fixture.Select("SELECT a,b FROM pp", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				// The default-only list above is rejected by partition.ConsumerBoundary
				// at admission. This mixed list bypasses that boundary because SET
				// ACCESS METHOD short-circuits it (partition/locks.go), so only the
				// executor guard in AlterTableLocked produces the 0A000. "Nothing
				// applied" here is that guard's public witness.
				mixed := fixture.ImplicitError("ALTER TABLE pp SET ACCESS METHOD heap, ALTER COLUMN b SET DEFAULT 7", fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"})
				mixed.ErrorStage = "execute"
				run(mixed)
				run(alterDefaultListAttrs("pp", [][]any{{"a", false}, {"b", false}}))
				run(alterDefaultListAttrs("pp1", [][]any{{"a", false}, {"b", false}}))
				run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid IN ('pp'::regclass,'pp1'::regclass)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("INSERT INTO pp VALUES(3,4)", 1))
				run(fixture.Select("SELECT a,b FROM pp WHERE a=3", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}))
			})
		})
	}
}

// TestReferenceAlterDefaultListIfExistsMissing witnesses the selected IF EXISTS
// acceptance shape: the missing relation is skipped with one NOTICE before any
// action is resolved, and the same list applies both members once the relation
// exists. The shared alterDefaultListScenario runner cannot observe notices, so
// this test drives the notice-pool connection directly.
func TestReferenceAlterDefaultListIfExistsMissing(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = rec
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					notices = nil
					run(fixture.Exec("ALTER TABLE IF EXISTS adl_missing ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN b DROP DEFAULT", 0))
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "adl_missing" does not exist, skipping`}
					if len(notices) != 1 || notices[0] != want {
						t.Fatalf("missing-relation notices got %#v want %#v", notices, want)
					}
					// Nothing was remembered for the skipped relation.
					run(fixture.Exec("CREATE TABLE adl_missing(a INT,b INT)", 0))
					run(alterDefaultListAttrs("adl_missing", [][]any{{"a", false}, {"b", false}}))
					run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='adl_missing'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					// The same list shape on the now-existing relation applies both.
					run(fixture.Exec("ALTER TABLE IF EXISTS adl_missing ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN b SET DEFAULT 2", 0))
					run(alterDefaultListExpressions("adl_missing", [][]any{{"1"}, {"2"}}))
					run(fixture.Exec("INSERT INTO adl_missing DEFAULT VALUES", 1))
					run(fixture.Select("SELECT a,b FROM adl_missing", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
					// Keep the direct and prepared variants independent on one name.
					run(fixture.Exec("DROP TABLE adl_missing", 0))
				})
			}
		})
	}
}
