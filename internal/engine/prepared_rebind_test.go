package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func preparedRebindValid(t *testing.T, e *Engine, s *session.Session, cell *PreparedAnalysis) bool {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	path, err := EngineSequenceEnvironmentLocked(e, auth)
	if err != nil {
		t.Fatal(err)
	}
	return EnginePreparedAnalysisValidLocked(e, auth, cell, path)
}

func TestPreparedRebindValidity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE b.t(v INT)", "SET search_path TO a,b"} {
		sessionSQL(t, s, q)
	}
	_, d := sequencePreparedDescription(t, e, s, "SELECT l.v,r.v FROM t AS l CROSS JOIN t AS r")
	cell := DescriptionAnalysis(&d)
	if cell == nil || len(PreparedAnalysisSources(cell)) != 2 {
		t.Fatal("occurrences were collapsed", cell)
	}
	var source ids.ObjectRef
	for _, occurrence := range PreparedAnalysisSources(cell) {
		if source == (ids.ObjectRef{}) {
			source = occurrence.Ref
		}
		if occurrence.Ref != source || occurrence.Query == nil {
			t.Fatal("wrong occurrence identity", occurrence)
		}
	}
	relations := 0
	for _, dependency := range PreparedAnalysisDependencies(cell) {
		if dependency.Ref.Address.ClassID == ids.RelationClassID {
			relations++
			if dependency.Ref != source {
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
	SetPreparedAnalysisDomain(&foreign, &ids.Domain{})
	if preparedRebindValid(t, e, s, &foreign) {
		t.Fatal("foreign owner was accepted")
	}
	private := *cell
	SetPreparedAnalysisPrivate(&private, &session.Journal{})
	if preparedRebindValid(t, e, s, &private) {
		t.Fatal("different private catalog certified")
	}
	sessionSQL(t, s, "CREATE SCHEMA other")
	if preparedRebindValid(t, e, s, cell) {
		t.Fatal("equal-path namespace callback ignored")
	}
	_, pathDescription := sequencePreparedDescription(t, e, s, "SELECT v FROM t")
	sessionSQL(t, s, "SET search_path TO b,a")
	if preparedRebindValid(t, e, s, DescriptionAnalysis(&pathDescription)) {
		t.Fatal("effective path reorder ignored")
	}
	_, dependencyDescription := sequencePreparedDescription(t, e, s, "SELECT v FROM b.t")
	sessionSQL(t, s, "ALTER TABLE b.t ADD unused INT")
	if preparedRebindValid(t, e, s, DescriptionAnalysis(&dependencyDescription)) {
		t.Fatal("dependency definition change ignored")
	}
	_, removed := sequencePreparedDescription(t, e, s, "SELECT v FROM b.t")
	sessionSQL(t, s, "DROP TABLE b.t")
	if preparedRebindValid(t, e, s, DescriptionAnalysis(&removed)) {
		t.Fatal("missing dependency accepted")
	}
	sessionSQL(t, s, "CREATE TABLE b.t(v INT)")
	if preparedRebindValid(t, e, s, DescriptionAnalysis(&removed)) {
		t.Fatal("replacement dependency reused old identity")
	}
	_, native := sequencePreparedDescription(t, e, s, "SELECT v FROM b.t")
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if preparedRebindValid(t, e, s, DescriptionAnalysis(&native)) {
		t.Fatal("replacement catalog kept validity")
	}
	t.Run("PrivateRollback", func(t *testing.T) {
		privateEngine := New()
		privateSession := sessionStart(t, privateEngine, nil)
		for _, q := range []string{"BEGIN", "SAVEPOINT before_temp", "CREATE TEMP TABLE rollback_source(v INT)"} {
			sessionSQL(t, privateSession, q)
		}
		_, retained := sequencePreparedDescription(t, privateEngine, privateSession, "SELECT v FROM pg_temp.rollback_source")
		if !preparedRebindValid(t, privateEngine, privateSession, DescriptionAnalysis(&retained)) {
			t.Fatal("private analysis was not initially valid")
		}
		sessionSQL(t, privateSession, "ROLLBACK TO before_temp")
		if preparedRebindValid(t, privateEngine, privateSession, DescriptionAnalysis(&retained)) {
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
		if DescriptionAnalysis(&description) == nil || len(PreparedAnalysisSources(DescriptionAnalysis(&description))) != 1 || DescriptionAnalysis(&qualified) == nil || len(PreparedAnalysisSources(DescriptionAnalysis(&qualified))) != 1 {
			t.Fatal("missing initial prepared namespace identities")
		}
		bound := engine.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: late.Address, Name: "t"})[0]
		for _, source := range PreparedAnalysisSources(DescriptionAnalysis(&description)) {
			if source.Ref.Address != bound {
				t.Fatal("inaccessible early schema participated in initial resolution")
			}
		}
		initial := DescriptionAnalysis(&description)
		set(early, usage)
		if preparedRebindValid(t, engine, caller, initial) {
			t.Fatal("real schema-USAGE callback retained validity")
		}
		result, err := sequencePreparedExecute(engine, caller, ast, &description)
		if err != nil {
			t.Fatal(err)
		}
		result.Rows.Close()
		bound = engine.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: early.Address, Name: "t"})[0]
		if DescriptionAnalysis(&description) == initial {
			t.Fatal("schema-USAGE invalidation did not publish a new identity cell")
		}
		for _, source := range PreparedAnalysisSources(DescriptionAnalysis(&description)) {
			if source.Ref.Address != bound {
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
		if DescriptionAnalysis(&qualified) != DescriptionAnalysis(&original) || !reflect.DeepEqual(qualified.Parameters, original.Parameters) || !reflect.DeepEqual(qualified.Results, original.Results) {
			t.Fatal("schema denial published candidate identities or signatures")
		}
		set(late, usage)
		result, err = sequencePreparedExecute(engine, caller, qualifiedAST, &qualified)
		if err != nil {
			t.Fatal("same qualified handle did not recover schema-USAGE", err)
		}
		result.Rows.Close()
		if DescriptionAnalysis(&qualified) == DescriptionAnalysis(&original) {
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
		for _, dependency := range PreparedAnalysisDependencies(DescriptionAnalysis(&description)) {
			seen[dependency.Ref.Address] = true
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
		bind := func(input sqltype.InputEnvironment, expected *StatementDescription) analyze.Binding {
			t.Helper()
			engine.Mu.Lock()
			defer engine.Mu.Unlock()
			auth, err := engine.AuthorizationLocked(op)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := Describe(op.Ctx, engine.AnalysisHost(op), ast, auth, ids.ObjectRef{}, input, expected, nil)
			if err != nil {
				t.Fatal(err)
			}
			return bound
		}
		first := bind(sqltype.InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)}, nil)
		if len(first.Prepared.Temporals) != 4 {
			t.Fatal("missing logical and genuine temporal captures", first.Prepared.Temporals)
		}
		description := *NewDescription(DescriptionFields{Parameters: first.Parameters, Results: first.Query.Root.Schema(), Temporals: first.Prepared.Temporals, CatalogOwner: &engine.Catalog.Owner, CatalogEpoch: engine.CatalogRead().Epoch()})
		frozen := append([]analyze.TemporalConstant(nil), first.Prepared.Temporals...)
		run(t, engine, "ALTER TYPE mood RENAME VALUE 'q' TO 'end'")
		SetDescriptionRefreshTemporals(&description, true)
		fresh := bind(sqltype.InputEnvironment{TransactionStart: time.Date(2004, 8, 8, 12, 13, 14, 0, time.UTC)}, &description)
		if len(fresh.Prepared.Temporals) != len(frozen) {
			t.Fatal("candidate lost captures", fresh.Prepared.Temporals)
		}
		for i, old := range frozen {
			same := reflect.DeepEqual(old, fresh.Prepared.Temporals[i])
			if same == analyze.RefreshableTemporal(old) {
				t.Fatal("incorrect temporal/logical candidate boundary", i, old, fresh.Prepared.Temporals[i])
			}
		}
		if !reflect.DeepEqual(description.Temporals, frozen) {
			t.Fatal("candidate mutated original logical/temporal group")
		}
	})
	t.Run("ValuesRetainsInitialCaptures", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		ast, description := sequencePreparedDescription(t, engine, session, "VALUES(TIMESTAMP 'now',('{now}'::timestamp[])[1])")
		if len(description.Temporals) != 2 || DescriptionAnalysis(&description) == nil {
			t.Fatal("missing initial VALUES analysis", description.Temporals)
		}
		original := description
		for range 2 {
			result, err := sequencePreparedExecute(engine, session, ast, &description)
			if err != nil {
				t.Fatal(err)
			}
			result.Rows.Close()
			if DescriptionAnalysis(&description) != DescriptionAnalysis(&original) || len(description.Temporals) != 2 || &description.Temporals[0] != &original.Temporals[0] {
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
	if len(d.Temporals) < 2 || d.Sequences == nil || DescriptionRegtypes(&d) == nil || DescriptionAnalysis(&d) == nil {
		t.Fatal("missing genuine capture groups")
	}
	before := d
	sequenceBefore, regtypeBefore := *d.Sequences, *DescriptionRegtypes(&d)
	assertUnchanged := func() {
		t.Helper()
		if DescriptionAnalysis(&d) != DescriptionAnalysis(&before) || len(d.Temporals) != len(before.Temporals) || &d.Temporals[0] != &before.Temporals[0] || d.Sequences != before.Sequences || DescriptionRegtypes(&d) != DescriptionRegtypes(&before) || !reflect.DeepEqual(*d.Sequences, sequenceBefore) || !reflect.DeepEqual(*DescriptionRegtypes(&d), regtypeBefore) || !reflect.DeepEqual(d.Parameters, before.Parameters) || !reflect.DeepEqual(d.Results, before.Results) {
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
	if DescriptionAnalysis(&d) == DescriptionAnalysis(&before) || len(d.Temporals) != len(before.Temporals) || &d.Temporals[0] == &before.Temporals[0] || d.Sequences != before.Sequences || DescriptionRegtypes(&d) != DescriptionRegtypes(&before) {
		t.Fatal("complete group did not publish together")
	}
	if err := CheckStatementSignature(before, d); err != nil {
		t.Fatal(err)
	}
	stable := d
	result, err = sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if DescriptionAnalysis(&d) != DescriptionAnalysis(&stable) || len(d.Temporals) != len(stable.Temporals) || &d.Temporals[0] != &stable.Temporals[0] || d.Sequences != stable.Sequences || DescriptionRegtypes(&d) != DescriptionRegtypes(&stable) {
		t.Fatal("valid use allocated retained state")
	}

	// Successful analysis publication precedes importing execution values.
	parameterAST, parameterDescription := sequencePreparedDescription(t, e, s, "SELECT $1::int,TIMESTAMP 'now' FROM t")
	old := DescriptionAnalysis(&parameterDescription)
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
	_, err = scope.Execute(parameterAST, 1, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return importFailure
	})
	err = scope.Finish(err)
	if !errors.Is(err, importFailure) || DescriptionAnalysis(&parameterDescription) == old {
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
		sequence, regtype := *description.Sequences, *DescriptionRegtypes(&description)
		if err := Reset(context.Background(), engine); err != nil {
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
		if !errors.Is(err, ids.ErrCatalogReplaced) || DescriptionAnalysis(&description) != DescriptionAnalysis(&old) || len(description.Temporals) != len(old.Temporals) || &description.Temporals[0] != &old.Temporals[0] || !reflect.DeepEqual(*description.Sequences, sequence) || !reflect.DeepEqual(*DescriptionRegtypes(&description), regtype) {
			t.Fatal("native lifetime rejection published capture/identity state", err)
		}
	})
}
