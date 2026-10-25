package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type loReferenceValuer struct{ value driver.Value }

func (v loReferenceValuer) Value() (driver.Value, error) { return v.value, nil }

func TestReferenceLoAssignmentAndBinds(t *testing.T) {
	loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loInstall(t, p, b, c)
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_assign(id integer NOT NULL,v lo,w lo)", 0))
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_source(i integer,b bigint,o oid,s text)", 0))
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_source VALUES (-1,4294967295,'2147483648','0377')", 1))
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_assign SELECT 1,i,o FROM lo_source", 1))
		loRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO lo_assign SELECT 2,b,s::lo FROM lo_source", 1)))
		loRun(t, p, b, c, fixture.Exec("UPDATE lo_assign SET v='26'::oid WHERE id=1", 1))
		loRows(t, p, b, c, fixture.Select("SELECT id,v,w FROM lo_assign ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "OID", "OID"}, [][]any{{int64(1), int64(26), int64(2147483648)}, {int64(2), int64(4294967295), int64(255)}}))
		// Explicit text casts above are allowed; implicit typed-text assignment
		// must not gain permission just because the target is a domain over oid.
		textAssignment := "INSERT INTO lo_assign SELECT 40,s,NULL FROM lo_source"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(textAssignment, fixture.Diagnostic{
				Code: "42804", Message: `column "v" is of type lo but expression is of type text`,
				Hint:     "You will need to rewrite or cast the expression.",
				Position: strconv.Itoa(strings.Index(textAssignment, ",s,") + 2),
			})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			loRun(t, p, b, c, s)
		}
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO lo_assign VALUES ($1,$2,$2)"
			db := fixture.Executor(c)
			if prepared {
				db = loHeld(t, p, c, q)
			}
			for i, input := range []any{"0", []byte("2147483648"), int64(-1), nil, loReferenceValuer{"26"}} {
				id := 10 + i
				if prepared {
					id += 10
				}
				loRun(t, p, b, db, fixture.Exec(q, 1, id, input))
			}
		}
		owned := []byte("4294967295")
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_assign VALUES (30,$1,$1)", 1, owned))
		owned[0] = '0'
		for _, start := range []int{10, 20} {
			for i, value := range []any{int64(0), int64(2147483648), int64(4294967295), nil, int64(26)} {
				loRows(t, p, b, c, fixture.Select("SELECT v,w FROM lo_assign WHERE id=$1", []string{"v", "w"}, []string{"OID", "OID"}, [][]any{{value, value}}, start+i))
			}
		}
		loRows(t, p, b, c, fixture.Select("SELECT v,w FROM lo_assign WHERE id=30", []string{"v", "w"}, []string{"OID", "OID"}, [][]any{{int64(4294967295), int64(4294967295)}}))
		update := "UPDATE lo_assign SET v=$1 WHERE id=$2"
		heldUpdate := loHeld(t, p, c, update)
		for _, tc := range []struct {
			value any
			id    int
		}{{"7", 10}, {[]byte("2147483648"), 11}, {int64(-1), 12}, {nil, 14}} {
			loRun(t, p, b, heldUpdate, fixture.Exec(update, 1, tc.value, tc.id))
		}
		loRun(t, p, b, c, fixture.Exec(update, 1, "26", 13))
		loRows(t, p, b, c, fixture.Select("SELECT id,v FROM lo_assign WHERE id>=10 AND id<15 ORDER BY id", []string{"id", "v"}, []string{"INT4", "OID"}, [][]any{{int64(10), int64(7)}, {int64(11), int64(2147483648)}, {int64(12), int64(4294967295)}, {int64(13), int64(26)}, {int64(14), nil}}))
		q := "SELECT $1::lo AS v,$1::lo AS repeated FROM lo_source"
		held := loHeld(t, p, c, q)
		for _, tc := range []struct {
			input any
			want  any
			bad   bool
		}{{"7", int64(7), false}, {"bad", nil, true}, {nil, nil, false}, {"4294967295", int64(4294967295), false}, {"0", int64(0), false}} {
			if tc.bad {
				s := loBoundError(q, "bad", "22P02", "1", tc.input)
				s.SelectRows = true
				loRun(t, p, b, held, s)
			} else {
				loRows(t, p, b, held, fixture.Select(q, []string{"v", "repeated"}, []string{"OID", "OID"}, [][]any{{tc.want, tc.want}}, tc.input))
			}
		}
		for _, q := range []string{"INSERT INTO lo_assign VALUES (40,(-1)::bigint,NULL)", "INSERT INTO lo_assign VALUES (40,4294967296::bigint,NULL)"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "OID out of range"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				loRun(t, p, b, c, s)
			}
		}
		q = "INSERT INTO lo_assign VALUES (40,$1,NULL)"
		for _, prepared := range []bool{false, true} {
			db := fixture.Executor(c)
			if prepared {
				db = loHeld(t, p, c, q)
			}
			result, err := db.ExecContext(p.Ctx, q, uint64(math.MaxInt64)+1)
			if result != nil {
				t.Fatal("failed converter exposed result")
			}
			if b.PG {
				d := loInputDiagnostic("9223372036854775808", "22003")
				d.Context = "unnamed portal parameter $1 = '...'"
				fixture.AssertDiagnostic(t, err, &d)
			} else {
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatal("ordinary oversized uint64 converter error", err)
				}
			}
		}
		loRows(t, p, b, c, fixture.Select("SELECT v FROM lo_assign WHERE id=40", []string{"v"}, []string{"OID"}, [][]any{}))
	})
}
