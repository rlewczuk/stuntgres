package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"unicode/utf8"
)

func TestNotNullStableMetadataAndNoops(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b TEXT,c BIGINT)")
	run(t, e, "INSERT INTO t VALUES(7,'keep',9)")
	run(t, e, "COMMENT ON COLUMN t.c IS 'survivor'")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	storage := e.storage.Relations[ref.Address]
	values := &storage.Versions[0].Values[0]
	held := run(t, e, "SELECT * FROM t")
	run(t, e, "ALTER TABLE t ALTER a SET NOT NULL")
	next := e.catalog.state.MustRelation(ref.Address)
	if next == old || next.Revision() != old.Revision()+1 || next.Address() != old.Address() || next.Owner != old.Owner || !reflect.DeepEqual(next.Attributes, old.Attributes) || !reflect.DeepEqual(next.ColumnMeta.Comments, old.ColumnMeta.Comments) || e.storage.Relations[ref.Address] != storage || &storage.Versions[0].Values[0] != values || next.Columns[0].Nullable || !old.Columns[0].Nullable || len(next.Constraints.NotNull) != 1 || next.Constraints.NotNull[0].OID == 0 || next.Constraints.NotNull[0].Name != "t_a_not_null" || next.Constraints.NotNull[0].Attribute != 1 {
		t.Fatal(old, next)
	}
	if !held.Rows.Schema()[0].Nullable || !reflect.DeepEqual(tuples(held), [][]driver.Value{{int64(7), "keep", int64(9)}}) {
		t.Fatal(held.Rows.Schema())
	}
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT other NOT NULL a", "ALTER TABLE t ALTER b DROP NOT NULL", "ALTER TABLE t DROP CONSTRAINT IF EXISTS absent"} {
		run(t, e, q)
		if e.catalog.state.Get(ref.Address) != next || e.storage.Relations[ref.Address] != storage {
			t.Fatal(q, "no-op mutated state")
		}
	}
	run(t, e, "ALTER TABLE t ALTER c SET NOT NULL")
	run(t, e, "ALTER TABLE t DROP a")
	after := e.catalog.state.MustRelation(ref.Address)
	if len(after.Constraints.NotNull) != 1 || after.Constraints.NotNull[0].OID == 0 || after.Constraints.NotNull[0].Name != "t_c_not_null" || after.Constraints.NotNull[0].Attribute != 3 || after.Columns[1].OriginAttribute != 3 || after.ColumnMeta.Comments[3] != "survivor" {
		t.Fatal(after)
	}
	run(t, e, "ALTER TABLE t DROP CONSTRAINT t_c_not_null")
	run(t, e, "INSERT INTO t VALUES(NULL,NULL)")
}

func TestNotNullSequentialCollisionUsesAllocatedIdentity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE keeper(a INT)")
	run(t, e, "INSERT INTO keeper VALUES(91)")
	ref := roleTableRef(t, e, "keeper")
	state, descriptor, storage := e.catalog.state, e.catalog.state.Get(ref.Address), e.storage.Relations[ref.Address]
	stmt := statement(t, "CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL)")
	if _, err := testDescribe(e, stmt); err != nil || e.catalog.state != state {
		t.Fatal("utility description must be effect-free", err)
	}
	candidate := e.catalog.nextOID
	_, err := testExecute(e, context.Background(), stmt, nil)
	var got *sqlstate.Error
	want := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
		Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`,
		Detail:  fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, t_a_not_null) already exists.", candidate),
		Schema:  "pg_catalog", Table: "pg_constraint", Constraint: "pg_constraint_conrelid_contypid_conname_index",
	}
	if !errors.As(err, &got) || *got != want || e.catalog.nextOID <= candidate {
		t.Fatal(err, got, want, candidate, e.catalog.nextOID)
	}
	failedFrontier := e.catalog.nextOID
	if e.catalog.state != state || e.catalog.state.Get(ref.Address) != descriptor || e.storage.Relations[ref.Address] != storage {
		t.Fatal("failed CREATE published a catalog/storage mutation")
	}
	if _, err := testExecute(e, context.Background(), statement(t, "SELECT * FROM t"), nil); err == nil {
		t.Fatal("failed CREATE published t")
	}
	if !reflect.DeepEqual(tuples(run(t, e, "SELECT * FROM keeper")), [][]driver.Value{{int64(91)}}) {
		t.Fatal("failed CREATE changed keeper")
	}
	run(t, e, "CREATE TABLE t(b INT CONSTRAINT t_a_not_null NOT NULL,a INT NOT NULL)")
	d := e.catalog.state.MustRelation(roleTableRef(t, e, "t").Address)
	if d.Address().ObjectID < failedFrontier {
		t.Fatal("successful CREATE reused failed allocation", failedFrontier, d.Address())
	}
	if len(d.Constraints.NotNull) != 2 || d.Constraints.NotNull[0].OID == 0 || d.Constraints.NotNull[1].OID == 0 || d.Constraints.NotNull[0].OID == d.Constraints.NotNull[1].OID || d.Constraints.NotNull[0].Name != "t_a_not_null" || d.Constraints.NotNull[0].Attribute != 1 || d.Constraints.NotNull[1].Name != "t_a_not_null1" || d.Constraints.NotNull[1].Attribute != 2 {
		t.Fatal(d.Constraints.NotNull)
	}
}

func TestNotNullGeneratedNames(t *testing.T) {
	for _, tc := range []struct{ table, column, want string }{
		{strings.Repeat("t", 40), strings.Repeat("a", 40), strings.Repeat("t", 27) + "_" + strings.Repeat("a", 26) + "_not_null"},
		{strings.Repeat("界", 20), strings.Repeat("文", 20), strings.Repeat("界", 9) + "_" + strings.Repeat("文", 8) + "_not_null"},
	} {
		e := New()
		run(t, e, `CREATE TABLE "`+tc.table+`"("`+tc.column+`" INT NOT NULL)`)
		ref := roleTableRef(t, e, `"`+tc.table+`"`)
		d := e.catalog.state.MustRelation(ref.Address)
		if len(d.Constraints.NotNull) != 1 || d.Constraints.NotNull[0].Name != tc.want || !utf8.ValidString(d.Constraints.NotNull[0].Name) {
			t.Fatal(d.Constraints.NotNull, tc.want)
		}
	}
	e := New()
	table, column := strings.Repeat("t", 40), strings.Repeat("a", 40)
	first := strings.Repeat("t", 27) + "_" + strings.Repeat("a", 26) + "_not_null"
	run(t, e, `CREATE TABLE occupied(x INT CONSTRAINT "`+first+`" NOT NULL)`)
	run(t, e, `CREATE TABLE "`+table+`"("`+column+`" INT NOT NULL)`)
	ref := roleTableRef(t, e, `"`+table+`"`)
	d := e.catalog.state.MustRelation(ref.Address)
	if d.Constraints.NotNull[0].Name != strings.Repeat("t", 26)+"_"+strings.Repeat("a", 26)+"_not_null1" {
		t.Fatal(d.Constraints.NotNull)
	}
	run(t, e, "CREATE SCHEMA s CREATE TABLE occupied(x INT CONSTRAINT t_a_not_null NOT NULL) CREATE TABLE t(a INT NOT NULL)")
	d = e.catalog.state.MustRelation(roleTableRef(t, e, "s.t").Address)
	if d.Constraints.NotNull[0].Name != "t_a_not_null1" {
		t.Fatal(d)
	}
	run(t, e, "CREATE TABLE t(a INT NOT NULL)")
	run(t, e, "CREATE TABLE explicit(a INT CONSTRAINT t_a_not_null NOT NULL)")
	run(t, e, "ALTER TABLE t DROP CONSTRAINT t_a_not_null")
	run(t, e, "DROP TABLE explicit")
	run(t, e, "ALTER TABLE t ALTER a SET NOT NULL")
	run(t, e, "ALTER TABLE t DROP CONSTRAINT t_a_not_null")
}

func TestNotNullProviderInvariants(t *testing.T) {
	for _, mutate := range []func(*catalog.Relation){
		func(d *catalog.Relation) { d.Constraints.NotNull = nil },
		func(d *catalog.Relation) { d.Constraints.NotNull[0].Attribute = 2 },
		func(d *catalog.Relation) { d.Constraints.NotNull[0].Name = "" },
		func(d *catalog.Relation) { d.Columns[0].Nullable = true },
		func(d *catalog.Relation) {
			d.Constraints.NotNull = append(d.Constraints.NotNull, d.Constraints.NotNull[0])
		},
	} {
		e := New()
		run(t, e, "CREATE TABLE t(a INT NOT NULL)")
		ref := roleTableRef(t, e, "t")
		old := e.catalog.state.MustRelation(ref.Address)
		next := *old
		next.ObjectRevision++
		next.Columns = append([]catalog.Column(nil), old.Columns...)
		next.Constraints.NotNull = append([]catalog.NotNullConstraint(nil), old.Constraints.NotNull...)
		mutate(&next)
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil || e.catalog.state.Get(ref.Address) != old {
			t.Fatal(err)
		}
		e.DiscardCatalogStage(stage)
	}
}

func TestNotNullAlterScanFailureAndPreparedUtility(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT)")
	roleExecute(t, e, op, "INSERT INTO t VALUES(NULL,'keep')")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.Get(ref.Address)
	storage := e.storage.Relations[ref.Address]
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
		ast := statement(t, q)
		description, err := e.Describe(op, ast)
		if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 || e.catalog.state.Get(ref.Address) != old {
			t.Fatal(description, err)
		}
		_, err = e.Execute(op, ast, 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}
		if !errors.As(err, &got) || *got != want || e.catalog.state.Get(ref.Address) != old || e.storage.Relations[ref.Address] != storage {
			t.Fatal(got, err)
		}
	}
	// Nonvisible versions must not participate in the committed scan.
	e.mu.Lock()
	storage.Versions[0].Born = e.commitSeq + 1
	e.mu.Unlock()
	roleExecute(t, e, op, "ALTER TABLE t ALTER a SET NOT NULL")
}

func notNullMetadataStage(t *testing.T, e *Engine) (*CatalogStage, ids.ObjectRef, *catalog.Relation) {
	t.Helper()
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	next := *old
	next.ObjectRevision++
	next.Columns = append([]catalog.Column(nil), old.Columns...)
	next.Columns[0].Nullable = false
	next.Constraints.NotNull = []catalog.NotNullConstraint{{Name: "t_a_not_null", Attribute: 1}}
	e.mu.Lock()
	err := e.assignDefinitionOIDsLocked(&next)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	original := e.storage.Relations[ref.Address]
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.alterRelation(context.Background(), ref, &next, original, original, len(original.Versions), original.Generation, original.Ends); err != nil {
		t.Fatal(err)
	}
	return stage, ref, old
}

func TestNotNullSameStorageLengthFence(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT)")
	run(t, e, "INSERT INTO t VALUES(1)")
	stage, ref, old := notNullMetadataStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "INSERT INTO t VALUES(NULL)")
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 || e.catalog.state.Get(ref.Address) != old {
		t.Fatal(changes, err)
	}
	if got := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {nil}}) {
		t.Fatal(got)
	}
}

func TestNotNullProviderPublicationFences(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t DROP CONSTRAINT a_nn", "ALTER TABLE t DROP CONSTRAINT a_nn RESTRICT", "ALTER TABLE t DROP CONSTRAINT a_nn CASCADE"} {
		t.Run(q, func(t *testing.T) { notNullProviderPublicationFences(t, q) })
	}
}

func notNullProviderPublicationFences(t *testing.T, q string) {
	for _, kind := range []string{"cancel", "reset", "insert", "descriptor", "restore", "role", "auth", "end"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			dropping := strings.Contains(q, "DROP")
			if dropping {
				run(t, e, "CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,b INT CONSTRAINT b_nn NOT NULL)")
			} else {
				run(t, e, "CREATE TABLE t(a INT,b INT CONSTRAINT b_nn NOT NULL)")
			}
			run(t, e, "INSERT INTO t VALUES(1,8)")
			ref := roleTableRef(t, e, "t")
			old := e.catalog.state.Get(ref.Address)
			storage := e.storage.Relations[ref.Address]
			ends, generation := storage.Ends, storage.Generation
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := e.catalog.Providers[ids.RelationClassID]
			op, cleanup, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			e.catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
				e.catalog.Providers[ids.RelationClassID] = provider
				switch kind {
				case "cancel":
					cancel()
				case "reset":
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "restore":
					if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE sentinel(v INT);INSERT INTO sentinel VALUES(91)")); err != nil {
						t.Fatal(err)
					}
				case "insert":
					value := sqltype.Null(testUsage(e, sqltype.Int4).Type)
					if dropping {
						value = storage.Versions[0].Values[0]
					}
					e.mu.Lock()
					storage.Versions = append(storage.Versions, rowVersion{Row: 99, Version: 99, Born: e.commitSeq, Values: []sqltype.Value{value, storage.Versions[0].Values[1]}})
					e.mu.Unlock()
				case "descriptor":
					next := *old.(*catalog.Relation)
					next.ObjectRevision++
					next.Comment = "changed"
					stage := e.BeginCatalogStage(e.CatalogRead())
					if err := stage.Replace(&next); err != nil {
						t.Fatal(err)
					}
					prepared, err := e.ValidateCatalogStage(context.Background(), stage)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
						t.Fatal(err)
					}
				case "role":
					run(t, e, "CREATE USER changed")
				case "auth":
					op.session.mu.Lock()
					op.session.authorization.revision++
					op.session.mu.Unlock()
				case "end":
					op.session.RequestEnd(CleanClose)
				}
			}}
			_, err = e.Execute(op, statement(t, q), 0, nil)
			if err == nil {
				t.Fatal("stale alteration published")
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if kind == "reset" || kind == "restore" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			} else if kind != "descriptor" && e.catalog.state.Get(ref.Address) != old {
				t.Fatal("descriptor changed", err)
			}
			if kind != "reset" && kind != "restore" {
				if e.storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != map[bool]int{false: 1, true: 2}[kind == "insert"] {
					t.Fatal("failed alteration changed storage", err)
				}
				if dropping {
					current := e.catalog.state.MustRelation(ref.Address)
					if !reflect.DeepEqual(current.Constraints.NotNull, old.(*catalog.Relation).Constraints.NotNull) || current.Columns[0].Nullable || current.Columns[1].Nullable {
						t.Fatal("failed drop removed enforcement", current)
					}
					notNullDropEnforcement(t, e)
					count := 1
					if kind == "insert" {
						count++
					}
					notNullDropRows(t, e, 1, count)
				}
			}
		})
	}
}

func TestNotNullIntegratedTemporalTypes(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a TIME(3) CONSTRAINT nn NOT NULL,b TIMESTAMP(3))")
	ref := roleTableRef(t, e, "t")
	d := e.catalog.state.MustRelation(ref.Address)
	if d.Columns[0].Usage.Type.ID() != sqltype.Time || d.Columns[1].Usage.Type.ID() != sqltype.Timestamp || d.Columns[0].Usage.Typmod != 3 || d.Columns[1].Usage.Typmod != 3 {
		t.Fatal(d.Columns)
	}
	run(t, e, "INSERT INTO t VALUES('01:02:03.4567','2024-01-02 03:04:05.6789')")
	run(t, e, "ALTER TABLE t ALTER b SET NOT NULL")
	_, err := testExecute(e, context.Background(), statement(t, "INSERT INTO t VALUES(NULL,NULL)"), nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "t", Column: "a"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got, want)
	}
	result := run(t, e, "SELECT * FROM t")
	if result.Count != 1 || result.Rows.Schema()[0].Nullable || result.Rows.Schema()[1].Nullable {
		t.Fatal(result.Count, result.Rows.Schema())
	}
	values := make([]sqltype.Value, 2)
	present, err := result.Rows.Next(context.Background(), values)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	for i, text := range []string{"01:02:03.457", "2024-01-02 03:04:05.679"} {
		output, err := values[i].Type().Output(context.Background(), values[i])
		if err != nil || output != text {
			t.Fatal(i, output, text, err)
		}
	}
	if present, err = result.Rows.Next(context.Background(), values); err != nil || present {
		t.Fatal(present, err)
	}
	result.Rows.Close()
	run(t, e, "ALTER TABLE t DROP CONSTRAINT nn")
	run(t, e, "ALTER TABLE t DROP CONSTRAINT t_b_not_null")
	run(t, e, "INSERT INTO t VALUES(NULL,NULL)")
	if result := run(t, e, "SELECT * FROM t"); result.Count != 2 || !result.Rows.Schema()[0].Nullable || !result.Rows.Schema()[1].Nullable {
		t.Fatal(result.Count, result.Rows.Schema())
	}
}
