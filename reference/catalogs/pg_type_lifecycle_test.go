package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTypeConsumeNames(t *testing.T, rows *sql.Rows, want ...string) {
	t.Helper()
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained names got=%v want=%v", got, want)
	}
}
func pgTypeTransactionalIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, create, name string) {
	t.Helper()
	tx, err := b.DB.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(create, 0))
	var id, array int64
	if err := tx.QueryRowContext(p.Ctx, "SELECT $1::regtype::oid,$2::regtype::oid", name, name+"[]").Scan(&id, &array); err != nil {
		t.Fatal(err)
	}
	q := "SELECT oid FROM pg_type WHERE oid=$1::oid OR oid=$2::oid ORDER BY oid"
	want := [][]any{{id}, {array}}
	if array < id {
		want = [][]any{{array}, {id}}
	}
	step := fixture.Select(q, []string{"oid"}, []string{"OID"}, want, id, array)
	fixture.PgDescriptionExecutorRows(t, p, b, tx, step)
	step.Rows = nil
	fixture.PgDescriptionRows(t, p, b, step)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.PgDescriptionRows(t, p, b, step)
}

func pgTypeTransitions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	createDomain := "CREATE DOMAIN pt18_transaction AS integer"
	pgTypeTransactionalIdentity(t, p, b, createDomain, "pt18_transaction")
	// CREATE TEMP TABLE is already supported transaction-private DDL: both
	// actual rowtype and canonical array disappear atomically on rollback.
	pgTypeTransactionalIdentity(t, p, b, "CREATE TEMP TABLE pt18_tx_table(n integer)", "pg_temp.pt18_tx_table")
	q := "SELECT typname FROM pg_type WHERE oid=$1::oid"
	for _, sql := range []string{"CREATE SCHEMA pt18_move", "CREATE TABLE pt18_before(n integer)", "CREATE ROLE pt18_type_owner"} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(sql, 0))
	}
	defer func() {
		for _, sql := range []string{"DROP SCHEMA pt18_move CASCADE", "DROP ROLE pt18_type_owner"} {
			if _, err := b.DB.ExecContext(p.Ctx, sql); err != nil {
				t.Error(err)
			}
		}
	}()
	rowID, arrayID := pgTypeID(t, p, b, "pt18_before"), pgTypeID(t, p, b, "pt18_before[]")
	retained, err := b.DB.QueryContext(p.Ctx, q, rowID)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	held := fixture.ViewHold(t, p, b, q)
	for _, sql := range []string{"ALTER TABLE pt18_before RENAME TO pt18_after", "ALTER TABLE pt18_after SET SCHEMA pt18_move", "ALTER TABLE pt18_move.pt18_after OWNER TO pt18_type_owner", "ALTER TABLE pt18_move.pt18_after ADD COLUMN label text"} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(sql, 0))
	}
	if pgTypeID(t, p, b, "pt18_move.pt18_after") != rowID || pgTypeID(t, p, b, "pt18_move.pt18_after[]") != arrayID {
		t.Fatal("identity changed across relation edits")
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"typname"}, []string{"NAME"}, [][]any{{"pt18_after"}}, rowID))
	link := "SELECT t.typowner=c.relowner AS owner_link,a.typowner=t.typowner AS array_owner,t.typnamespace=c.relnamespace AS namespace_link,a.typnamespace=t.typnamespace AS array_namespace,t.typrelid=c.oid AS row_relation,a.typrelid=0 AS array_relation FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pt18_move.pt18_after'::regclass::oid"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(link, []string{"owner_link", "array_owner", "namespace_link", "array_namespace", "row_relation", "array_relation"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}}))
	pgTypeConsumeNames(t, retained, "pt18_before")
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE pt18_move.pt18_after", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"typname"}, []string{"NAME"}, nil, rowID))
	fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid FROM pg_type WHERE oid=$1 OR oid=$2", []string{"oid"}, []string{"OID"}, nil, rowID, arrayID))
}
func pgTypeTemporary(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	b.DB.SetMaxIdleConns(0)
	owner, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	observer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE TEMP TABLE pt18_temporary(n integer)", 0))
	var id, array int64
	if err := owner.QueryRowContext(p.Ctx, "SELECT 'pg_temp.pt18_temporary'::regtype::oid,'pg_temp.pt18_temporary[]'::regtype::oid").Scan(&id, &array); err != nil {
		t.Fatal(err)
	}
	q := "SELECT oid FROM pg_type WHERE oid=$1 OR oid=$2 ORDER BY oid"
	want := [][]any{{id}, {array}}
	if array < id {
		want = [][]any{{array}, {id}}
	}
	fixture.PgDescriptionExecutorRows(t, p, b, observer, fixture.Select(q, []string{"oid"}, []string{"OID"}, want, id, array))
	// Closing the client transport alone does not synchronize PostgreSQL's
	// asynchronous backend/TEMP cleanup. Reuse the existing physical-exit
	// barrier before requiring the shared catalog inventory to be empty.
	session := fixture.TempReferenceSession{T: t, P: p, DB: b.DB, C: owner, Peer: observer, Record: b.Recorder, Postgres: b.PG}
	session.Retire()
	fixture.PgDescriptionExecutorRows(t, p, b, observer, fixture.Select(q, []string{"oid"}, []string{"OID"}, nil, id, array))
}
func pgTypeLogicalControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, q := range []string{"CREATE TYPE pt18_saved_enum AS ENUM('calm','busy')", "CREATE TABLE pt18_saved(n integer,m pt18_saved_enum)"} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pt18_saved VALUES(1,'calm'),(2,NULL)", 2))
	id := pgTypeID(t, p, b, "pt18_saved_enum")
	q := "SELECT typname FROM pg_type WHERE oid=$1::oid"
	held := fixture.ViewHold(t, p, b, q)
	retained, err := b.DB.QueryContext(p.Ctx, q, id)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	saved, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		fixture.EnumReferenceRunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT n,m FROM pt18_saved ORDER BY n", []string{"n", "m"}, []string{"INT4", ""}, [][]any{{int64(1), "calm"}, {int64(2), nil}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT t.typtype,t.typcategory,a.typelem=t.oid AS element_link,a.typrelid=0 AS array_relation,a.oid='pt18_saved_enum[]'::regtype::oid AS array_identity FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='pt18_saved_enum'::regtype::oid", []string{"typtype", "typcategory", "element_link", "array_relation", "array_identity"}, []string{"CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"e", "E", true, true, true}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT t.typtype,t.typcategory,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,a.typrelid=0 AS array_relation FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pt18_saved'::regclass::oid", []string{"typtype", "typcategory", "relation_link", "element_link", "array_relation"}, []string{"CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"c", "C", true, true, true}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT atttypid='pt18_saved_enum'::regtype::oid AS attribute_type FROM pg_attribute WHERE attrelid='pt18_saved'::regclass::oid AND attnum=2", []string{"attribute_type"}, []string{"BOOL"}, [][]any{{true}}))
	}
	check()
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "pt18_saved_enum") || !strings.Contains(dump.String(), "INSERT INTO") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_type") {
			t.Fatal("not a logical user dump", dump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"typname"}, []string{"NAME"}, nil, id))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		// Restore resolves logical names in a fresh catalog authority. A valid
		// receiving identity may reuse the same numeric OID after Reset; prove
		// publication and current links, not an allocator sequence.
		next := pgTypeID(t, p, b, "pt18_saved_enum")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"typname"}, []string{"NAME"}, [][]any{{"pt18_saved_enum"}}, next))
		check()
	}
	pgTypeConsumeNames(t, retained, "pt18_saved_enum")
	if len(saved) != 1 || saved[0].Name() != "typname" || saved[0].DatabaseTypeName() != "NAME" {
		t.Fatal("saved description changed", saved)
	}
}
func pgTypeCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	} // Go context cancellation is not a PostgreSQL SQL error.
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := b.DB.QueryContext(ctx, "SELECT * FROM pg_type")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled query published rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("ordinary cancellation gained SQLSTATE", err)
	}
}
func TestReferencePGTypeLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pt18_type_owner"})
	fixture.SchemaReferenceCleanup(t, "pt18_move")
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		t.Run("transitions", func(t *testing.T) { pgTypeTransitions(t, p, b) })
		t.Run("temporary_physical_lifetime", func(t *testing.T) { pgTypeTemporary(t, p, b) })
		t.Run("logical_controls", func(t *testing.T) { pgTypeLogicalControls(t, p, b) })
		t.Run("cancellation", func(t *testing.T) { pgTypeCanceled(t, p, b) })
	})
}
