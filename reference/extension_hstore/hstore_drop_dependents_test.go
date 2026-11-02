package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreDropRestrict(detail string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop extension hstore because other objects depend on it")
	want.Detail, want.Hint = detail, "Use DROP ... CASCADE to drop the dependent objects too."
	return want
}

func hstoreDropCascaded(t *testing.T, notices *[]fixture.Diagnostic, objects ...string) {
	t.Helper()
	var want []fixture.Diagnostic
	if len(objects) != 0 {
		n := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to " + objects[0]}
		if len(objects) > 1 {
			n.Message = "drop cascades to " + strconv.Itoa(len(objects)) + " other objects"
			for i, object := range objects {
				if i != 0 {
					n.Detail += "\n"
				}
				n.Detail += "drop cascades to " + object
			}
		}
		want = []fixture.Diagnostic{n}
	}
	if len(*notices) != len(want) || len(want) != 0 && !reflect.DeepEqual(*notices, want) {
		t.Fatalf("DROP EXTENSION notices got %#v want %#v", *notices, want)
	}
	*notices = nil
}

// The witness view reaches type hstore through column v and also calls the
// hstore_to_jsonb cast function directly.
func hstoreDropWitnessSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_jsonb_dependent(v hstore)", 0))
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_jsonb_dependent VALUES ('a=>1')", 1))
	hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_jsonb_dependent_view AS SELECT v::jsonb AS j FROM hs_jsonb_dependent", 0))
}

const hstoreDropWitnessDetail = "column v of table hs_jsonb_dependent depends on type hstore\nview hs_jsonb_dependent_view depends on column v of table hs_jsonb_dependent"

// hstoreDropWitnessIntact proves a rejected DROP left identities, definitions,
// dependency records, data and queryability unchanged.
func hstoreDropWitnessIntact(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	hstoreJSONBCatalogCheck(t, p, b, c)
	hstoreSubscriptCatalogCheck(t, p, b, c, true)
	hstoreRun(t, p, b, c, fixture.Select(`SELECT j::text AS j FROM hs_jsonb_dependent_view`, []string{"j"}, []string{"TEXT"}, [][]any{{`{"a": "1"}`}}))
	hstoreRun(t, p, b, c, fixture.Select(`SELECT pg_describe_object(refclassid,refobjid,refobjsubid) AS ref,deptype::text AS d FROM pg_depend WHERE classid='pg_rewrite'::regclass AND objid=(SELECT oid FROM pg_rewrite WHERE ev_class='hs_jsonb_dependent_view'::regclass) ORDER BY 1`, []string{"ref", "d"}, []string{"TEXT", "TEXT"}, [][]any{{"column v of table hs_jsonb_dependent", "n"}, {"function hstore_to_jsonb(hstore)", "n"}, {"view hs_jsonb_dependent_view", "i"}}))
}

func TestReferenceHstoreDropDependentConsumer(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		hstoreDropWitnessSeed(t, p, b, c)
		for _, q := range []string{"DROP EXTENSION hstore", "DROP EXTENSION hstore RESTRICT"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, hstoreDropRestrict(hstoreDropWitnessDetail))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				hstoreDropWitnessIntact(t, p, b, c)
				hstoreDropCascaded(t, &notices)
			}
		}
		// An extension-level dependency retires silently with the extension.
		run(fixture.Exec("CREATE MATERIALIZED VIEW hs_drop_mv AS SELECT 1 AS x", 1))
		run(fixture.Exec("ALTER MATERIALIZED VIEW hs_drop_mv DEPENDS ON EXTENSION hstore", 0))
		run(fixture.Exec("DROP EXTENSION hstore CASCADE", 0))
		hstoreDropCascaded(t, &notices, "column v of table hs_jsonb_dependent", "view hs_jsonb_dependent_view")
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT count(*) AS n FROM pg_attribute WHERE attrelid='hs_jsonb_dependent'::regclass AND attnum>0 AND NOT attisdropped`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			fixture.Select(`SELECT count(*) AS n FROM hs_jsonb_dependent`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			fixture.Select(`SELECT count(*) AS n FROM pg_class WHERE relname IN ('hs_jsonb_dependent_view','hs_drop_mv')`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			fixture.Select(`SELECT count(*) AS n FROM pg_extension WHERE extname='hstore'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
		} {
			run(s)
		}
		hstoreInstall(t, p, b, c)
		hstoreJSONBCatalogCheck(t, p, b, c)
		hstoreSubscriptCatalogCheck(t, p, b, c, true)
	})
}

// Consumers referencing several members, directly and through another view,
// are each reported once, through the path PostgreSQL reaches first.
func TestReferenceHstoreDropMultiMember(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		hstoreInstall(t, p, b, c)
		for _, q := range []string{
			"CREATE TABLE hs_drop_t(id integer, v hstore)",
			"CREATE VIEW hs_drop_multi AS SELECT id,'a=>1'::hstore -> 'a' AS f,hstore_to_json(v) AS j FROM hs_drop_t",
			"CREATE VIEW hs_drop_outer AS SELECT id,j FROM hs_drop_multi",
			"CREATE TABLE hs_drop_def(id integer, k text DEFAULT (hstore_to_json('a=>1'::hstore))::text)",
		} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_drop_t VALUES (1,'a=>1')", 1))
		restrict := func(detail string) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("DROP EXTENSION hstore", hstoreDropRestrict(detail))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				hstoreDropCascaded(t, &notices)
			}
		}
		restrict("column v of table hs_drop_t depends on type hstore\nview hs_drop_multi depends on type hstore\nview hs_drop_outer depends on view hs_drop_multi\ndefault value for column k of table hs_drop_def depends on type hstore")
		hstoreRun(t, p, b, c, fixture.Select(`SELECT id,j::text AS j FROM hs_drop_outer`, []string{"id", "j"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), `{"a": "1"}`}}))
		hstoreRun(t, p, b, c, fixture.Exec("DROP VIEW hs_drop_outer, hs_drop_multi", 0))
		restrict("column v of table hs_drop_t depends on type hstore\ndefault value for column k of table hs_drop_def depends on type hstore")
		run(fixture.Exec("DROP EXTENSION hstore CASCADE", 0))
		hstoreDropCascaded(t, &notices, "column v of table hs_drop_t", "default value for column k of table hs_drop_def")
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_drop_def(id) VALUES (1)", 1))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT id,k FROM hs_drop_def`, []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT id FROM hs_drop_t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

// A view's output columns have their own datatype dependencies, so a view
// over a view is reported through the inner view's column.
func TestReferenceHstoreDropViewChain(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, q := range []string{
			"CREATE TABLE hs_chain_t(id integer, h hstore)",
			"CREATE VIEW hs_chain_v AS SELECT id,h FROM hs_chain_t",
			"CREATE VIEW hs_chain_w AS SELECT h FROM hs_chain_v",
		} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		want := hstoreDropRestrict("column h of table hs_chain_t depends on type hstore\nview hs_chain_v depends on column h of table hs_chain_t\nview hs_chain_w depends on column h of view hs_chain_v")
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, "DROP EXTENSION hstore", prepared, false, false, want)
		}
		hstoreRun(t, p, b, c, fixture.Exec("DROP EXTENSION hstore CASCADE", 0))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT count(*) AS n FROM pg_class WHERE relname IN ('hs_chain_v','hs_chain_w')`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT count(*) AS n FROM pg_attribute WHERE attrelid='hs_chain_t'::regclass AND attnum>0 AND NOT attisdropped`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

// pg_depend records a function call, not its result type, so a view
// computing an hstore value is reported through the called member.
func TestReferenceHstoreDropComputedType(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_drop_fn AS SELECT hstore('a','b') AS h", 0))
		want := hstoreDropRestrict("view hs_drop_fn depends on function hstore(text,text)")
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, "DROP EXTENSION hstore", prepared, false, false, want)
		}
		hstoreRun(t, p, b, c, fixture.Select(`SELECT h::text AS h FROM hs_drop_fn`, []string{"h"}, []string{"TEXT"}, [][]any{{`"a"=>"b"`}}))
	})
}
