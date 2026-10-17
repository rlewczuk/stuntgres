package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func queryTableCreateXIDRows(t *testing.T, postgres bool, query string, expected [][]any) fixture.Step {
	t.Helper()
	step := fixture.Select(query, []string{"x"}, []string{"XID"}, expected)
	step.Bag = true
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		nullable, known := columns[0].Nullable()
		if !postgres && (!known || !nullable) {
			t.Fatal("stored XID lost fresh nullable shape", nullable, known)
		}
	}
	step.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		raw, ok := value.([]byte)
		if !ok {
			t.Fatalf("XID raw carrier %T (%v), want decimal bytes", value, value)
		}
		text := string(raw)
		number, err := strconv.ParseUint(text, 10, 32)
		if err != nil || strconv.FormatUint(number, 10) != text {
			t.Fatal("noncanonical unsigned XID", text, err)
		}
		return true
	}
	return step
}

func TestReferenceQueryTableCreateXID(t *testing.T) {
	p := fixture.NewScenario(t)
	var dump []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			// Filtering the actual catalog is independent of other PG sessions' entries.
			body := "SELECT transaction AS x FROM pg_catalog.pg_prepared_xacts WHERE false"
			run(fixture.Prepared(fixture.Exec("CREATE TABLE qc_xid AS "+body, 0)))
			run(fixture.Select("SELECT transaction AS x INTO qc_xid_into FROM pg_catalog.pg_prepared_xacts WHERE false", []string{}, []string{}, nil))
			run(fixture.Exec("CREATE TABLE qc_xid_empty AS "+body+" WITH NO DATA", 0))
			run(fixture.Exec("INSERT INTO qc_xid VALUES('0'),('4294967295'),(NULL)", 3))
			held, err := conn.PrepareContext(p.Ctx, "INSERT INTO qc_xid VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: "INSERT INTO qc_xid VALUES($1)"}, b.Recorder, fixture.Exec("INSERT INTO qc_xid VALUES($1)", 1, "17"))
			check := func() {
				run(queryTableCreateXIDRows(t, b.PG, "SELECT x FROM qc_xid", [][]any{{"0"}, {"4294967295"}, {nil}, {"17"}}))
				run(fixture.Prepared(queryTableCreateXIDRows(t, b.PG, "SELECT x FROM qc_xid_into", nil)))
				run(queryTableCreateXIDRows(t, b.PG, "SELECT x FROM qc_xid_empty", nil))
			}
			check()
			if !b.PG {
				// Explicit native control phase, not an invented PostgreSQL host API.
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				dump = bytes.Clone(saved.Bytes())
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.QueryTableCreateAbsent(t, run, "qc_xid")
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				check()
			} else {
				if dump == nil {
					t.Fatal("actual native-emitted XID dump required")
				}
				run(fixture.Exec("DROP TABLE qc_xid,qc_xid_into,qc_xid_empty", 0))
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG rejected actual native XID dump", err)
				}
				check()
			}
		})
	}
}

func TestReferenceQueryTableCreateStructuralOutput(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, step) }
			run(fixture.Exec("CREATE TABLE qc_tree_source(i INT DEFAULT 7)", 0))
			sourceFilter := "adrelid=(SELECT oid FROM pg_catalog.pg_class WHERE relname='qc_tree_source')"
			body := "SELECT adbin AS tree FROM pg_catalog.pg_attrdef WHERE " + sourceFilter
			for _, tc := range []struct {
				name, query string
				empty       bool
			}{
				{"qc_tree_data", "CREATE TABLE qc_tree_data AS " + body, false},
				{"qc_tree_empty", "CREATE TABLE qc_tree_empty AS " + body + " WITH NO DATA", true},
				{"qc_tree_into", "SELECT adbin AS tree INTO qc_tree_into FROM pg_catalog.pg_attrdef WHERE " + sourceFilter, false},
			} {
				if b.PG {
					count := int64(1)
					if tc.empty {
						count = 0
					}
					run(fixture.Prepared(fixture.Exec(tc.query, count)))
					run(fixture.Select("SELECT tree FROM "+tc.name+" WHERE false", []string{"tree"}, []string{"PG_NODE_TREE"}, nil))
					var expected [][]any
					if !tc.empty {
						expected = [][]any{{true, "7"}}
					}
					run(fixture.Select("SELECT tree IS NOT NULL AS present,pg_get_expr(tree,'qc_tree_source'::regclass) AS expression FROM "+tc.name, []string{"present", "expression"}, []string{"BOOL", "TEXT"}, expected))
				} else {
					// Descriptor-time Execute boundary, not syntax/Prepare or runtime folding.
					run(fixture.Prepared(fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "query table output type pg_node_tree is unsupported"})))
					fixture.QueryTableCreateAbsent(t, run, tc.name)
				}
			}
			run(fixture.Exec("CREATE TABLE qc_tree_skip(keep INT)", 0))
			run(fixture.Exec("INSERT INTO qc_tree_skip VALUES(9)", 1))
			run(fixture.Exec("CREATE SEQUENCE qc_tree_draw START 10", 0))
			for _, suffix := range []string{"", " WITH NO DATA"} {
				before := len(notices)
				skip := "CREATE TABLE IF NOT EXISTS qc_tree_skip AS SELECT adbin AS tree,nextval('qc_tree_draw') AS n FROM pg_catalog.pg_attrdef WHERE " + sourceFilter + suffix
				run(fixture.Prepared(fixture.Exec(skip, 0)))
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_tree_skip" already exists, skipping`}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatal("structural skip notice", notices, want)
				}
				run(fixture.Select("SELECT keep FROM qc_tree_skip", []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}}))
			}
			run(fixture.Select("SELECT nextval('qc_tree_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
			run(fixture.Select("SELECT i FROM qc_tree_source", []string{"i"}, []string{"INT4"}, nil))
			run(fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
