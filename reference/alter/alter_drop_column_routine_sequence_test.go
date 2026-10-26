package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// The routine is reached only through column -> owned sequence -> body. Local
// defaults are automatic payload, not an extra NORMAL dependent. A surviving
// same-table owner and an equal stable attribute on another table must not match.
func TestReferenceAlterDropColumnRoutineOwnedSequence(t *testing.T) {
	for _, identity := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("identity=%t/held=%t", identity, held), func(t *testing.T) {
				dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					definition := "CREATE TABLE dcr_seq_t(unused integer,a bigint,keep integer)"
					sequence := "dcr_seq_owned"
					if identity {
						definition = "CREATE TABLE dcr_seq_t(unused integer,a bigint GENERATED ALWAYS AS IDENTITY,keep integer)"
						sequence = "dcr_seq_t_a_seq"
					}
					dcrExec(t, p, b, c, definition,
						"CREATE TABLE dcr_seq_other(unused integer,a bigint)",
						"CREATE SEQUENCE dcr_seq_keep OWNED BY dcr_seq_t.keep",
						"CREATE SEQUENCE dcr_seq_other_owned OWNED BY dcr_seq_other.a")
					if !identity {
						dcrExec(t, p, b, c, "CREATE SEQUENCE "+sequence+" OWNED BY dcr_seq_t.a",
							"ALTER TABLE dcr_seq_t ALTER a SET DEFAULT nextval('"+sequence+"')")
					}
					dcrRun(t, p, b, c, fixture.Exec("INSERT INTO dcr_seq_t VALUES(7,DEFAULT,9)", 1))
					dcrExec(t, p, b, c, "ALTER TABLE dcr_seq_t DROP unused",
						"CREATE FUNCTION dcr_seq_use() RETURNS bigint LANGUAGE SQL BEGIN ATOMIC SELECT last_value FROM "+sequence+"; END",
						"CREATE VIEW dcr_seq_view AS SELECT dcr_seq_use() AS v")
					dcrNotices(t, notices)
					idsSQL := "SELECT oid::bigint,relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname LIKE 'dcr_seq_%' ORDER BY oid"
					edgesSQL := "SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE objid IN (SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace AND relname LIKE 'dcr_seq_%') OR objid='dcr_seq_use()'::regprocedure OR refobjid IN (SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace AND relname LIKE 'dcr_seq_%') ORDER BY 1,2,3,4,5,6,7"
					idsBefore := dcrRows(t, p, c, idsSQL)
					edgesBefore := dcrRows(t, p, c, edgesSQL)
					sequenceID := dcrRows(t, p, c, "SELECT '"+sequence+"'::regclass::oid::bigint")[0][0]
					ownerID := dcrRows(t, p, c, "SELECT 'dcr_seq_t'::regclass::oid::bigint")[0][0]
					routineID := dcrRows(t, p, c, "SELECT 'dcr_seq_use()'::regprocedure::oid::bigint")[0][0]
					viewID := dcrRows(t, p, c, "SELECT 'dcr_seq_view'::regclass::oid::bigint")[0][0]
					kind := "a"
					if identity {
						kind = "i"
					}
					dcrRun(t, p, b, c, fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid=1259 AND objid='"+sequence+"'::regclass AND refclassid=1259 AND refobjid='dcr_seq_t'::regclass ORDER BY 1,2,3,4,5,6,7", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, [][]any{{int64(1259), sequenceID, int64(0), int64(1259), ownerID, int64(2), kind}}))
					incidentSQL := fmt.Sprintf("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE (classid=1259 AND objid IN (%v,%v)) OR (refclassid=1259 AND refobjid IN (%v,%v)) OR (classid=1255 AND objid=%v) OR (refclassid=1255 AND refobjid=%v) OR (refclassid=1259 AND refobjid=%v AND refobjsubid=2) ORDER BY 1,2,3,4,5,6,7", sequenceID, viewID, sequenceID, viewID, routineID, routineID, ownerID)
					unchanged := func() {
						t.Helper()
						if got := dcrRows(t, p, c, idsSQL); !reflect.DeepEqual(got, idsBefore) {
							t.Errorf("RESTRICT/rollback changed identities: %v; want %v", got, idsBefore)
						}
						if got := dcrRows(t, p, c, edgesSQL); !reflect.DeepEqual(got, edgesBefore) {
							t.Errorf("RESTRICT/rollback changed dependency bag: %v; want %v", got, edgesBefore)
						}
						dcrRun(t, p, b, c, fixture.Select("SELECT * FROM dcr_seq_t", []string{"a", "keep"}, []string{"INT8", "INT4"}, [][]any{{int64(1), int64(9)}}))
						dcrRun(t, p, b, c, fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_seq_t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"a", int64(2)}, {"keep", int64(3)}}))
						dcrRun(t, p, b, c, fixture.Select("SELECT * FROM dcr_seq_view", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
					want := dcrRestrict("dcr_seq_t", "a", "function dcr_seq_use() depends on sequence "+sequence+"\nview dcr_seq_view depends on function dcr_seq_use()")
					dcrError(t, p, b, c, "ALTER TABLE dcr_seq_t DROP a RESTRICT", want, held, 2)
					dcrNotices(t, notices)
					unchanged()
					for _, finish := range []string{"ROLLBACK", "COMMIT"} {
						dcrExec(t, p, b, c, "BEGIN")
						step := fixture.Exec("ALTER TABLE dcr_seq_t DROP a CASCADE", 0)
						step.Prepared = held
						dcrRun(t, p, b, c, step)
						dcrNotices(t, notices, "function dcr_seq_use()", "view dcr_seq_view")
						dcrRun(t, p, b, c, fixture.Select("SELECT * FROM dcr_seq_t", []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}}))
						dcrRun(t, p, b, c, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('"+sequence+"','dcr_seq_view') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
						dcrRun(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='dcr_seq_use'", []string{"proname"}, []string{"NAME"}, nil))
						dcrRun(t, p, b, c, fixture.Select(incidentSQL, []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, nil))
						dcrExec(t, p, b, c, finish)
						if finish == "ROLLBACK" {
							unchanged()
						}
					}
					dcrRun(t, p, b, c, fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_seq_t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"keep", int64(3)}}))
					for _, survivor := range []string{"dcr_seq_keep", "dcr_seq_other_owned"} {
						dcrRun(t, p, b, c, fixture.Select("SELECT nextval('"+survivor+"') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
					dcrRun(t, p, b, c, fixture.Exec("INSERT INTO dcr_seq_t VALUES(11)", 1))
					dcrRun(t, p, b, c, fixture.Select("SELECT keep FROM dcr_seq_t ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(11)}}))
				})
			})
		}
	}
}
