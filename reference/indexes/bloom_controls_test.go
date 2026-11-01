package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func bloomControlRoles(run func(fixture.Step)) {
	run(fixture.Exec("CREATE ROLE bloom_control_owner", 0))
	run(fixture.Exec("CREATE ROLE bloom_control_reader", 0))
}

func bloomControlSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE SCHEMA bloom_portable", 0))
	run(fixture.Exec("CREATE EXTENSION bloom SCHEMA bloom_portable", 0))
	bloomSeed(run, "bloom_portable.t")
	run(fixture.Exec("CREATE INDEX bloom_multi ON bloom_portable.t USING bloom(i bloom_portable.int4_ops,s bloom_portable.text_ops) WITH(length=1,col1=1,col2=1,col32=4095)", 0))
	run(fixture.Exec("CREATE INDEX bloom_expression ON bloom_portable.t USING bloom((i+1) bloom_portable.int4_ops) WITH(length=7,col32=4095) WHERE i IS NOT NULL", 0))
	run(fixture.Exec("CREATE VIEW bloom_portable.v AS SELECT i,s FROM bloom_portable.t WHERE i IS NOT NULL", 0))
	run(fixture.Exec("ALTER TABLE bloom_portable.t OWNER TO bloom_control_owner", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA bloom_portable TO bloom_control_reader", 0))
	run(fixture.Exec("GRANT SELECT ON bloom_portable.t,bloom_portable.v TO bloom_control_reader", 0))
}

func bloomControlCheck(run func(fixture.Step), b fixture.Backend) {
	bloomIdentity(run, "bloom_portable")
	bloomOptionsMetadata(run, "bloom_portable.bloom_multi", "{col1=1,col2=1,col32=4095,length=7}")
	bloomOptionsMetadata(run, "bloom_portable.bloom_expression", "{length=7,col32=4095}")
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t", bloomFinal()))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t WHERE i=9 AND s='a'", [][]any{{int64(9), "a"}, {int64(9), "a"}}))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t WHERE i+1=10 AND i IS NOT NULL", [][]any{{int64(9), "a"}, {int64(9), "a"}, {int64(9), nil}, {int64(9), "z"}}))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.v", [][]any{{int64(9), "a"}, {int64(9), "a"}, {int64(9), nil}, {int64(9), "z"}}))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.v WHERE i=99", nil))
	bloomBoth(run, fixture.Select("SELECT pg_get_expr(indexprs,indrelid) AS expression,pg_get_expr(indpred,indrelid) AS predicate,indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='bloom_portable.bloom_expression'::regclass", []string{"expression", "predicate", "indisvalid", "indisready", "indislive"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"(i + 1)", "(i IS NOT NULL)", true, true, true}}))
	for _, key := range []struct{ slot, class string }{{"0", "int4_ops"}, {"1", "text_ops"}} {
		bloomBoth(run, fixture.Select("SELECT a.amname,c.opcname,n.nspname,c.opcmethod=a.oid AS method_link,c.opcfamily=f.oid AS family_link,f.opfmethod=a.oid AS family_method FROM pg_index i JOIN pg_class r ON r.oid=i.indexrelid JOIN pg_am a ON a.oid=r.relam JOIN pg_opclass c ON c.oid=i.indclass["+key.slot+"] JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_namespace n ON n.oid=c.opcnamespace WHERE i.indexrelid='bloom_portable.bloom_multi'::regclass", []string{"amname", "opcname", "nspname", "method_link", "family_link", "family_method"}, []string{"NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"bloom", key.class, "bloom_portable", true, true, true}}))
	}
	bloomBoth(run, fixture.Select("SELECT pg_get_userbyid(relowner) AS owner,has_table_privilege('bloom_control_reader',oid,'SELECT') AS readable FROM pg_class WHERE oid='bloom_portable.t'::regclass", []string{"owner", "readable"}, []string{"NAME", "BOOL"}, [][]any{{"bloom_control_owner", true}}))
	// Table/index ownership does not grant namespace rights. These remain
	// absent before replay, after replacement and in the independent Driver.
	bloomBoth(run, fixture.Select("SELECT has_schema_privilege('bloom_control_owner','bloom_portable','USAGE') AS schema_usage,has_schema_privilege('bloom_control_owner','bloom_portable','CREATE') AS schema_create", []string{"schema_usage", "schema_create"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
	bloomBoth(run, fixture.Select("SELECT relname,pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid IN ('bloom_portable.bloom_multi'::regclass,'bloom_portable.bloom_expression'::regclass) ORDER BY relname", []string{"relname", "owner"}, []string{"NAME", "NAME"}, [][]any{{"bloom_expression", "bloom_control_owner"}, {"bloom_multi", "bloom_control_owner"}}))
}

func bloomControlPostDML(run func(fixture.Step), b fixture.Backend) {
	run(fixture.Prepared(fixture.Exec("INSERT INTO bloom_portable.t VALUES($1,$2)", 1, 10, "q")))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t WHERE i=$1 AND s=$2", [][]any{{int64(10), "q"}}, 10, "q"))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.v WHERE i=10", [][]any{{int64(10), "q"}}))
	run(fixture.Exec("UPDATE bloom_portable.t SET s='r' WHERE i=10", 1))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t WHERE i+1=11 AND i IS NOT NULL", [][]any{{int64(10), "r"}}))
	run(fixture.Exec("DELETE FROM bloom_portable.t WHERE i=10", 1))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t WHERE i=10", nil))
}

// The same logical replay is runnable before native exists. Once native emits a
// dump, the PostgreSQL phase additionally executes that actual unchanged stream.
// Only native control operations/retained eager handles are skipped for PG.
func bloomControlRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted *string) {
	t.Helper()
	run := bloomRun(t, p, b, c)
	run(fixture.Exec("ALTER INDEX bloom_portable.bloom_multi SET(length=7)", 0))
	bloomControlCheck(run, b)
	held := bloomHeld(t, p, c, "SELECT i,s FROM bloom_portable.t WHERE i=$1")
	wantHeld := bloomSelect(b, held.SQL, [][]any{{int64(9), "a"}, {int64(9), "a"}, {int64(9), nil}, {int64(9), "z"}}, 9)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)
	if b.PG {
		run(fixture.Exec("DROP EXTENSION bloom CASCADE", 0))
		run(fixture.Exec("DROP SCHEMA bloom_portable CASCADE", 0))
		if *emitted != "" {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			if _, err := c.ExecContext(ctx, *emitted); err != nil {
				t.Fatalf("PostgreSQL rejected actual native bloom Dump: %v\n%s", err, *emitted)
			}
		} else {
			bloomControlSeed(run)
			bloomDML(run, b, "bloom_portable.t")
			run(fixture.Exec("ALTER INDEX bloom_portable.bloom_multi SET(length=7)", 0))
		}
	} else {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		*emitted = dump.String()
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
		old, err := b.DB.QueryContext(ctx, "SELECT i,s FROM bloom_portable.t")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil || len(metadata) != 2 {
			t.Fatal(metadata, err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(amCount("SELECT count(*) FROM pg_am WHERE amname='bloom'", 0))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.DeleteReferenceConsume(t, false, old, nil, bloomSelect(b, "SELECT i,s FROM bloom_portable.t", bloomFinal()))
		if metadata[0].DatabaseTypeName() != "INT4" || metadata[1].DatabaseTypeName() != "TEXT" {
			t.Fatal("replacement changed retained descriptors", metadata)
		}
		bloomControlCheck(run, b)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)

		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/bloom-independent")
		if err != nil {
			t.Fatal(err)
		}
		target := sql.OpenDB(connector)
		defer target.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		targetRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
		bloomControlRoles(targetRun)
		if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("independent Driver Restore", err)
		}
		bloomControlCheck(targetRun, fixture.Backend{})
		bloomControlPostDML(targetRun, fixture.Backend{})
		bloomControlCheck(run, b) // destination writes cannot mutate source

		// A real late option failure and an ordinary cancellation must leave
		// the existing destination intact, not a partially replaced catalog.
		bad := dump.String() + "\nALTER INDEX bloom_portable.bloom_multi SET(length=0);"
		err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
		d := fixture.ControlsReferenceDiagnostic("22023", `value 0 out of bounds for option "length"`)
		d.Detail = `Valid values are between "1" and "4096".`
		fixture.AssertDiagnostic(t, err, &d)
		canceled, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("Restore cancellation", err)
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
			t.Fatal("failed replacement or independent writes changed source", err)
		}
	}
	bloomControlCheck(run, b)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)
	bloomControlPostDML(run, b)
}

func TestReferenceBloomControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bloom_control_owner", "bloom_control_reader"})
	fixture.SchemaReferenceCleanup(t, "bloom_portable")
	var emitted string
	bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := bloomRun(t, p, b, c)
		bloomControlRoles(run)
		bloomControlSeed(run)
		bloomDML(run, b, "bloom_portable.t")
		bloomControlRoundtrip(t, p, b, c, &emitted)
	})
}
