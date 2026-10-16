package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceACLValuesControls(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		owner := aclValuesOwner(b.PG)
		run(fixture.Exec("CREATE DOMAIN acl_values_domain AS aclitem[]", 0))
		run(fixture.Exec("CREATE TYPE acl_values_carrier AS (a aclitem[])", 0))
		run(fixture.Exec("CREATE DOMAIN acl_values_carrier_domain AS acl_values_carrier", 0))
		run(fixture.Exec("CREATE TYPE acl_values_outer AS (inner_value acl_values_carrier_domain,tag INT)", 0))
		run(fixture.Exec("CREATE DOMAIN acl_values_outer_domain AS acl_values_outer", 0))
		run(fixture.Exec("CREATE TABLE acl_values_recursive(id INT,n acl_values_outer_domain)", 0))
		run(fixture.Exec("INSERT INTO acl_values_recursive VALUES (9,ROW(ROW(acldefault('c',0))::acl_values_carrier::acl_values_carrier_domain,7)::acl_values_outer::acl_values_outer_domain)", 1))
		run(fixture.Exec("CREATE TABLE acl_values_control(id INT,a aclitem,aa aclitem[],d acl_values_domain,n acl_values_carrier)", 0))
		run(fixture.Exec("INSERT INTO acl_values_control VALUES (1,makeaclitem(10,10,'SELECT,UPDATE',true),acldefault('r',10),acldefault('c',0),ROW(acldefault('c',0))::acl_values_carrier)", 1))
		run(fixture.Exec("CREATE VIEW acl_values_recipe AS SELECT makeaclitem(10,10,'SELECT',false) AS a,acldefault('c',0) AS aa", 0))
		run(fixture.Exec("CREATE TABLE acl_values_default(a aclitem DEFAULT makeaclitem(10,10,'UPDATE',true),aa aclitem[] DEFAULT acldefault('c',0))", 0))
		run(fixture.Exec("INSERT INTO acl_values_default DEFAULT VALUES", 1))
		check := fixture.Select("SELECT id,a,aa FROM acl_values_control", []string{"id", "a", "aa"}, []string{"INT4", "ACLITEM", "_ACLITEM"}, [][]any{{int64(1), owner + "=r*w*/" + owner, "{" + owner + "=arwdDxtm/" + owner + "}"}})
		recursiveCheck := fixture.Select("SELECT id,(n).tag AS tag,((n).inner_value).a IS NULL AS missing FROM acl_values_recursive", []string{"id", "tag", "missing"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(9), int64(7), false}})
		shapeChecks := []fixture.Step{
			aclValuesExplode("SELECT x.* FROM acl_values_control c,LATERAL aclexplode(c.d::aclitem[]) x", nil),
			aclValuesExplode("SELECT x.* FROM acl_values_control c,LATERAL aclexplode((c.n).a) x", nil),
			aclValuesExplode("SELECT x.* FROM acl_values_recipe v,LATERAL aclexplode(v.aa) x", nil),
			aclValuesExplode("SELECT x.* FROM acl_values_default d,LATERAL aclexplode(d.aa) x", nil),
			aclValuesExplode("SELECT x.* FROM acl_values_recursive r,LATERAL aclexplode(((r.n).inner_value).a) x", nil),
		}
		run(check)
		run(fixture.Prepared(check))
		run(recursiveCheck)
		run(fixture.Prepared(recursiveCheck))
		run(aclitemSelect("SELECT a FROM acl_values_recipe", [][]any{{owner + "=r/" + owner}}))
		run(aclitemSelect("SELECT a FROM acl_values_default", [][]any{{owner + "=w*/" + owner}}))
		for _, s := range shapeChecks {
			run(s)
			run(fixture.Prepared(s))
		}
		// Both backends first establish that the negative native-control datum
		// is valid SQL and retains a nonnull rank-one empty nested ACL.
		run(fixture.Exec("CREATE TABLE acl_values_carrier_array(id INT,a acl_values_carrier[])", 0))
		run(fixture.Exec("INSERT INTO acl_values_carrier_array VALUES (11,ARRAY[ROW(acldefault('c',0))::acl_values_carrier])", 1))
		arrayCheck := fixture.Select("SELECT id,(a[1]).a IS NULL AS missing FROM acl_values_carrier_array", []string{"id", "missing"}, []string{"INT4", "BOOL"}, [][]any{{int64(11), false}})
		arrayShape := aclValuesExplode("SELECT x.* FROM acl_values_carrier_array r,LATERAL aclexplode((r.a[1]).a) x", nil)
		for _, s := range []fixture.Step{arrayCheck, fixture.Prepared(arrayCheck), arrayShape, fixture.Prepared(arrayShape)} {
			run(s)
		}
		if !b.PG {
			held, err := c.QueryContext(p.Ctx, arrayCheck.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			writer := &aclValuesWriter{}
			err = stuntgres.Dump(p.Ctx, p.Native, writer)
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) || writer.calls != 0 || writer.Len() != 0 {
				t.Fatalf("array of empty-ACL composite carriers: err=%v writes=%d bytes=%d", err, writer.calls, writer.Len())
			}
			run(arrayCheck)
			run(arrayShape)
			run(check)
			run(recursiveCheck)
			for _, s := range shapeChecks {
				run(s)
			}
			fixture.ArrayReferenceConsumeHeld(t, held, arrayCheck.Rows, arrayCheck.Types)
		}
		run(fixture.Exec("DROP TABLE acl_values_carrier_array", 0))
		if b.PG {
			return // PostgreSQL has no native control API.
		}
		fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
		run(recursiveCheck)
		for _, s := range shapeChecks {
			run(s)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE acl_values_spacer", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, recursiveCheck)
		for _, s := range shapeChecks {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
		}
		// Exercise genuine emitted SQL against the real fixture, not a replacement dump.
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("emitted ACL replay", err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, recursiveCheck)
			for _, s := range shapeChecks {
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			}
			for _, q := range []string{"DROP VIEW acl_values_recipe", "DROP TABLE acl_values_control", "DROP TABLE acl_values_default", "DROP TABLE acl_values_recursive", "DROP DOMAIN acl_values_outer_domain", "DROP TYPE acl_values_outer", "DROP DOMAIN acl_values_carrier_domain", "DROP TYPE acl_values_carrier", "DROP DOMAIN acl_values_domain"} {
				if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
		}
		stream := "CREATE TABLE acl_values_staged(a aclitem); INSERT INTO acl_values_staged VALUES (makeaclitem(10,10,'SELECT',false)); INSERT INTO acl_values_staged VALUES (makeaclitem(10,10,'bad',false));"
		d := aclitemDiagnostic("22023", `unrecognized privilege type: "bad"`)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream)), &d)
		run(check)
		run(fixture.Error("SELECT a FROM acl_values_staged", "42P01"))
		for _, id := range []int64{0, 4294967295} {
			run(fixture.Exec("CREATE TABLE acl_values_unresolvable(a aclitem)", 0))
			run(fixture.Exec("INSERT INTO acl_values_unresolvable VALUES (makeaclitem(0,$1::oid,'SELECT',false))", 1, id))
			held, err := c.QueryContext(p.Ctx, check.Query)
			if err != nil {
				t.Fatal(err)
			}
			writer := &aclValuesWriter{}
			err = stuntgres.Dump(p.Ctx, p.Native, writer)
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) || writer.calls != 0 || writer.Len() != 0 {
				t.Fatalf("unresolvable grantor %d: err=%v writes=%d bytes=%d", id, err, writer.calls, writer.Len())
			}
			run(check)
			fixture.ArrayReferenceConsumeHeld(t, held, check.Rows, check.Types)
			run(fixture.Exec("DROP TABLE acl_values_unresolvable", 0))
		}
	})
}

type aclValuesWriter struct {
	bytes.Buffer
	calls int
}

func (w *aclValuesWriter) Write(p []byte) (int, error) {
	w.calls++
	return w.Buffer.Write(p)
}
