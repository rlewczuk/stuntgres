package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIdentityMetadata(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		id := identityID(t, p, a)
		typ := "XID8"
		if b.PG {
			typ = ""
		}
		s := fixture.Select("SELECT age($1::xid) AS a,mxid_age(NULL::xid) AS m,pg_current_xact_id() AS c,pg_current_xact_id_if_assigned() AS i,pg_xact_status($2::xid8) AS s", []string{"a", "m", "c", "i", "s"}, []string{"INT4", "INT4", typ, typ, "TEXT"}, [][]any{{int64(0), nil, id, id, "in progress"}}, identityLow(t, id), id)
		s.RawTypes = []string{"INT4", "INT4", "XID8", "XID8", "TEXT"}
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if column != 2 && column != 3 {
				return false
			}
			if value != nil {
				bytes, ok := value.([]byte)
				if !ok || string(bytes) != id {
					t.Fatalf("XID8 raw value %T %v", value, value)
				}
			}
			return true
		}
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range []int{2, 3} {
				want := reflect.TypeFor[[]byte]()
				if b.PG {
					want = reflect.TypeFor[any]()
				}
				if types[i].ScanType() != want {
					t.Fatalf("XID8 ScanType %v want %v", types[i].ScanType(), want)
				}
			}
		}
		identityBoth(t, p, b, a, s)
		for _, q := range []string{
			"SELECT * FROM pg_get_multixact_members(NULL::xid)",
			"SELECT * FROM pg_get_multixact_members(multixid => NULL::xid)",
		} {
			identityBoth(t, p, b, a, fixture.Select(q, []string{"xid", "mode"}, []string{"XID", "TEXT"}, nil))
		}
		identityBoth(t, p, b, a, fixture.Select("SELECT * FROM pg_get_multixact_members(NULL::xid) WITH ORDINALITY AS m(owner,kind,n)", []string{"owner", "kind", "n"}, []string{"XID", "TEXT", "INT8"}, nil))
		identityBoth(t, p, b, a, fixture.Select("SELECT v.id,m.xid,m.mode FROM (VALUES(1,NULL::xid)) AS v(id,mx) LEFT JOIN LATERAL pg_get_multixact_members(v.mx) AS m ON true", []string{"id", "xid", "mode"}, []string{"INT4", "XID", "TEXT"}, [][]any{{int64(1), nil, nil}}))
		identityBoth(t, p, b, a, fixture.Select("SELECT * FROM ROWS FROM(pg_get_multixact_members(NULL::xid),generate_series(1,2)) WITH ORDINALITY AS m(xid,mode,v,n)", []string{"xid", "mode", "v", "n"}, []string{"XID", "TEXT", "INT4", "INT8"}, [][]any{{nil, nil, int64(1), int64(1)}, {nil, nil, int64(2), int64(2)}}))
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW tx_identity_live AS SELECT pg_current_xact_id_if_assigned() AS i", 0)
		held, err := a.PrepareContext(p.Ctx, "SELECT i::text FROM tx_identity_live")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for range 2 {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			empty := fixture.Select("SELECT pg_current_xact_id_if_assigned() AS i", []string{"i"}, []string{typ}, [][]any{{nil}})
			empty.RawTypes = []string{"XID8"}
			identityBoth(t, p, b, a, empty)
			var value sql.NullString
			if err := held.QueryRowContext(p.Ctx).Scan(&value); err != nil || value.Valid {
				t.Fatalf("stored inquiry captured value %v %v", value, err)
			}
			current := identityID(t, p, a)
			if err := held.QueryRowContext(p.Ctx).Scan(&value); err != nil || !value.Valid || value.String != current {
				t.Fatalf("stored inquiry not live %v %v", value, err)
			}
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		}
		if !b.PG {
			held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			identityBoth(t, p, b, a, fixture.Select("SELECT i::text AS i FROM tx_identity_live", []string{"i"}, []string{"TEXT"}, [][]any{{nil}}))
			restored := identityID(t, p, a)
			identityBoth(t, p, b, a, fixture.Select("SELECT i::text AS i FROM tx_identity_live", []string{"i"}, []string{"TEXT"}, [][]any{{restored}}))
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		}
	})
}

func identityError(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, q string, want fixture.Diagnostic, stage string, args ...any) {
	t.Helper()
	want.Severity = "ERROR"
	want.SeverityUnlocalized = "ERROR"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want, args...)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = stage
		}
		fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
	}
}
func TestReferenceTransactionIdentityDiagnostics(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ call, signature string }{
			{"age()", "age()"}, {"age(true)", "age(boolean)"},
			{"age(xid => '1'::xid)", "age(xid => xid)"},
			{"mxid_age()", "mxid_age()"}, {"mxid_age(true)", "mxid_age(boolean)"}, {"mxid_age('1'::xid,'2'::xid)", "mxid_age(xid, xid)"},
			{"pg_current_xact_id(true)", "pg_current_xact_id(boolean)"}, {"pg_current_xact_id_if_assigned(true)", "pg_current_xact_id_if_assigned(boolean)"},
			{"pg_xact_status()", "pg_xact_status()"}, {"pg_xact_status(true)", "pg_xact_status(boolean)"}, {"pg_xact_status('3'::xid8,'4'::xid8)", "pg_xact_status(xid8, xid8)"},
			{"pg_get_multixact_members()", "pg_get_multixact_members()"}, {"pg_get_multixact_members(true)", "pg_get_multixact_members(boolean)"}, {"pg_get_multixact_members('1'::xid,'2'::xid)", "pg_get_multixact_members(xid, xid)"},
			{"pg_get_multixact_members(wrong => '1'::xid)", "pg_get_multixact_members(wrong => xid)"},
			{"mxid_age(multixid => '1'::xid)", "mxid_age(multixid => xid)"},
			{"pg_xact_status(xid => '3'::xid8)", "pg_xact_status(xid => xid8)"},
			{"pg_catalog.mxid_age(true)", "pg_catalog.mxid_age(boolean)"}, {"\"PG_CURRENT_XACT_ID\"()", "PG_CURRENT_XACT_ID()"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				identityError(t, p, b, a, "SELECT "+tc.call, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: "8"}, "prepare")
			})
		}
		identityError(t, p, b, a, "/*é*/ SELECT mxid_age(true)", fixture.Diagnostic{Code: "42883", Message: "function mxid_age(boolean) does not exist", Hint: hint, Position: "14"}, "prepare")
		identityError(t, p, b, a, "SELECT tx_identity_missing.mxid_age('1'::xid)", fixture.Diagnostic{Code: "3F000", Message: `schema "tx_identity_missing" does not exist`, Position: "8"}, "prepare")
		identityError(t, p, b, a, "SELECT mxid_age(missing)", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "17"}, "prepare")
		identityError(t, p, b, a, "SELECT mxid_age(DISTINCT '1'::xid)", fixture.Diagnostic{Code: "42809", Message: "DISTINCT specified, but mxid_age is not an aggregate function", Position: "8"}, "prepare")
		identityError(t, p, b, a, "SELECT mxid_age('1'::xid) FILTER (WHERE true)", fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but mxid_age is not an aggregate function", Position: "8"}, "prepare")
		for _, argument := range []string{"NULL", "'infinity'", "$1"} {
			var args []any
			if argument == "$1" {
				args = []any{nil}
			}
			identityError(t, p, b, a, "SELECT age("+argument+")", fixture.Diagnostic{Code: "42725", Message: "function age(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"}, "prepare", args...)
		}
		identityError(t, p, b, a, "SELECT pg_xact_status('18446744073709551615'::xid8)", fixture.Diagnostic{Code: "22023", Message: "transaction ID 18446744073709551615 is in the future"}, "execute")
		identityError(t, p, b, a, "SELECT * FROM pg_get_multixact_members('0'::xid)", fixture.Diagnostic{Code: "22023", Message: "invalid MultiXactId: 0"}, "execute")
		next := identityNextMulti(t, p, observer)
		n := strconv.FormatUint(uint64(next), 10)
		identityError(t, p, b, a, "SELECT * FROM pg_get_multixact_members($1::xid)", fixture.Diagnostic{Code: "XX000", Message: "MultiXactId " + n + " has not been created yet -- apparent wraparound"}, "execute", n)
		identityBoth(t, p, b, a, fixture.Select("SELECT age(NULL::xid) AS a,mxid_age(NULL::xid) AS m,pg_xact_status(NULL::xid8) AS s", []string{"a", "m", "s"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{nil, nil, nil}}))
	})
}
