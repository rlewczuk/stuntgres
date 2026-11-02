package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceMaterializedStorageDiagnostics(t *testing.T) {
	materializedStorageRoutesMode(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		materializedStorageSeed(run)
		run(fixture.Exec("CREATE TABLE ms_table(id int)", 0))
		run(fixture.Exec("CREATE INDEX ms_other_idx ON ms_table(id)", 0))
		run(fixture.Exec("CREATE INDEX ms_hash ON ms_mv USING hash(id)", 0))
		run(fixture.Exec("CREATE INDEX ms_partial ON ms_mv(id) WHERE id>0", 0))
		run(fixture.Exec("CREATE ACCESS METHOD ms_heap2 TYPE TABLE HANDLER heap_tableam_handler", 0))
		for _, tc := range []struct{ query, state, message, detail string }{
			{"ALTER MATERIALIZED VIEW ms_table SET (fillfactor=70)", "42809", `"ms_table" is not a materialized view`, ""},
			{"ALTER MATERIALIZED VIEW missing SET (fillfactor=70)", "42P01", `relation "missing" does not exist`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER absent SET STATISTICS 7", "42703", `column "absent" of relation "ms_mv" does not exist`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER ctid SET STATISTICS 7", "0A000", `cannot alter system column "ctid"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS -2", "22023", `statistics target -2 is too low`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET (bogus=1)", "22023", `unrecognized parameter "bogus"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET (n_distinct=1,n_distinct=2)", "22023", `parameter "n_distinct" specified more than once`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET (n_distinct='bad')", "22023", `invalid value for floating point option "n_distinct": bad`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET (n_distinct='NaN')", "22023", `invalid value for floating point option "n_distinct": NaN`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id RESET (n_distinct=2)", "42601", "RESET must not include values for parameters", ""},
			{"ALTER MATERIALIZED VIEW ms_mv RESET (fillfactor=70)", "42601", "RESET must not include values for parameters", ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET (bogus=1)", "22023", `unrecognized parameter "bogus"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET (bogus.fillfactor=70)", "22023", `unrecognized parameter namespace "bogus"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=70,fillfactor=80)", "22023", `parameter "fillfactor" specified more than once`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
			{"ALTER MATERIALIZED VIEW ms_mv SET (toast.bogus=1)", "22023", `unrecognized parameter "bogus"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER note SET STORAGE bogus", "22023", `invalid storage type "bogus"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET STORAGE EXTENDED", "0A000", `column data type integer can only have storage PLAIN`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET COMPRESSION pglz", "0A000", `column data type integer does not support compression`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER note SET COMPRESSION bogus", "22023", `invalid compression method "bogus"`, ""},
			{`ALTER MATERIALIZED VIEW ms_mv ALTER note SET COMPRESSION "PGLZ"`, "22023", `invalid compression method "PGLZ"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD absent_am", "42704", `access method "absent_am" does not exist`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD btree", "55000", `access method "btree" is not of type TABLE`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD ms_heap2, SET ACCESS METHOD absent_am", "0A000", `cannot have multiple SET ACCESS METHOD subcommands`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv CLUSTER ON absent_idx", "42704", `index "absent_idx" for table "ms_mv" does not exist`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_table", "42809", `"ms_table" is not an index`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_other_idx", "42809", `"ms_other_idx" is not an index for table "ms_mv"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_hash", "0A000", `cannot cluster on index "ms_hash" because access method does not support clustering`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_partial", "0A000", `cannot cluster on partial index "ms_partial"`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET TABLESPACE absent_space", "42704", `tablespace "absent_space" does not exist`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET TABLESPACE pg_global", "22023", `only shared relations can be placed in pg_global tablespace`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET TABLESPACE pg_default, SET TABLESPACE pg_default", "42601", `cannot have multiple SET TABLESPACE subcommands`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET TABLESPACE pg_default, SET TABLESPACE absent_space", "42704", `tablespace "absent_space" does not exist`, ""},
			{"ALTER MATERIALIZED VIEW ms_mv SET TABLESPACE absent_space, SET TABLESPACE pg_default", "42704", `tablespace "absent_space" does not exist`, ""},
		} {
			want := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			want.Detail = tc.detail
			run(fixture.ErrorDiagnostic(tc.query, want))
			materializedStorageBaseline(run)
		}
		// A real failed concurrent unique build produces an invalid index; no
		// injection of catalog bits or fixture-only validator is involved.
		fixture.ConcurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ms_invalid ON ms_mv(id)", fixture.ConcurrentIndexUnique("ms_mv", "ms_invalid", "Key (id)=(1) is duplicated.", true))
		if state, ok := fixture.ConcurrentIndexState(t, p, b, "ms_invalid"); !ok || state.Valid {
			t.Fatal("missing genuine invalid index", state, ok)
		}
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_invalid", fixture.ControlsReferenceDiagnostic("0A000", `cannot cluster on invalid index "ms_invalid"`)))
		materializedStorageBaseline(run)
		materializedStorageRoles(run)
		run(fixture.Exec("SET ROLE ms_reader", 0))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_table SET (fillfactor=70)", fixture.ControlsReferenceDiagnostic("42501", "must be owner of table ms_table")))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=70)", fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view ms_mv")))
		run(fixture.Exec("RESET ROLE", 0))
		materializedStorageBaseline(run)
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		noticeRun := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, nc, recorder, step) }
		noticeRun(fixture.Exec("ALTER MATERIALIZED VIEW IF EXISTS missing SET (fillfactor=70)", 0))
		wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}
		if len(notices) != 1 || notices[0] != wantNotice {
			t.Fatalf("IF EXISTS notices %+v want %+v", notices, wantNotice)
		}
		notices = nil
		noticeRun(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 10001", 0))
		wantWarning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}
		if len(notices) != 1 || notices[0] != wantWarning {
			t.Fatalf("clamp notices %+v want %+v", notices, wantWarning)
		}
		run(materializedStorageColumns(int64(10000), nil, "x", ""))
		// Syntax errors must be diagnosed during genuine Prepare, including the
		// complete malformed suffix, with character rather than byte positions.
		for _, tc := range []struct {
			q, token string
			end      bool
		}{
			{"ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=70) trailing", "trailing", false},
			{`ALTER MATERIALIZED VIEW "é" SET (fillfactor=70) trailing`, "trailing", false},
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 8; SELECT FROM", "", true},
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default, SET (fillfactor=70)", ",", false},
			{"ALTER MATERIALIZED VIEW IF EXISTS ALL IN TABLESPACE pg_default SET TABLESPACE pg_default", "ALL", false},
		} {
			want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
			want.Position = strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
			if tc.end {
				want.Message = "syntax error at end of input"
				want.Position = "69"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(tc.q, want))
			statement, err := c.PrepareContext(p.Ctx, tc.q)
			if statement != nil {
				statement.Close()
				t.Fatal("malformed utility returned statement")
			}
			fixture.AssertDiagnostic(t, err, &want)
		}
		run(materializedStorageRows())
		run(materializedStorageOptions(nil))
		run(materializedStorageColumns(int64(10000), nil, "x", ""))
	})
}
