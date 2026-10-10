package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres"
)

func queryTableCreateXIDRows(t *testing.T, postgres bool, query string, expected [][]any) referenceStep {
	t.Helper()
	step := referenceSelect(query, []string{"x"}, []string{"XID"}, expected)
	step.bag = true
	step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		nullable, known := columns[0].Nullable()
		if !postgres && (!known || !nullable) {
			t.Fatal("stored XID lost fresh nullable shape", nullable, known)
		}
	}
	step.checkRaw = func(t *testing.T, column, row int, value any) bool {
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
	p := newReferenceScenario(t)
	var dump []byte
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			// Filtering the actual catalog is independent of other PG sessions' entries.
			body := "SELECT transaction AS x FROM pg_catalog.pg_prepared_xacts WHERE false"
			run(referencePrepared(referenceExec("CREATE TABLE qc_xid AS "+body, 0)))
			run(referenceSelect("SELECT transaction AS x INTO qc_xid_into FROM pg_catalog.pg_prepared_xacts WHERE false", []string{}, []string{}, nil))
			run(referenceExec("CREATE TABLE qc_xid_empty AS "+body+" WITH NO DATA", 0))
			run(referenceExec("INSERT INTO qc_xid VALUES('0'),('4294967295'),(NULL)", 3))
			held, err := conn.PrepareContext(p.ctx, "INSERT INTO qc_xid VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: "INSERT INTO qc_xid VALUES($1)"}, b.recorder, referenceExec("INSERT INTO qc_xid VALUES($1)", 1, "17"))
			check := func() {
				run(queryTableCreateXIDRows(t, b.pg, "SELECT x FROM qc_xid", [][]any{{"0"}, {"4294967295"}, {nil}, {"17"}}))
				run(referencePrepared(queryTableCreateXIDRows(t, b.pg, "SELECT x FROM qc_xid_into", nil)))
				run(queryTableCreateXIDRows(t, b.pg, "SELECT x FROM qc_xid_empty", nil))
			}
			check()
			if !b.pg {
				// Explicit native control phase, not an invented PostgreSQL host API.
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &saved); err != nil {
					t.Fatal(err)
				}
				dump = bytes.Clone(saved.Bytes())
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				queryTableCreateAbsent(t, run, false, "qc_xid")
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				check()
			} else {
				if dump == nil {
					t.Fatal("actual native-emitted XID dump required")
				}
				run(referenceExec("DROP TABLE qc_xid,qc_xid_into,qc_xid_empty", 0))
				if _, err := conn.ExecContext(p.ctx, string(dump)); err != nil {
					t.Fatal("PG rejected actual native XID dump", err)
				}
				check()
			}
		})
	}
}

func TestReferenceQueryTableCreateStructuralOutput(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, b.pg, &notices)
			run := func(step referenceStep) { referenceRunStep(t, p.ctx, conn, record, step) }
			run(referenceExec("CREATE TABLE qc_tree_source(i INT DEFAULT 7)", 0))
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
				if b.pg {
					count := int64(1)
					if tc.empty {
						count = 0
					}
					run(referencePrepared(referenceExec(tc.query, count)))
					run(referenceSelect("SELECT tree FROM "+tc.name+" WHERE false", []string{"tree"}, []string{"PG_NODE_TREE"}, nil))
					var expected [][]any
					if !tc.empty {
						expected = [][]any{{true, "7"}}
					}
					run(referenceSelect("SELECT tree IS NOT NULL AS present,pg_get_expr(tree,'qc_tree_source'::regclass) AS expression FROM "+tc.name, []string{"present", "expression"}, []string{"BOOL", "TEXT"}, expected))
				} else {
					// Descriptor-time Execute boundary, not syntax/Prepare or runtime folding.
					run(referencePrepared(referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: "query table output type pg_node_tree is unsupported"})))
					queryTableCreateAbsent(t, run, false, tc.name)
				}
			}
			run(referenceExec("CREATE TABLE qc_tree_skip(keep INT)", 0))
			run(referenceExec("INSERT INTO qc_tree_skip VALUES(9)", 1))
			run(referenceExec("CREATE SEQUENCE qc_tree_draw START 10", 0))
			for _, suffix := range []string{"", " WITH NO DATA"} {
				before := len(notices)
				skip := "CREATE TABLE IF NOT EXISTS qc_tree_skip AS SELECT adbin AS tree,nextval('qc_tree_draw') AS n FROM pg_catalog.pg_attrdef WHERE " + sourceFilter + suffix
				run(referencePrepared(referenceExec(skip, 0)))
				want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_tree_skip" already exists, skipping`}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatal("structural skip notice", notices, want)
				}
				run(referenceSelect("SELECT keep FROM qc_tree_skip", []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}}))
			}
			run(referenceSelect("SELECT nextval('qc_tree_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
			run(referenceSelect("SELECT i FROM qc_tree_source", []string{"i"}, []string{"INT4"}, nil))
			run(referenceSelect("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
