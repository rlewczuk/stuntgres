package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

type synonymState struct {
	id, namespace, owner, template int64
	options                        string
	dependencies                   [][4]any
}

func synonymSnapshot(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) synonymState {
	t.Helper()
	var state synonymState
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dictnamespace::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1::regdictionary", name).Scan(&state.id, &state.namespace, &state.owner, &state.template, &state.options); err != nil {
		t.Fatal(err)
	}
	if state.id <= 0 || state.namespace <= 0 || state.owner <= 0 || state.template != 3730 {
		t.Fatal("synonym identity", state)
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT refclassid::bigint,refobjid::bigint,refobjsubid::bigint,deptype::text FROM pg_depend WHERE classid=3600 AND objid=$1::oid ORDER BY refclassid,refobjid,refobjsubid,deptype", state.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var class, id, sub int64
		var kind string
		if err := rows.Scan(&class, &id, &sub, &kind); err != nil {
			t.Fatal(err)
		}
		state.dependencies = append(state.dependencies, [4]any{class, id, sub, kind})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The template is pinned, so PG may omit that edge. The ordinary namespace
	// edge must really exist; the full captured dependency bag must then survive.
	found := false
	for _, dep := range state.dependencies {
		if dep == [4]any{int64(2615), state.namespace, int64(0), "n"} {
			found = true
		}
	}
	if !found {
		t.Fatal("missing namespace dependency", state)
	}
	return state
}

func synonymUnchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, name string, before synonymState) {
	t.Helper()
	if after := synonymSnapshot(t, p, c, name); !reflect.DeepEqual(before, after) {
		t.Fatalf("synonym publication changed: before=%+v after=%+v", before, after)
	}
}

func TestReferenceSynonymPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		synonymCreate(t, p, b, c, "synonym_held")
		synonymConfig(t, p, b, c, "synonym_held_config", "synonym_held")
		captured := synonymSnapshot(t, p, c, "synonym_held")
		numericConstantSQL := fmt.Sprintf("SELECT ts_lexize(%d::oid::regdictionary,'POSTGRES'::text) AS lexemes", captured.id)
		numericConstant := synonymHeld(t, p, c, numericConstantSQL)
		defer numericConstant.Statement.Close()
		constantSQL := "SELECT ts_lexize('public.synonym_held'::regdictionary,'POSTGRES'::text) AS lexemes"
		constant := synonymHeld(t, p, c, constantSQL)
		defer constant.Statement.Close()
		parameterSQL := "SELECT ts_lexize('public.synonym_held'::regdictionary,$1::text) AS lexemes"
		parameter := synonymHeld(t, p, c, parameterSQL)
		defer parameter.Statement.Close()
		dynamicSQL := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		dynamic := synonymHeld(t, p, c, dynamicSQL)
		defer dynamic.Statement.Close()
		querySQL := "SELECT to_tsquery('synonym_held_config'::regconfig,'indices'::text) AS normalized"
		query := synonymHeld(t, p, c, querySQL)
		defer query.Statement.Close()
		queryParameterSQL := "SELECT to_tsquery('synonym_held_config'::regconfig,$1::text) AS normalized"
		queryParameter := synonymHeld(t, p, c, queryParameterSQL)
		defer queryParameter.Statement.Close()
		queryUpperSQL := "SELECT to_tsquery('synonym_held_config'::regconfig,'POSTGRES'::text) AS normalized"
		queryUpper := synonymHeld(t, p, c, queryUpperSQL)
		defer queryUpper.Statement.Close()
		regtsExec(t, p, b, c, "PREPARE synonym_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE synonym_named", 0)
		check := func(upper any, normalized string) {
			dictionaryResult(t, p, b, constant, constantSQL, "{pgsql}")
			dictionaryResult(t, p, b, numericConstant, numericConstantSQL, "{pgsql}")
			dictionaryResult(t, p, b, c, "EXECUTE synonym_named", "{pgsql}")
			dictionaryResult(t, p, b, c, constantSQL, upper)
			fresh := synonymHeld(t, p, c, constantSQL)
			dictionaryResult(t, p, b, fresh, constantSQL, upper)
			fresh.Statement.Close()
			dictionaryResult(t, p, b, parameter, parameterSQL, upper, "POSTGRES")
			dictionaryResult(t, p, b, dynamic, dynamicSQL, upper, "synonym_held", "POSTGRES")
			dictionaryResult(t, p, b, parameter, parameterSQL, "{pgsql}", "postgres")
			dictionaryResult(t, p, b, parameter, parameterSQL, nil, nil)
			synonymConverted(t, p, b, query, querySQL, "TSQUERY", "'index':*")
			synonymConverted(t, p, b, queryUpper, queryUpperSQL, "TSQUERY", "'pgsql'")
			synonymConverted(t, p, b, queryParameter, queryParameterSQL, "TSQUERY", normalized, "POSTGRES")
			synonymConverted(t, p, b, queryParameter, queryParameterSQL, "TSQUERY", "'index':*AB", "indices:AB")
		}
		check("{pgsql}", "'pgsql'")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_held (CaseSensitive=true)", 0)
		check(nil, "'postgres'")
		before := synonymSnapshot(t, p, c, "synonym_held")
		synonymMissingResource(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_held (synonyms=synonym_missing)", "synonym_missing")
		synonymUnchanged(t, p, c, "synonym_held", before)
		check(nil, "'postgres'")
		dictionaryResult(t, p, b, dynamic, dynamicSQL, "{pgsql}", before.id, "postgres")
		regtsExec(t, p, b, c, "CREATE SCHEMA dictionary_right", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_held RENAME TO synonym_moved", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_moved SET SCHEMA dictionary_right", 0)
		regtsSelect(t, p, b, c, "SELECT 'dictionary_right.synonym_moved'::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{before.id}})
		staleName := dictionaryDiagnostic("42704", `text search dictionary "public.synonym_held" does not exist`)
		staleName.Position = "18"
		stale := fixture.ErrorDiagnostic(parameterSQL, staleName, "POSTGRES")
		stale.SelectRows, stale.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, parameter, b.Recorder, stale)
		dictionaryResult(t, p, b, dynamic, dynamicSQL, nil, before.id, "POSTGRES")
		dictionaryResult(t, p, b, dynamic, dynamicSQL, "{pgsql}", before.id, "postgres")
		dictionaryResult(t, p, b, dynamic, dynamicSQL, "{pgsql}", "dictionary_right.synonym_moved", "postgres")
		regtsExec(t, p, b, c, "CREATE TABLE synonym_unrelated(v int)", 0)
		staleConstant := fixture.ErrorDiagnostic(constantSQL, staleName)
		staleConstant.SelectRows, staleConstant.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, constant, b.Recorder, staleConstant)
		// Pinned PostgreSQL re-evaluates this numeric constant after identity
		// DDL; its captured OID survives, not its earlier insensitive result.
		dictionaryResult(t, p, b, numericConstant, numericConstantSQL, nil)
		dictionaryResult(t, p, b, dynamic, dynamicSQL, nil, captured.id, "POSTGRES")
		dictionaryResult(t, p, b, dynamic, dynamicSQL, "{pgsql}", captured.id, "postgres")
		synonymConverted(t, p, b, queryParameter, queryParameterSQL, "TSQUERY", "'postgres'", "POSTGRES")
		// Drop/recreate is not a rename: a retained numeric identity cannot be
		// redirected to the newly created dictionary of the same spelling.
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_right.synonym_moved CASCADE", 0)
		synonymCreate(t, p, b, c, "dictionary_right.synonym_moved")
		regtsSelect(t, p, b, c, "SELECT 'dictionary_right.synonym_moved'::regdictionary::oid::bigint<>$1 AS distinct_identity", []string{"distinct_identity"}, []string{"BOOL"}, [][]any{{true}}, before.id)
		dictionaryResult(t, p, b, dynamic, dynamicSQL, "{pgsql}", "dictionary_right.synonym_moved", "POSTGRES")
		d := dictionaryDiagnostic("XX000", fmt.Sprintf("cache lookup failed for text search dictionary %d", before.id))
		s := fixture.ErrorDiagnostic(dynamicSQL, d, before.id, "POSTGRES")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, dynamic, b.Recorder, s)
	})
}

func TestReferenceSynonymOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.synonym_owned (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)", "RESET ROLE"} {
			regtsExec(t, p, b, c, q, 0)
		}
		before := synonymSnapshot(t, p, c, "dictionary_owned.synonym_owned")
		regtsExec(t, p, b, c, "SET ROLE dictionary_peer", 0)
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.synonym_denied (TEMPLATE=synonym,Synonyms=synonym_sample)", "42501", "permission denied for schema dictionary_owned")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.synonym_denied (TEMPLATE=synonym,Synonyms=synonym_sample)", "42501", "permission denied for schema pg_catalog")
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.synonym_owned (CaseSensitive=true)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.synonym_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.synonym_owned")
			synonymUnchanged(t, p, c, "dictionary_owned.synonym_owned", before)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.synonym_owned','POSTGRES') AS lexemes", "{pgsql}")
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.synonym_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "synonyms = 'synonym_sample'"}})
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='synonym_denied'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceSynonymAtomicity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		synonymCreate(t, p, b, c, "synonym_atomic")
		before := synonymSnapshot(t, p, c, "synonym_atomic")
		for _, q := range []string{"BEGIN", "ALTER TEXT SEARCH DICTIONARY synonym_atomic (casesensitive=true)", "SAVEPOINT synonym_save", "ALTER TEXT SEARCH DICTIONARY synonym_atomic (casesensitive)", "ROLLBACK TO synonym_save"} {
			regtsExec(t, p, b, c, q, 0)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_atomic','POSTGRES') AS lexemes", nil)
		regtsExec(t, p, b, c, "ROLLBACK", 0)
		synonymUnchanged(t, p, c, "synonym_atomic", before)
		for _, q := range []string{"BEGIN", "CREATE TEXT SEARCH DICTIONARY synonym_rolled (TEMPLATE=synonym,Synonyms=synonym_sample)", "ALTER TEXT SEARCH DICTIONARY synonym_atomic RENAME TO synonym_temporal", "ROLLBACK"} {
			regtsExec(t, p, b, c, q, 0)
		}
		synonymUnchanged(t, p, c, "synonym_atomic", before)
		for _, tc := range []struct{ options, code, message string }{
			{"casesensitive=true,unknown=1", "22023", `unrecognized synonym parameter: "unknown"`}, {"synonyms", "22023", "missing Synonyms parameter"}, {"casesensitive='yes'", "42601", "casesensitive requires a Boolean value"},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_atomic ("+tc.options+")", tc.code, tc.message)
			synonymUnchanged(t, p, c, "synonym_atomic", before)
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_atomic','POSTGRES') AS lexemes", "{pgsql}")
		}
		synonymMissingResource(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_failed (TEMPLATE=synonym,Synonyms=synonym_missing)", "synonym_missing")
		synonymMissingResource(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_atomic (casesensitive=true,synonyms=synonym_missing)", "synonym_missing")
		synonymUnchanged(t, p, c, "synonym_atomic", before)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('synonym_failed','synonym_rolled','synonym_temporal')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend dep LEFT JOIN pg_ts_dict d ON d.oid=dep.objid WHERE dep.classid=3600 AND d.oid IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		for _, q := range []string{"BEGIN", "SAVEPOINT synonym_failure"} {
			regtsExec(t, p, b, c, q, 0)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY synonym_atomic (casesensitive=true,unknown=1)", dictionaryDiagnostic("22023", `unrecognized synonym parameter: "unknown"`)))
		regtsExec(t, p, b, c, "ROLLBACK TO synonym_failure", 0)
		synonymUnchanged(t, p, c, "synonym_atomic", before)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_atomic','POSTGRES') AS lexemes", "{pgsql}")
		regtsExec(t, p, b, c, "COMMIT", 0)
		// Keep genuine Rows across successful and failed replacement. pq needs a
		// second physical connection while its retained result is unread.
		old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('synonym_atomic','POSTGRES') AS lexemes")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil || meta[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal(meta, err)
		}
		other, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		for _, q := range []string{"BEGIN", "ALTER TEXT SEARCH DICTIONARY synonym_atomic (casesensitive=true)", "COMMIT"} {
			regtsExec(t, p, b, other, q, 0)
		}
		after := synonymSnapshot(t, p, other, "synonym_atomic")
		if after.id != before.id || after.owner != before.owner || after.namespace != before.namespace || after.template != before.template || !reflect.DeepEqual(after.dependencies, before.dependencies) || after.options != "synonyms = 'synonym_sample', casesensitive = 'true'" {
			t.Fatal("partial replacement", before, after)
		}
		synonymMissingResource(t, p, b, other, "ALTER TEXT SEARCH DICTIONARY synonym_atomic (synonyms=synonym_missing)", "synonym_missing")
		synonymUnchanged(t, p, other, "synonym_atomic", after)
		dictionaryResult(t, p, b, other, "SELECT ts_lexize('synonym_atomic','POSTGRES') AS lexemes", nil)
		if !old.Next() {
			t.Fatal("retained row missing", old.Err())
		}
		var value string
		if err := old.Scan(&value); err != nil || value != "{pgsql}" {
			t.Fatal(value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
	})
}
