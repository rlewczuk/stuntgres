package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGRewriteControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)", "CREATE TABLE base(id INT,v INT)")
			rewriteEffect(t, p, b, "INSERT INTO base VALUES(1,10)", 1)
			rewriteExec(t, p, b, "CREATE VIEW v(k,label) AS SELECT id,v FROM base WHERE v>0")
			rewriteEffect(t, p, b, "CREATE MATERIALIZED VIEW mv AS SELECT id FROM base", 1)
			rewriteExec(t, p, b,
				"CREATE RULE r AS ON INSERT TO src WHERE NEW.v>0 DO ALSO (INSERT INTO dst VALUES(NEW.v); SELECT NEW.v)",
				"CREATE RULE nothing AS ON DELETE TO src DO INSTEAD NOTHING", "ALTER TABLE src ENABLE ALWAYS RULE r")
			if !b.PG {
				before := rewriteTrees(t, p, b, "src", "r")
				edges := rewriteEdges(t, p, b, "src", "r")
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				emitted = bytes.Clone(dump.Bytes())
				rewriteExec(t, p, b, "CREATE TABLE sentinel(v INT)")
				rewriteEffect(t, p, b, "INSERT INTO sentinel VALUES(99)", 1)
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+"\nALTER TABLE public.src DISABLE RULE absent;")); err == nil {
					t.Fatal("late malformed restore accepted")
				}
				if before != rewriteTrees(t, p, b, "src", "r") || !reflect.DeepEqual(edges, rewriteEdges(t, p, b, "src", "r")) {
					t.Fatal("failed restore changed original rule/dependencies")
				}
				rewriteRows(t, p, b, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
				retained, err := b.DB.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if before != rewriteTrees(t, p, b, "src", "r") || !reflect.DeepEqual(edges, rewriteEdges(t, p, b, "src", "r")) {
					t.Fatal("canceled restore changed definition")
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				rewriteExec(t, p, b, "CREATE TABLE sentinel(v INT)", "CREATE RULE sentinel_rule AS ON INSERT TO sentinel DO NOTHING")
				rewriteEffect(t, p, b, "INSERT INTO sentinel VALUES(99)", 1)
				sentinel := rewriteTrees(t, p, b, "sentinel", "sentinel_rule")
				sentinelEdges := rewriteEdges(t, p, b, "sentinel", "sentinel_rule")
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+"\nALTER TABLE public.src DISABLE RULE absent;")); err == nil {
					t.Fatal("late malformed restore succeeded")
				}
				if sentinel != rewriteTrees(t, p, b, "sentinel", "sentinel_rule") || !reflect.DeepEqual(sentinelEdges, rewriteEdges(t, p, b, "sentinel", "sentinel_rule")) {
					t.Fatal("failed restore partially published")
				}
				rewriteRows(t, p, b, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if sentinel != rewriteTrees(t, p, b, "sentinel", "sentinel_rule") {
					t.Fatal("canceled sentinel restore published")
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				if got := rewriteTreeRows(t, b, retained); got != before {
					t.Fatal("reset/restore changed retained tree", got, before)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(emitted, again.Bytes()) {
					t.Fatal("dump restore redump changed definitions", string(emitted), again.String())
				}
				rewriteCount(t, p, b, "SELECT count(*) FROM pg_class WHERE relname='sentinel'", 0)
			} else if emitted != nil {
				rewriteExec(t, p, b, "DROP TABLE src,dst,base CASCADE", string(emitted))
			}
			// Logical dumps declare materialized views WITH NO DATA. PostgreSQL
			// replay needs population; exercise the same refresh in both backends.
			materializedTree := rewriteTrees(t, p, b, "mv", "_RETURN")
			rewriteExec(t, p, b, "REFRESH MATERIALIZED VIEW mv")
			if got := rewriteTrees(t, p, b, "mv", "_RETURN"); got != materializedTree {
				t.Fatal("refresh changed stored rewrite definition", got, materializedTree)
			}
			for _, relation := range []string{"v", "mv"} {
				pair := rewriteTrees(t, p, b, relation, "_RETURN")
				if pair[0] != "<>" || pair[1] == "" {
					t.Fatal(pair)
				}
			}
			rewriteRows(t, p, b, fixture.Select("SELECT rulename,ev_enabled FROM pg_rewrite WHERE ev_class='src'::regclass ORDER BY rulename", []string{"rulename", "ev_enabled"}, []string{"NAME", "CHAR"}, [][]any{{"nothing", "O"}, {"r", "A"}}))
			rewriteRows(t, p, b, fixture.Select("SELECT k,label FROM v", []string{"k", "label"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			rewriteRows(t, p, b, fixture.Select("SELECT id FROM mv", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			rewriteEffect(t, p, b, "INSERT INTO src VALUES(7)", 1)
			rewriteRows(t, p, b, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			rewriteEffect(t, p, b, "DELETE FROM src", 0)
			rewriteCount(t, p, b, "SELECT count(*) FROM src", 1)
		})
	}
}

func TestReferencePGRewriteSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)", "CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v+1)", "CREATE VIEW v AS SELECT v FROM dst")
			before := rewriteTrees(t, p, b, "src", "r")
			view := rewriteTrees(t, p, b, "v", "_RETURN")
			if before[0] != "<>" || before[1] == "" || view[0] != "<>" || view[1] == "" {
				t.Fatal(before, view)
			}
			metadata, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_rewrite WHERE ev_class='src'::regclass")
			if err != nil {
				t.Fatal(err)
			}
			types, err := metadata.ColumnTypes()
			if err != nil || len(types) != 8 {
				t.Fatal(types, err)
			}
			for i, name := range []string{"OID", "NAME", "OID", "CHAR", "CHAR", "BOOL", "PG_NODE_TREE", "PG_NODE_TREE"} {
				if types[i].DatabaseTypeName() != name {
					t.Fatal(i, types[i])
				}
			}
			metadata.Close()
			rewriteRows(t, p, b, fixture.Select("SELECT rulename,ev_type,ev_enabled,is_instead,ev_qual IS NOT NULL AS qual,ev_action IS NOT NULL AS action FROM pg_rewrite WHERE ev_class='src'::regclass", []string{"rulename", "ev_type", "ev_enabled", "is_instead", "qual", "action"}, []string{"NAME", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"r", "3", "O", true, true, true}}))
			rewriteEffect(t, p, b, "INSERT INTO src VALUES(10)", 1)
			rewriteCount(t, p, b, "SELECT count(*) FROM src", 0)
			rewriteRows(t, p, b, fixture.Select("SELECT v FROM v", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
			retained, err := b.DB.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			rewriteExec(t, p, b, "CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v+2)")
			if got := rewriteTreeRows(t, b, retained); got != before {
				t.Fatal("retained tree changed")
			}
			s := fixture.ImplicitError("SELECT pg_get_expr(ev_action,0) FROM pg_rewrite WHERE ev_class='src'::regclass", fixture.Diagnostic{Code: "22023", Message: "input is a query, not an expression"})
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			rewriteExec(t, p, b, "CREATE RULE qual AS ON INSERT TO src WHERE EXISTS(SELECT v FROM dst) DO ALSO NOTHING")
			rewriteRows(t, p, b, fixture.Select("SELECT pg_get_expr(ev_qual,0) AS expression FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='qual'", []string{"expression"}, []string{"TEXT"}, [][]any{{"(EXISTS ( SELECT v\n   FROM dst))"}}))
			id := rewriteOID(t, p, b, "src", "r")
			rewriteExec(t, p, b, "DROP TABLE dst CASCADE")
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid="+fmtRewriteOID(id), 0)
		})
	}
	t.Run("selected_stage", func(t *testing.T) { rewriteSelectedRule(t, "aggregate") })
	t.Run("assignment_target", rewriteAssignmentTargetSmoke)
	t.Run("action_default", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) { rewriteAssignmentActionDefaults(t, p, b, false, "action_default") })
		}
	})
}
