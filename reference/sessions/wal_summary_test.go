package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var walNames = []string{"pg_available_wal_summaries", "pg_wal_summary_contents", "pg_get_wal_summarizer_state"}
var walColumns = [][]string{{"tli", "start_lsn", "end_lsn"}, {"relfilenode", "reltablespace", "reldatabase", "relforknumber", "relblocknumber", "is_limit_block"}, {"summarized_tli", "summarized_lsn", "pending_lsn", "summarizer_pid"}}
var walTypes = [][]string{{"INT8", "PG_LSN", "PG_LSN"}, {"OID", "OID", "OID", "INT2", "INT8", "BOOL"}, {"INT8", "PG_LSN", "PG_LSN", "INT4"}}
var walCalls = []string{"pg_available_wal_summaries()", "pg_wal_summary_contents(1,'0/0','0/1')", "pg_get_wal_summarizer_state()"}

func walMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(names) {
			t.Fatal(columns, err)
		}
		for i, c := range columns {
			scan := reflect.TypeFor[int64]()
			switch types[i] {
			case "PG_LSN":
				scan = reflect.TypeFor[string]()
				if pg {
					scan = reflect.TypeFor[any]()
				}
			case "OID":
				if pg {
					scan = reflect.TypeFor[any]()
				}
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "RECORD":
				scan = reflect.TypeFor[string]()
				if pg {
					scan = reflect.TypeFor[any]()
				}
			case "INT2":
				if pg {
					scan = reflect.TypeFor[int16]()
				}
			case "INT4":
				if pg {
					scan = reflect.TypeFor[int32]()
				}
			}
			if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan {
				t.Fatalf("column %d: %s/%s/%v want %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i], types[i], scan)
			}
			if nullable, known := c.Nullable(); nullable || known {
				t.Fatal("computed nullability", nullable, known)
			}
			if _, ok := c.Length(); ok {
				t.Fatal("length claim")
			}
			if _, _, ok := c.DecimalSize(); ok {
				t.Fatal("precision claim")
			}
		}
	}
}
func walEmpty(query string, index int, pg bool) fixture.Step {
	s := fixture.Select(query, walColumns[index], walTypes[index], nil)
	s.CheckMetadata = walMetadata(pg, s.Columns, s.Types)
	return s
}
func walFailure(query, code, message string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}
func walContentsDemand(query string, pg bool, tli int64, start, end uint64) fixture.Step {
	if !pg {
		return walFailure(query, "0A000", walNames[1]+" physical WAL summary file access is unsupported in embedded mode")
	}
	file := fmt.Sprintf("%08X%08X%08X%08X%08X.summary", tli, start>>32, start&0xffffffff, end>>32, end&0xffffffff)
	return walFailure(query, "58P01", `could not open file "pg_wal/summaries/`+file+`": No such file or directory`)
}
func walPrecondition(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW summarize_wal", []string{"summarize_wal"}, []string{"TEXT"}, [][]any{{"off"}}))
	}
}
func walDemands(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	walPrecondition(t, p, b, c)
	for i, call := range walCalls {
		query := "SELECT * FROM " + call
		s := walFailure(query, "0A000", walNames[i]+" physical WAL summary file listing is unsupported in embedded mode")
		if i == 0 && b.PG {
			s = walEmpty(query, 0, b.PG)
		}
		if i == 1 {
			s = walContentsDemand(query, b.PG, 1, 0, 1)
		}
		if i == 2 {
			s = walFailure(query, "0A000", walNames[i]+" physical WAL summarizer state is unsupported in embedded mode")
			if b.PG {
				s = fixture.Select(query, walColumns[i], walTypes[i], [][]any{{int64(0), "0/0", "0/0", nil}})
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		recoveryHeld(t, p, b, c, s)
	}
	query := "SELECT pg_get_wal_summarizer_state()"
	s := walFailure(query, "0A000", walNames[2]+" physical WAL summarizer state is unsupported in embedded mode")
	if b.PG {
		s = fixture.Select(query, []string{walNames[2]}, []string{"RECORD"}, [][]any{{"(0,0/0,0/0,)"}})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}
func walMetadataCases(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for i, call := range walCalls {
		for _, mode := range []string{"plain", "qualified", "uppercase", "alias", "ordinality", "target"} {
			query := "SELECT * FROM " + call + " LIMIT 0"
			names := walColumns[i]
			types := walTypes[i]
			switch mode {
			case "qualified":
				query = "SELECT * FROM pg_catalog." + call + " LIMIT 0"
			case "uppercase":
				query = "SELECT * FROM PG_CATALOG." + strings.ToUpper(call) + " LIMIT 0"
			case "alias":
				query = "SELECT * FROM " + call + " AS w(first) LIMIT 0"
				names = append([]string(nil), names...)
				names[0] = "first"
			case "ordinality":
				query = "SELECT * FROM " + call + " WITH ORDINALITY LIMIT 0"
				names = append(append([]string(nil), names...), "ordinality")
				types = append(append([]string(nil), types...), "INT8")
			case "target":
				query = "SELECT " + call + " LIMIT 0"
				names = []string{walNames[i]}
				types = []string{"RECORD"}
			}
			s := fixture.Select(query, names, types, nil)
			s.CheckMetadata = walMetadata(b.PG, names, types)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			recoveryHeld(t, p, b, c, s)
		}
	}
}
func TestReferenceWALSummaryMetadata(t *testing.T) { recoveryScenario(t, walMetadataCases) }
func TestReferenceWALSummaryFeatureSmoke(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		walMetadataCases(t, p, b, c)
		walDemands(t, p, b, c)
		for _, call := range []string{"pg_wal_summary_contents(NULL,'0/0','0/1')", "pg_wal_summary_contents(end_lsn=>'0/1',tli=>NULL::bigint,start_lsn=>'0/0')"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, walEmpty("SELECT * FROM "+call, 1, b.PG))
		}
		for _, tc := range []struct {
			tli        int64
			start, end uint64
			a, z       string
		}{{2147483647, 0, 0, "0/0", "0/0"}, {1, 2, 1, "0/2", "0/1"}} {
			q := fmt.Sprintf("SELECT * FROM pg_wal_summary_contents(%d,'%s','%s')", tc.tli, tc.a, tc.z)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, walContentsDemand(q, b.PG, tc.tli, tc.start, tc.end))
		}
		walHeldArguments(t, p, b, c)
	})
}
func TestReferenceWALSummaryCatalog(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE oid IN (6321,6322,6323) ORDER BY oid`
		names := []string{"oid", "proname", "namespace", "owner", "language", "kind", "result", "proretset", "proisstrict", "volatility", "parallel", "procost", "prorows", "pronargdefaults", "variadic", "prosecdef", "proleakproof", "prosrc", "inputs", "alltypes", "modes", "names", "acl"}
		types := []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT2", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
		vectors := [][4]string{{"", "{20,3220,3220}", "{o,o,o}", "{tli,start_lsn,end_lsn}"}, {"20 3220 3220", "{20,3220,3220,26,26,26,21,20,16}", "{i,i,i,o,o,o,o,o,o}", "{tli,start_lsn,end_lsn,relfilenode,reltablespace,reldatabase,relforknumber,relblocknumber,is_limit_block}"}, {"", "{20,3220,3220,23}", "{o,o,o,o}", "{summarized_tli,summarized_lsn,pending_lsn,summarizer_pid}"}}
		var rows [][]any
		for i, name := range walNames {
			estimate := float64(100)
			if i == 2 {
				estimate = 0
			}
			v := vectors[i]
			rows = append(rows, []any{int64(6321 + i), name, int64(11), int64(10), int64(12), "f", int64(2249), i != 2, true, "v", "s", float64(1), estimate, int64(0), int64(0), false, false, name, v[0], v[1], v[2], v[3], nil})
		}
		s := fixture.Select(q, names, types, rows)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 'pg_available_wal_summaries()'::regprocedure::text AS a,'pg_wal_summary_contents(bigint,pg_lsn,pg_lsn)'::regprocedure::text AS b,'pg_get_wal_summarizer_state()'::regprocedure::text AS c", []string{"a", "b", "c"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"pg_available_wal_summaries()", "pg_wal_summary_contents(bigint,pg_lsn,pg_lsn)", "pg_get_wal_summarizer_state()"}}))
	})
}
