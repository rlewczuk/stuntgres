package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type overexplainProviderIdentity struct {
	extension, namespace, owner, template, dictionary int64
	members                                           map[[2]int64]bool
	relations                                         map[string]int64
}

func overexplainProvider(t *testing.T, p *fixture.Pair, c *sql.Conn) overexplainProviderIdentity {
	t.Helper()
	id := overexplainProviderIdentity{members: make(map[[2]int64]bool), relations: make(map[string]int64)}
	var name, version, schema, owner string
	var relocatable bool
	var config, condition any
	// CREATE EXTENSION is issued by this session's bootstrap installer. The
	// pinned cluster calls it stuntgres; a fresh native Driver calls it postgres.
	var installer string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&installer); err != nil {
		t.Fatal(err)
	}
	err := c.QueryRowContext(p.Ctx, "SELECT e.oid,e.extnamespace,e.extowner,e.extname,e.extversion,e.extrelocatable,e.extconfig,e.extcondition,n.nspname,r.rolname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='unaccent'").Scan(&id.extension, &id.namespace, &id.owner, &name, &version, &relocatable, &config, &condition, &schema, &owner)
	if err != nil || id.extension <= 0 || id.namespace <= 0 || id.owner <= 0 || name != "unaccent" || version != "1.1" || !relocatable || config != nil || condition != nil || schema != "public" || owner != installer {
		t.Fatalf("installed provider identity: %+v %s %s %s %s want owner %s %v: %v", id, name, version, schema, owner, installer, relocatable, err)
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", id.extension)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[int64]int{}
	for rows.Next() {
		var class, object, sub, refclass, refid, refsub int64
		var kind string
		if err := rows.Scan(&class, &object, &sub, &refclass, &refid, &refsub, &kind); err != nil {
			t.Fatal(err)
		}
		key := [2]int64{class, object}
		if object <= 0 || sub != 0 || refclass != 3079 || refid != id.extension || refsub != 0 || kind != "e" || id.members[key] {
			t.Fatal("invalid real member dependency", key, refclass, refid, kind)
		}
		id.members[key], counts[class] = true, counts[class]+1
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !reflect.DeepEqual(counts, map[int64]int{1255: 4, 3764: 1, 3600: 1}) {
		t.Fatal("provider membership", counts)
	}
	var templateNS, dictionaryNS, dictionaryOwner, dictionaryTemplate int64
	var options string
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,tmplnamespace FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace=$1", id.namespace).Scan(&id.template, &templateNS); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,dictnamespace,dictowner,dicttemplate,dictinitoption FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace=$1", id.namespace).Scan(&id.dictionary, &dictionaryNS, &dictionaryOwner, &dictionaryTemplate, &options); err != nil {
		t.Fatal(err)
	}
	if !id.members[[2]int64{3764, id.template}] || !id.members[[2]int64{3600, id.dictionary}] || templateNS != id.namespace || dictionaryNS != id.namespace || dictionaryOwner != id.owner || dictionaryTemplate != id.template || options != "rules = 'unaccent'" {
		t.Fatalf("invalid provider template/dictionary links %+v", id)
	}
	var init, lexize int64
	if err := c.QueryRowContext(p.Ctx, "SELECT tmplinit::oid,tmpllexize::oid FROM pg_ts_template WHERE oid=$1", id.template).Scan(&init, &lexize); err != nil {
		t.Fatal(err)
	}
	if !id.members[[2]int64{1255, init}] || !id.members[[2]int64{1255, lexize}] || init == lexize {
		t.Fatal("template lost installed executable providers", init, lexize)
	}
	rows, err = c.QueryContext(p.Ctx, "SELECT oid,relname,relkind,relowner FROM pg_class WHERE relnamespace=$1 AND relname IN ('overexplain_provider_data','overexplain_translated')", id.namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var oid, owner int64
		var name, kind string
		if err := rows.Scan(&oid, &name, &kind, &owner); err != nil {
			t.Fatal(err)
		}
		wantKind := "r"
		if name == "overexplain_translated" {
			wantKind = "v"
		}
		if oid <= 0 || owner != id.owner || kind != wantKind {
			t.Fatal("persistent object identity", oid, name, kind, owner)
		}
		id.relations[name] = oid
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(id.relations) != 2 {
		t.Fatal("missing persistent table/view identities", id.relations)
	}
	return id
}

func overexplainProviderValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, q := range []string{"SELECT id,v FROM overexplain_translated ORDER BY id", "SELECT id,unaccent(v) AS v FROM overexplain_provider_data ORDER BY id"} {
		s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pg_overexplain'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func TestReferenceOverexplainControls(t *testing.T) {
	overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE EXTENSION unaccent", "CREATE TABLE overexplain_provider_data(id integer,v text)", "CREATE VIEW overexplain_translated AS SELECT id,unaccent(v) AS v FROM overexplain_provider_data"} {
			overexplainExec(t, p, b, c, q)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO overexplain_provider_data VALUES(1,'Hôtel'),(2,NULL)", 2))
		before := overexplainProvider(t, p, c)
		overexplainProviderValues(t, p, b, c)
		overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
		if after := overexplainProvider(t, p, c); !reflect.DeepEqual(before, after) {
			t.Fatal("LOAD changed installed provider/object identities", before, after)
		}
		const q = "EXPLAIN (DEBUG, VERBOSE, FORMAT JSON, COSTS FALSE) SELECT id,v FROM overexplain_translated ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		retained := overexplainQuery(t, p, executor, q, "JSON", false)
		snapshot := fmt.Sprint(retained)
		overexplainDebug(t, b, retained, "JSON", true)
		overexplainProviderValues(t, p, b, c)
		if b.PG {
			return
		} // Actual controls are native APIs, never fake PG commands.
		overexplainExec(t, p, b, c, "PREPARE overexplain_control_handle AS SELECT id FROM overexplain_provider_data")
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "pg_overexplain") || strings.Contains(dump.String(), "overexplain_control_handle") {
			t.Fatal("dump serialized module activation or session handles", dump.String())
		}
		// Persistent catalog identities are legitimately remapped on replacement;
		// exact TEMP-target retention is a separate established control contract.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		overexplainProvider(t, p, c)
		overexplainProviderValues(t, p, b, c)
		overexplainDebug(t, b, overexplainQuery(t, p, executor, q, "JSON", false), "JSON", true)
		if fmt.Sprint(retained) != snapshot {
			t.Fatal("retained plan mutated by actual catalog replacement")
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		// Reset clears persistent objects, not this physical owner's activation.
		overexplainDebug(t, b, overexplainQuery(t, p, c, "EXPLAIN (DEBUG, FORMAT JSON) SELECT 1", "JSON", true), "JSON", true)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		overexplainProvider(t, p, c)
		overexplainProviderValues(t, p, b, c)
		overexplainDebug(t, b, overexplainQuery(t, p, c, q, "JSON", true), "JSON", true)
		connector, err := stuntgres.NewDriver().OpenConnector("overexplain-independent-destination")
		if err != nil {
			t.Fatal(err)
		}
		recorded, other, recorder := fixture.DatabaseConnectorScenarioPool(t, connector, false)
		destinationBackend := b
		destinationBackend.DB, destinationBackend.Recorder = recorded, recorder
		destination, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		overexplainUnloaded(t, p, destinationBackend, destination)
		if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		overexplainProvider(t, p, destination)
		overexplainProviderValues(t, p, destinationBackend, destination)
		overexplainUnloaded(t, p, destinationBackend, destination)
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT count(*) FROM pg_prepared_statements WHERE name='overexplain_control_handle'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		overexplainExec(t, p, destinationBackend, destination, "LOAD 'pg_overexplain'")
		loadedIDs := overexplainProvider(t, p, destination)
		overexplainExec(t, p, destinationBackend, destination, "LOAD 'pg_overexplain'")
		if after := overexplainProvider(t, p, destination); !reflect.DeepEqual(loadedIDs, after) {
			t.Fatal("repeat activation changed imported provider identities")
		}
		overexplainDebug(t, b, overexplainQuery(t, p, destination, q, "JSON", true), "JSON", true)
		if err := destination.Close(); err != nil {
			t.Fatal(err)
		}
		if err := recorded.Close(); err != nil {
			t.Fatal(err)
		}
		if err := other.Close(); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(retained) != snapshot {
			t.Fatal("old document changed after destination physical teardown")
		}
	})
}
