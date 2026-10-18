package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func preparedRebindValid(t *testing.T, e *Engine, s *Session, cell *preparedAnalysis) bool {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		t.Fatal(err)
	}
	return e.preparedAnalysisValidLocked(auth, cell, path)
}

func TestPreparedRebindValidity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE b.t(v INT)", "SET search_path TO a,b"} {
		sessionSQL(t, s, q)
	}
	_, d := sequencePreparedDescription(t, e, s, "SELECT l.v,r.v FROM t AS l CROSS JOIN t AS r")
	cell := d.analysis
	if cell == nil || len(cell.sources) != 2 {
		t.Fatal("occurrences were collapsed", cell)
	}
	var source ids.ObjectRef
	for _, occurrence := range cell.sources {
		if source == (ids.ObjectRef{}) {
			source = occurrence.ref
		}
		if occurrence.ref != source || occurrence.query == nil {
			t.Fatal("wrong occurrence identity", occurrence)
		}
	}
	relations := 0
	for _, dependency := range cell.dependencies {
		if dependency.ref.Address.ClassID == RelationClassID {
			relations++
			if dependency.ref != source {
				t.Fatal("wrong dependency identity", dependency)
			}
		}
	}
	if relations != 1 || !preparedRebindValid(t, e, s, cell) {
		t.Fatal("dependency dedup or initial validity")
	}
	for _, q := range []string{"CREATE TABLE a.t(v INT)", "INSERT INTO b.t VALUES(7)", "CREATE TABLE b.unrelated(v INT)", "SELECT set_config('search_path','a,b,b,missing',false)"} {
		sessionSQL(t, s, q)
		if !preparedRebindValid(t, e, s, cell) {
			t.Fatal("unrelated mutation/equivalent path invalidated", q)
		}
	}
	foreign := *cell
	foreign.domain = &Domain{}
	if preparedRebindValid(t, e, s, &foreign) {
		t.Fatal("foreign owner was accepted")
	}
	private := *cell
	private.private = &catalogChanges{}
	if preparedRebindValid(t, e, s, &private) {
		t.Fatal("different private catalog certified")
	}
	sessionSQL(t, s, "CREATE SCHEMA other")
	if preparedRebindValid(t, e, s, cell) {
		t.Fatal("equal-path namespace callback ignored")
	}
	_, pathDescription := sequencePreparedDescription(t, e, s, "SELECT v FROM t")
	sessionSQL(t, s, "SET search_path TO b,a")
	if preparedRebindValid(t, e, s, pathDescription.analysis) {
		t.Fatal("effective path reorder ignored")
	}
	_, dependencyDescription := sequencePreparedDescription(t, e, s, "SELECT v FROM b.t")
	sessionSQL(t, s, "ALTER TABLE b.t ADD unused INT")
	if preparedRebindValid(t, e, s, dependencyDescription.analysis) {
		t.Fatal("dependency definition change ignored")
	}
	_, removed := sequencePreparedDescription(t, e, s, "SELECT v FROM b.t")
	sessionSQL(t, s, "DROP TABLE b.t")
	if preparedRebindValid(t, e, s, removed.analysis) {
		t.Fatal("missing dependency accepted")
	}
	sessionSQL(t, s, "CREATE TABLE b.t(v INT)")
	if preparedRebindValid(t, e, s, removed.analysis) {
		t.Fatal("replacement dependency reused old identity")
	}
	_, native := sequencePreparedDescription(t, e, s, "SELECT v FROM b.t")
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if preparedRebindValid(t, e, s, native.analysis) {
		t.Fatal("replacement catalog kept validity")
	}
	t.Run("PrivateRollback", func(t *testing.T) {
		privateEngine := New()
		privateSession := sessionStart(t, privateEngine, nil)
		for _, q := range []string{"BEGIN", "SAVEPOINT before_temp", "CREATE TEMP TABLE rollback_source(v INT)"} {
			sessionSQL(t, privateSession, q)
		}
		_, retained := sequencePreparedDescription(t, privateEngine, privateSession, "SELECT v FROM pg_temp.rollback_source")
		if !preparedRebindValid(t, privateEngine, privateSession, retained.analysis) {
			t.Fatal("private analysis was not initially valid")
		}
		sessionSQL(t, privateSession, "ROLLBACK TO before_temp")
		if preparedRebindValid(t, privateEngine, privateSession, retained.analysis) {
			t.Fatal("rolled-back private metadata retained validity")
		}
		sessionSQL(t, privateSession, "ROLLBACK")
	})
	t.Run("RealNamespaceACLInvalidation", func(t *testing.T) {
		engine := New()
		caller := sessionStart(t, engine, nil)
		for _, query := range []string{"CREATE USER acl_reader", "CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE a.t(v INT)", "CREATE TABLE b.t(v INT)", "GRANT SELECT ON a.t TO acl_reader", "GRANT SELECT ON b.t TO acl_reader"} {
			sessionSQL(t, caller, query)
		}
		admin, _ := engine.Roles().Lookup("postgres")
		reader, _ := engine.Roles().Lookup("acl_reader")
		usage := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.UsagePrivilege}}}
		early, late := schemaRef(t, engine, "a"), schemaRef(t, engine, "b")
		set := func(ref ids.ObjectRef, acl authorization.ACLValue) {
			t.Helper()
			if err := engine.SetObjectACL(context.Background(), ref, acl); err != nil {
				t.Fatal(err)
			}
		}
		set(early, authorization.ACLValue{Explicit: true})
		set(late, usage)
		sessionSQL(t, caller, "SET search_path TO a,b")
		sessionSQL(t, caller, "SET SESSION AUTHORIZATION acl_reader")
		ast, description := sequencePreparedDescription(t, engine, caller, "SELECT v FROM t")
		qualifiedAST, qualified := sequencePreparedDescription(t, engine, caller, "SELECT v FROM b.t")
		if description.analysis == nil || len(description.analysis.sources) != 1 || qualified.analysis == nil || len(qualified.analysis.sources) != 1 {
			t.Fatal("missing initial prepared namespace identities")
		}
		bound := engine.CatalogRead().Lookup(ids.NameKey{ClassID: RelationClassID, Namespace: late.Address, Name: "t"})[0]
		for _, source := range description.analysis.sources {
			if source.ref.Address != bound {
				t.Fatal("inaccessible early schema participated in initial resolution")
			}
		}
		initial := description.analysis
		set(early, usage)
		if preparedRebindValid(t, engine, caller, initial) {
			t.Fatal("real schema-USAGE callback retained validity")
		}
		result, err := sequencePreparedExecute(engine, caller, ast, &description)
		if err != nil {
			t.Fatal(err)
		}
		result.Rows.Close()
		bound = engine.CatalogRead().Lookup(ids.NameKey{ClassID: RelationClassID, Namespace: early.Address, Name: "t"})[0]
		if description.analysis == initial {
			t.Fatal("schema-USAGE invalidation did not publish a new identity cell")
		}
		for _, source := range description.analysis.sources {
			if source.ref.Address != bound {
				t.Fatal("schema-USAGE grant did not rebind to early identity")
			}
		}
		original := qualified
		set(late, authorization.ACLValue{Explicit: true})
		result, err = sequencePreparedExecute(engine, caller, qualifiedAST, &qualified)
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("schema denial exposed rows")
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for schema b" || diagnostic.Position != "15" {
			t.Fatal("schema denial lost ordinary source-positioned authority diagnostic", err)
		}
		if qualified.analysis != original.analysis || !reflect.DeepEqual(qualified.Parameters, original.Parameters) || !reflect.DeepEqual(qualified.Results, original.Results) {
			t.Fatal("schema denial published candidate identities or signatures")
		}
		set(late, usage)
		result, err = sequencePreparedExecute(engine, caller, qualifiedAST, &qualified)
		if err != nil {
			t.Fatal("same qualified handle did not recover schema-USAGE", err)
		}
		result.Rows.Close()
		if qualified.analysis == original.analysis {
			t.Fatal("schema-USAGE repair kept invalid analysis")
		}
	})
}

func TestPreparedRebindDependencyManifest(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(v INT)", "CREATE TABLE dep(v INT)", "CREATE VIEW inner_view AS SELECT v FROM dep", "CREATE VIEW outer_view AS SELECT v FROM inner_view"} {
		sessionSQL(t, s, q)
	}
	for _, query := range []string{
		"WITH unused AS (SELECT v FROM dep) SELECT (SELECT v FROM t) FROM outer_view",
		"INSERT INTO t(v) SELECT v FROM outer_view",
		"UPDATE t SET v=src.v FROM outer_view AS src",
		"DELETE FROM t USING outer_view AS src WHERE t.v=src.v",
		"MERGE INTO t USING outer_view AS src ON t.v=src.v WHEN NOT MATCHED THEN INSERT(v) VALUES(src.v)",
	} {
		_, description := sequencePreparedDescription(t, e, s, query)
		seen := make(map[ids.ObjectAddress]bool)
		for _, dependency := range description.analysis.dependencies {
			seen[dependency.ref.Address] = true
		}
		for _, name := range []string{"t", "dep", "inner_view", "outer_view"} {
			ref := catalogRef(t, e, e.CatalogRead(), name)
			if !seen[ref.Address] {
				t.Fatal("missing analyzed dependency", query, name)
			}
		}
	}
}

func TestPreparedRebindPublication(t *testing.T) {
	t.Run("TemporalCandidatePreservesLogicalCompounds", func(t *testing.T) {
		engine := New()
		for _, query := range []string{"CREATE TYPE mood AS ENUM('q')", "CREATE TYPE mixed_capture AS(m mood,ts TIMESTAMP)"} {
			run(t, engine, query)
		}
		op, done, err := testOperation(engine, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		ast := statement(t, "SELECT 'q'::mood,'(q,now)'::mixed_capture,'{now}'::timestamp[],TIMESTAMP 'now'")
		bind := func(input sqltype.InputEnvironment, expected *StatementDescription) binding {
			t.Helper()
			engine.mu.Lock()
			defer engine.mu.Unlock()
			auth, err := engine.authorizationLocked(op)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := engine.describe(op.ctx, ast, auth, ids.ObjectRef{}, input, op, expected, nil)
			if err != nil {
				t.Fatal(err)
			}
			return bound
		}
		first := bind(sqltype.InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)}, nil)
		if len(first.temporals) != 4 {
			t.Fatal("missing logical and genuine temporal captures", first.temporals)
		}
		description := StatementDescription{Parameters: first.parameters, Results: first.query.Root.Schema(), temporals: first.temporals, catalogOwner: engine, catalogEpoch: engine.CatalogRead().Epoch()}
		frozen := append([]temporalConstant(nil), first.temporals...)
		run(t, engine, "ALTER TYPE mood RENAME VALUE 'q' TO 'end'")
		description.refreshTemporals = true
		fresh := bind(sqltype.InputEnvironment{TransactionStart: time.Date(2004, 8, 8, 12, 13, 14, 0, time.UTC)}, &description)
		if len(fresh.temporals) != len(frozen) {
			t.Fatal("candidate lost captures", fresh.temporals)
		}
		for i, old := range frozen {
			same := reflect.DeepEqual(old, fresh.temporals[i])
			if same == refreshableTemporalCapture(old) {
				t.Fatal("incorrect temporal/logical candidate boundary", i, old, fresh.temporals[i])
			}
		}
		if !reflect.DeepEqual(description.temporals, frozen) {
			t.Fatal("candidate mutated original logical/temporal group")
		}
	})
	t.Run("ValuesRetainsInitialCaptures", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		ast, description := sequencePreparedDescription(t, engine, session, "VALUES(TIMESTAMP 'now',('{now}'::timestamp[])[1])")
		if len(description.temporals) != 2 || description.analysis == nil {
			t.Fatal("missing initial VALUES analysis", description.temporals)
		}
		original := description
		for range 2 {
			result, err := sequencePreparedExecute(engine, session, ast, &description)
			if err != nil {
				t.Fatal(err)
			}
			result.Rows.Close()
			if description.analysis != original.analysis || len(description.temporals) != 2 || &description.temporals[0] != &original.temporals[0] {
				t.Fatal("unchanged VALUES use replaced retained capture state")
			}
		}
	})
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(v INT)", "CREATE SEQUENCE q"} {
		sessionSQL(t, s, q)
	}
	ast, d := sequencePreparedDescription(t, e, s, "SELECT t.*,TIMESTAMP 'now','{now}'::timestamp[],nextval('q'),'integer'::regtype FROM t")
	if len(d.temporals) < 2 || d.sequences == nil || d.regtypes == nil || d.analysis == nil {
		t.Fatal("missing genuine capture groups")
	}
	before := d
	sequenceBefore, regtypeBefore := *d.sequences, *d.regtypes
	assertUnchanged := func() {
		t.Helper()
		if d.analysis != before.analysis || len(d.temporals) != len(before.temporals) || &d.temporals[0] != &before.temporals[0] || d.sequences != before.sequences || d.regtypes != before.regtypes || !reflect.DeepEqual(*d.sequences, sequenceBefore) || !reflect.DeepEqual(*d.regtypes, regtypeBefore) || !reflect.DeepEqual(d.Parameters, before.Parameters) || !reflect.DeepEqual(d.Results, before.Results) {
			t.Fatal("failed analysis published retained state")
		}
	}
	sessionSQL(t, s, "ALTER TABLE t ADD other INT")
	result, err := sequencePreparedExecute(e, s, ast, &d)
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("shape failure exposed rows")
	}
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "0A000" {
		t.Fatal("missing fixed-shape error", err)
	}
	assertUnchanged()
	sessionSQL(t, s, "DROP TABLE t")
	_, err = sequencePreparedExecute(e, s, ast, &d)
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42P01" {
		t.Fatal("missing analysis error", err)
	}
	assertUnchanged()
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	result, err = sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if d.analysis == before.analysis || len(d.temporals) != len(before.temporals) || &d.temporals[0] == &before.temporals[0] || d.sequences != before.sequences || d.regtypes != before.regtypes {
		t.Fatal("complete group did not publish together")
	}
	if err := checkStatementSignature(before, d); err != nil {
		t.Fatal(err)
	}
	stable := d
	result, err = sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if d.analysis != stable.analysis || len(d.temporals) != len(stable.temporals) || &d.temporals[0] != &stable.temporals[0] || d.sequences != stable.sequences || d.regtypes != stable.regtypes {
		t.Fatal("valid use allocated retained state")
	}

	// Successful analysis publication precedes importing execution values.
	parameterAST, parameterDescription := sequencePreparedDescription(t, e, s, "SELECT $1::int,TIMESTAMP 'now' FROM t")
	old := parameterDescription.analysis
	sessionSQL(t, s, "ALTER TABLE t ADD unused INT")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &parameterDescription
	importFailure := errors.New("unit importer failure")
	_, err = scope.Execute(parameterAST, 1, func(context.Context, ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return importFailure
	})
	err = scope.Finish(err)
	if !errors.Is(err, importFailure) || parameterDescription.analysis == old {
		t.Fatal("analysis publication incorrectly depended on importer success", err)
	}
	t.Run("NativeProvenancePublishesNothing", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		for _, q := range []string{"CREATE TABLE source(v INT)", "CREATE SEQUENCE source_q"} {
			sessionSQL(t, session, q)
		}
		ast, description := sequencePreparedDescription(t, engine, session, "SELECT TIMESTAMP 'now','{now}'::timestamp[],nextval('source_q'),'integer'::regtype FROM source")
		old := description
		sequence, regtype := *description.sequences, *description.regtypes
		if err := engine.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"CREATE TABLE source(v INT)", "CREATE SEQUENCE source_q"} {
			sessionSQL(t, session, q)
		}
		result, err := sequencePreparedExecute(engine, session, ast, &description)
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("retired provenance exposed rows")
		}
		if !errors.Is(err, ErrCatalogReplaced) || description.analysis != old.analysis || len(description.temporals) != len(old.temporals) || &description.temporals[0] != &old.temporals[0] || !reflect.DeepEqual(*description.sequences, sequence) || !reflect.DeepEqual(*description.regtypes, regtype) {
			t.Fatal("native lifetime rejection published capture/identity state", err)
		}
	})
}
