package reference_test

import (
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type aclitemCountingWriter struct{ calls int }

func (w *aclitemCountingWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, errors.New("stale ACL must reject before writer")
}

func TestReferenceACLItemStaleControls(t *testing.T) {
	aclitemScenario(t, []string{"acl_stale_a", "acl_stale_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			role  string
			array bool
		}{{"acl_stale_a", false}, {"acl_stale_b", false}, {"acl_stale_a", true}, {"acl_stale_b", true}} {
			t.Run(tc.role+"_"+fixture.Position(1+boolInt(tc.array)), func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				var id int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", tc.role).Scan(&id); err != nil || id <= 0 {
					t.Fatalf("role identity=%d error=%v", id, err)
				}
				typ, family, input := "aclitem", "ACLITEM", "acl_stale_a=rw*/acl_stale_b"
				stale := strconv.FormatInt(id, 10) + "=rw*/acl_stale_b"
				if tc.role == "acl_stale_b" {
					stale = "acl_stale_a=rw*/" + strconv.FormatInt(id, 10)
				}
				if tc.array {
					typ = "aclitem[]"
					family = "_ACLITEM"
					input = "[-1:0][3:4]={{" + input + ",NULL},{=r/postgres,postgres=/postgres}}"
					stale = "[-1:0][3:4]={{" + stale + ",NULL},{=r/postgres,postgres=/postgres}}"
				}
				run(fixture.Exec("CREATE TABLE public.acl_stale (a "+typ+")", 0))
				defer c.ExecContext(p.Ctx, "DROP TABLE public.acl_stale")
				run(fixture.Exec("INSERT INTO public.acl_stale VALUES ($1::"+typ+")", 1, input))
				run(fixture.Exec("DROP ROLE "+tc.role, 0))
				defer c.ExecContext(p.Ctx, "CREATE ROLE "+tc.role)
				numeric := strconv.FormatInt(id, 10)
				// A live role named after the stale identity must not be adopted.
				run(fixture.Exec("CREATE ROLE "+aclitemSQLName(numeric), 0))
				defer c.ExecContext(p.Ctx, "DROP ROLE "+aclitemSQLName(numeric))
				check := fixture.Select("SELECT a FROM public.acl_stale", []string{"a"}, []string{family}, [][]any{{stale}})
				run(check)
				run(fixture.Prepared(check))
				run(fixture.Select("SELECT a=a AS retained_identity FROM public.acl_stale", []string{"retained_identity"}, []string{"BOOL"}, [][]any{{true}}))
				numericInput := numeric + "=rw*/acl_stale_b"
				if tc.role == "acl_stale_b" {
					numericInput = "acl_stale_a=rw*/" + numeric
				}
				if tc.array {
					numericInput = "[-1:0][3:4]={{" + numericInput + ",NULL},{=r/postgres,postgres=/postgres}}"
				}
				run(fixture.Select("SELECT a=$1::"+typ+" AS adopted_identity FROM public.acl_stale", []string{"adopted_identity"}, []string{"BOOL"}, [][]any{{false}}, numericInput))
				if b.PG {
					return
				}
				writer := &aclitemCountingWriter{}
				err := stuntgres.Dump(p.Ctx, p.Native, writer)
				if err == nil || writer.calls != 0 {
					t.Fatalf("stale Dump error=%v writer calls=%d want rejection/0", err, writer.calls)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatalf("stale Dump should be ordinary serialization error: %v", err)
				}
				run(check)
			})
		}
	})
}
