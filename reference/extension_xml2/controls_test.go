package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func xml2IDs(t *testing.T, p *fixture.Pair, c *sql.Conn) []int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid::bigint FROM pg_proc p JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='xml2' AND p.proname IN ("+xml2Names+") ORDER BY p.proname,p.pronargs")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	seen := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil || id <= 0 || seen[id] {
			t.Fatal("invalid procedure identity", id, err)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if rows.Err() != nil || len(ids) != 10 {
		t.Fatal("selected identities", ids, rows.Err())
	}
	return ids
}

func xml2Retained(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id", "v", "s", "n", "b"}) {
		t.Fatal("retained columns", columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 5 {
		t.Fatal("retained metadata", meta, err)
	}
	for i, want := range []string{"INT4", "BOOL", "TEXT", "FLOAT4", "BOOL"} {
		if meta[i].DatabaseTypeName() != want {
			t.Fatalf("retained type %d=%s want=%s", i, meta[i].DatabaseTypeName(), want)
		}
	}
	if !rows.Next() {
		t.Fatal("retained row missing", rows.Err())
	}
	values := make([]any, 5)
	dest := make([]any, 5)
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i := range values {
		values[i] = fixture.Normalize(values[i])
	}
	if !reflect.DeepEqual(values, []any{int64(1), true, "12", float64(12), true}) {
		t.Fatalf("retained values=%#v", values)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained tail", rows.Err())
	}
}

func TestReferenceXML2Controls(t *testing.T) {
	t.Run("Public", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"xml2_probe_user", "xml2_probe_denied", "xml2_provider_owner"})
		xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			xml2Install(t, p, b, c)
			xml2Consumers(t, p, b, c)
			xml2Inventory(t, p, b, c)
			xml2Saved(t, p, b, c)
			xml2Guard(t, p, b, c, "public", "xpath_string")
			xml2ACLSetup(t, p, b, c)
			xml2ControlRoundTrip(t, p, b, c)
		})
	})
	t.Run("CustomSchema", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"xml2_control_reader", "xml2_control_owner"})
		fixture.SchemaReferenceCleanup(t, xml2ControlSchema)
		xml2Case(t, xml2SchemaControls)
	})
}

func xml2ControlRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT id,v,s,n,b FROM xml2_values ORDER BY id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	want := fixture.Select(q, []string{"id", "v", "s", "n", "b"}, []string{"INT4", "BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{int64(1), true, "12", float64(12), true}})
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	if b.PG {
		return // Public native controls have no PostgreSQL API equivalent.
	}
	old, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	extAt := strings.Index(dump, "CREATE EXTENSION")
	if extAt < 0 || strings.Index(dump, "CREATE TABLE") <= extAt || strings.Index(dump, "CREATE VIEW") <= extAt {
		t.Fatal("provider must precede stored consumers", dump)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	xml2Retained(t, old)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='xml2'", []string{"extname"}, []string{"NAME"}, nil))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	xml2Inventory(t, p, b, c)
	xml2Saved(t, p, b, c)
	xml2Guard(t, p, b, c, "public", "xpath_string")
	xml2ACLCheck(t, p, b, c)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	ids := xml2IDs(t, p, c)
	readFailure := errors.New("xml2 replacement read failure")
	for _, mode := range []string{"sql", "precanceled", "reader", "late-cancel"} {
		ctx, cancel := context.WithCancel(p.Ctx)
		var input io.Reader = strings.NewReader(dump + "\nINVALID SQL;")
		var ordinary error
		switch mode {
		case "precanceled":
			cancel()
			input, ordinary = strings.NewReader(dump), context.Canceled
		case "reader":
			input = io.MultiReader(strings.NewReader(dump), &fixture.ReferenceRangeErrorReader{Err: readFailure})
			ordinary = readFailure
		case "late-cancel":
			input = io.MultiReader(strings.NewReader(dump), &xml2CancelReader{Reader: strings.NewReader("\n"), cancel: cancel})
			ordinary = context.Canceled
		}
		err := stuntgres.Restore(ctx, p.Native, input)
		cancel()
		if err == nil || ordinary != nil && !errors.Is(err, ordinary) {
			t.Fatalf("rejected %s replacement error=%v want=%v", mode, err, ordinary)
		}
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("rejected restore changed committed dump")
		}
		if after := xml2IDs(t, p, c); !reflect.DeepEqual(ids, after) {
			t.Fatalf("rejected restore changed identities: %v -> %v", ids, after)
		}
		xml2Saved(t, p, b, c)
		xml2Guard(t, p, b, c, "public", "xpath_string")
		xml2ACLCheck(t, p, b, c)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("xml2_destination")
	if err != nil {
		t.Fatal(err)
	}
	control := sql.OpenDB(connector)
	defer control.Close()
	pool, recorder := fixture.RecordedNativePool(t, connector)
	dst, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	destination := fixture.Backend{Name: "destination", DB: pool, Recorder: recorder}
	// Roles are shared-object prerequisites, not objects serialized by Dump.
	for _, role := range []string{"xml2_probe_user", "xml2_probe_denied", "xml2_provider_owner"} {
		fixture.RunStep(t, p.Ctx, dst, recorder, fixture.Exec("CREATE ROLE "+role, 0))
	}
	if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	xml2Inventory(t, p, destination, dst)
	xml2Saved(t, p, destination, dst)
	xml2Guard(t, p, destination, dst, "public", "xpath_string")
	xml2ACLCheck(t, p, destination, dst)
	fixture.RunStep(t, p.Ctx, dst, recorder, fixture.Exec("INSERT INTO xml2_saved(id) VALUES(2)", 1))
	fixture.RunStep(t, p.Ctx, dst, recorder, fixture.Select("SELECT v,s,n,b FROM xml2_saved WHERE id=2", []string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{true, "12", float64(12), true}}))
	xml2RenamedReplay(t, p, b, c, control, destination, dst)
}

// Cancellation is triggered by the reader only after the complete valid dump
// has been consumed; it is not a pre-canceled replacement or a timing race.
type xml2CancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *xml2CancelReader) Read(p []byte) (int, error) {
	r.cancel()
	return r.Reader.Read(p)
}
