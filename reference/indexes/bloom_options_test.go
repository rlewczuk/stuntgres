package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func bloomOptionsMetadata(run func(fixture.Step), index string, options any) {
	bloomBoth(run, fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid=$1::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{options}}, index))
}

func TestReferenceBloomOptions(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := bloomRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION bloom", 0))
			bloomSeed(run, "bloom_options")
			for _, options := range []string{"", "length=1", "length=16", "length=17", "length=4096", "length=1,col1=4095,col32=4095", "col1=1,col32=1", "length=7,col32=4095"} {
				with := ""
				var metadata any
				if options != "" {
					with, metadata = " WITH("+options+")", "{"+options+"}"
				}
				run(fixture.Exec("CREATE INDEX bloom_option_idx ON bloom_options USING bloom(i,s)"+with, 0))
				bloomOptionsMetadata(run, "bloom_option_idx", metadata)
				bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_options WHERE i=7 AND s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}}))
				run(fixture.Exec("DROP INDEX bloom_option_idx", 0))
			}
			// Effective word rounding is private representation; public reloptions
			// deliberately retain 7 rather than exposing an invented 16-bit value.
			run(fixture.Prepared(fixture.Exec("CREATE INDEX bloom_option_idx ON bloom_options USING bloom(i,s) WITH(length=7,col32=4095)", 0)))
			bloomOptionsMetadata(run, "bloom_option_idx", "{length=7,col32=4095}")
			run(fixture.Exec("ALTER INDEX bloom_option_idx SET(length=17,col1=1)", 0))
			bloomOptionsMetadata(run, "bloom_option_idx", "{col32=4095,length=17,col1=1}")
			bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_options WHERE s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}, {nil, "a"}, {int64(8), "a"}}))
			run(fixture.Prepared(fixture.Exec("ALTER INDEX bloom_option_idx RESET(length,col1,col32)", 0)))
			bloomOptionsMetadata(run, "bloom_option_idx", nil)
			bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_options WHERE i=8", [][]any{{int64(8), "b"}, {int64(8), "a"}}))
			keys := strings.TrimSuffix(strings.Repeat("i,", 32), ",")
			run(fixture.Exec("CREATE INDEX bloom_32 ON bloom_options USING bloom("+keys+") WITH(col32=4095)", 0))
			bloomBoth(run, fixture.Select("SELECT indnatts,indnkeyatts FROM pg_index WHERE indexrelid='bloom_32'::regclass", []string{"indnatts", "indnkeyatts"}, []string{"INT2", "INT2"}, [][]any{{int64(32), int64(32)}}))
			allColumns := make([]string, 32)
			for i := range allColumns {
				allColumns[i] = fmt.Sprintf("col%d=1", i+1)
			}
			run(fixture.Exec("CREATE INDEX bloom_all_options ON bloom_options USING bloom(i,s) WITH("+strings.Join(allColumns, ",")+")", 0))
			bloomOptionsMetadata(run, "bloom_all_options", "{"+strings.Join(allColumns, ",")+"}")
		})
	})
	for _, tc := range []struct {
		option, message, detail string
	}{
		{"length=0", `value 0 out of bounds for option "length"`, `Valid values are between "1" and "4096".`},
		{"length=4097", `value 4097 out of bounds for option "length"`, `Valid values are between "1" and "4096".`},
		{"col1=0", `value 0 out of bounds for option "col1"`, `Valid values are between "1" and "4095".`},
		{"col1=4096", `value 4096 out of bounds for option "col1"`, `Valid values are between "1" and "4095".`},
		{"col32=0", `value 0 out of bounds for option "col32"`, `Valid values are between "1" and "4095".`},
		{"col32=4096", `value 4096 out of bounds for option "col32"`, `Valid values are between "1" and "4095".`},
		{"col0=1", `unrecognized parameter "col0"`, ""},
		{"col33=1", `unrecognized parameter "col33"`, ""},
		{"fillfactor=80", `unrecognized parameter "fillfactor"`, ""},
		{"foo=1", `unrecognized parameter "foo"`, ""},
		{"length='bad'", `invalid value for integer option "length": bad`, ""},
		{"length=80,length=16", `parameter "length" specified more than once`, ""},
		{"x.length=16", `unrecognized parameter namespace "x"`, ""},
	} {
		t.Run(tc.option, func(t *testing.T) {
			bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := bloomRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION bloom", 0))
				bloomSeed(run, "bloom_options")
				d := fixture.Diagnostic{Code: "22023", Message: tc.message, Detail: tc.detail}
				bloomError(run, "CREATE INDEX bloom_retry ON bloom_options USING bloom(i,s) WITH("+tc.option+")", d)
				bloomIndexAbsent(run, "bloom_retry")
				// Same name works after failure; no failed descriptor blocks retry.
				run(fixture.Exec("CREATE INDEX bloom_retry ON bloom_options USING bloom(i,s) WITH(length=7,col32=4095)", 0))
				bloomOptionsMetadata(run, "bloom_retry", "{length=7,col32=4095}")
				bloomError(run, "ALTER INDEX bloom_retry SET("+tc.option+")", d)
				bloomOptionsMetadata(run, "bloom_retry", "{length=7,col32=4095}")
				bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_options WHERE i=7", [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}}))
				run(fixture.Exec("ALTER INDEX bloom_retry SET(length=16)", 0))
				bloomOptionsMetadata(run, "bloom_retry", "{col32=4095,length=16}")
			})
		})
	}
}

func TestReferenceBloomDiagnostics(t *testing.T) {
	for n, tc := range []struct {
		definition, code, message, hint string
	}{
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(big)", "42704", `data type bigint has no default operator class for access method "bloom"`, "You must specify an operator class for the index or define a default operator class for the data type."},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(flag)", "42704", `data type boolean has no default operator class for access method "bloom"`, "You must specify an operator class for the index or define a default operator class for the data type."},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(i text_ops)", "42804", `operator class "text_ops" does not accept data type integer`, ""},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(i int8_ops)", "42704", `operator class "int8_ops" does not exist for access method "bloom"`, ""},
		{"CREATE UNIQUE INDEX bloom_retry ON bloom_diag USING bloom(i)", "0A000", `access method "bloom" does not support unique indexes`, ""},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(i) INCLUDE(s)", "0A000", `access method "bloom" does not support included columns`, ""},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(i ASC)", "0A000", `access method "bloom" does not support ASC/DESC options`, ""},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(i DESC)", "0A000", `access method "bloom" does not support ASC/DESC options`, ""},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(i NULLS FIRST)", "0A000", `access method "bloom" does not support NULLS FIRST/LAST options`, ""},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(i NULLS LAST)", "0A000", `access method "bloom" does not support NULLS FIRST/LAST options`, ""},
		{"CREATE INDEX bloom_retry ON bloom_diag USING bloom(" + strings.TrimSuffix(strings.Repeat("missing,", 33), ",") + ")", "54011", "cannot use more than 32 columns in an index", ""},
	} {
		t.Run(fmt.Sprintf("case_%02d", n), func(t *testing.T) {
			bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := bloomRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION bloom", 0))
				run(fixture.Exec("CREATE TABLE bloom_diag(i integer,s text,big bigint,flag boolean)", 0))
				bloomError(run, tc.definition, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint})
				bloomIndexAbsent(run, "bloom_retry")
				run(fixture.Prepared(fixture.Exec("CREATE INDEX bloom_retry ON bloom_diag USING bloom(i,s)", 0)))
				run(fixture.Exec("INSERT INTO bloom_diag VALUES(7,'a',7,true),(NULL,NULL,NULL,NULL)", 2))
				bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_diag WHERE i=7", [][]any{{int64(7), "a"}}))
			})
		})
	}
}
