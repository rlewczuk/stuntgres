package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func segIntervalMetadataSnapshot(cols []*sql.ColumnType) []any {
	var out []any
	for _, col := range cols {
		n, nk := col.Nullable()
		l, lk := col.Length()
		p, s, dk := col.DecimalSize()
		out = append(out, []any{col.Name(), col.DatabaseTypeName(), col.ScanType(), n, nk, l, lk, p, s, dk})
	}
	return out
}
func segIntervalPending(t *testing.T, p *fixture.Pair, c fixture.Executor) (*sql.Rows, []*sql.ColumnType, []any) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, segIntervalTableQuery())
	if err != nil {
		t.Fatal(err)
	}
	cols, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatal(err)
	}
	segIntervalMetadata(t, false, cols, append([]string{"id"}, segIntervalNames...), append([]string{"INT4"}, segIntervalTypes...), false)
	return rows, cols, segIntervalMetadataSnapshot(cols)
}
func segIntervalRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		values := make([]any, len(want[0]))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained predicates %#v want %#v", got, want)
	}
}
func segIntervalInvalidOwner(t *testing.T, script string) string {
	t.Helper()
	start, end, data := fixture.CatalogExtensionOwnerDirective(t, script, "seg", true)
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil {
		t.Fatal(err)
	}
	changed := false
	generated := 0
	for _, member := range members {
		if member["declaration"] == nil {
			generated++
			if string(member["kind"]) != `"type"` || string(member["extension"]) != `"seg"` || string(member["owner"]) != `""` {
				t.Fatal("generated seg companion metadata", member)
			}
			continue
		}
		var slot int
		if err := json.Unmarshal(member["declaration"], &slot); err != nil {
			t.Fatal(err)
		}
		if slot == 7 {
			if string(member["name"]) != `"seg_same"` || changed {
				t.Fatal("actual selected equality declaration", member)
			}
			member["name"] = json.RawMessage(`"seg_left"`)
			changed = true
		}
	}
	if !changed || generated != 1 {
		t.Fatal("missing selected equality declaration or generated seg companion", generated)
	}
	var err error
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return script[:start] + script[end+1:] + "-- stuntgres-extension-owners-v3 " + base64.StdEncoding.EncodeToString(data) + "\n"
}

func segIntervalNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	extension, consumer := strings.Index(text, "CREATE EXTENSION"), strings.Index(text, "CREATE TABLE")
	if extension < 0 || consumer < extension || strings.Contains(text, "CREATE FUNCTION") || strings.Contains(text, "CREATE OPERATOR") {
		t.Fatal("owned member dump ordering", text)
	}
	rows, cols, metadata := segIntervalPending(t, p, c)
	defer rows.Close()
	q := "SELECT $1::seg = $2::seg AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	typedCheck := segIntervalSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{false}}, false, "1.0 .. 1.00", "1.0")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, typedCheck)
	// Builtin text parameters can rebind their explicit seg casts in a new
	// catalog lifetime; nominal seg parameters deliberately cannot.
	textQuery := "SELECT $1::text::seg = $2::text::seg AS v"
	textStmt, err := c.PrepareContext(p.Ctx, textQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer textStmt.Close()
	textHeld := fixture.HeldStatement{Executor: c, Statement: textStmt, SQL: textQuery}
	textCheck := segIntervalSelect(b, textQuery, []string{"v"}, []string{"BOOL"}, [][]any{{false}}, false, "1.0 .. 1.00", "1.0")
	fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, textCheck)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='seg'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	segIntervalStoredChecks(t, p, b, c, false)
	stale := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type"), "1.0 .. 1.00", "1.0")
	stale.SelectRows, stale.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, stale)
	for _, input := range []struct{ a, b, want any }{
		{"1", "1", true},
		{"1.0 .. 1.00", "1.0", false},
		{nil, "1", nil},
		{"1", "1", true},
	} {
		fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, segIntervalSelect(b, textQuery, []string{"v"}, []string{"BOOL"}, [][]any{{input.want}}, false, input.a, input.b))
	}
	segIntervalBoth(t, p, b, c, typedCheck)
	segIntervalRetained(t, rows, segIntervalSeedRows())
	if !reflect.DeepEqual(segIntervalMetadataSnapshot(cols), metadata) {
		t.Fatal("replacement mutated old BOOL metadata")
	}
	const identities = "SELECT 'public.seg'::regtype::oid::bigint,'public.seg_same(public.seg,public.seg)'::regprocedure::oid::bigint,'public.=(public.seg,public.seg)'::regoperator::oid::bigint"
	var original [3]int64
	if err := c.QueryRowContext(p.Ctx, identities).Scan(&original[0], &original[1], &original[2]); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	owner, err := fresh.Memory.Conn(fresh.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	segIntervalRetainCollisions(t, fresh, owner, original)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var remapped [3]int64
	if err := fresh.Memory.QueryRowContext(fresh.Ctx, identities).Scan(&remapped[0], &remapped[1], &remapped[2]); err != nil {
		t.Fatal(err)
	}
	for i, id := range remapped {
		if id <= 0 || id == original[i] {
			t.Fatal("identities not reminted", original, remapped)
		}
	}
	freshBackend := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
	segIntervalStoredChecks(t, fresh, freshBackend, fresh.Memory, false)
	for _, s := range segIntervalCatalogChecks("public") {
		fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, s)
	}
	old, oldCols, oldMetadata := segIntervalPending(t, fresh, fresh.Memory)
	defer old.Close()
	invalidScalar := "CREATE EXTENSION seg;CREATE TABLE staged(v boolean DEFAULT ('1'::seg && '1'::seg));INSERT INTO staged VALUES (true);INSERT INTO staged VALUES (seg_overlap('1'::seg,'1'::seg));"
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, strings.NewReader(invalidScalar)); err == nil || err.Error() != "stuntgres: invalid restore scalar" {
		t.Fatal("restore scalar whitelist", err)
	} else {
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary restore error gained SQLSTATE", err)
		}
	}
	badSidecar := strings.Replace(text, "stuntgres-domain-null-v1", "stuntgres-domain-null-v999", 1)
	if badSidecar == text {
		t.Fatal("missing exact seg sidecar")
	}
	for _, bad := range []string{segIntervalInvalidOwner(t, text), badSidecar, text + "INSERT INTO seg_interval_data VALUES (99,'ABC','1');"} {
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid replacement accepted")
		}
		segIntervalStoredChecks(t, fresh, freshBackend, fresh.Memory, false)
	}
	ctx, cancel := context.WithCancel(fresh.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, fresh.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled replacement", err)
	}
	segIntervalRetained(t, old, segIntervalSeedRows())
	if !reflect.DeepEqual(segIntervalMetadataSnapshot(oldCols), oldMetadata) {
		t.Fatal("failure mutated captured metadata")
	}
	fixture.RunStep(t, fresh.Ctx, owner, fresh.MemoryRecord, fixture.Select("SELECT v FROM pg_temp.seg_interval_allocator_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	var again bytes.Buffer
	if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("nondeterministic reminted dump", again.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, text); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		// Stock seg_in sees canonical 1.0, losing the source's hidden upper digits.
		segIntervalStoredChecks(t, p, pg, p.Postgres, true)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceSegIntervalControls(t *testing.T) {
	t.Run("stored", func(t *testing.T) {
		segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			segIntervalStoredSetup(t, p, b, c)
			segIntervalStoredChecks(t, p, b, c, false)
			if !b.PG {
				segIntervalNativeControls(t, p, b, c)
			}
		})
	})
	t.Run("exceptional_exact", func(t *testing.T) {
		segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE seg_interval_edges(id integer,a seg,b seg)", 0))
			insert := "INSERT INTO seg_interval_edges VALUES (1,'0(+-)-1','-2 .. 0'),(2,'0(+-)-1','0(+-)-1'),(3,'3.4e38(+-)3.4e38','1'),(4,'-0.0','0.0'),(5,'1.0 .. 1.00','1.0')"
			run(fixture.Exec(insert, 5))
			check := segIntervalSelect(b, "SELECT id,"+segIntervalProjection("a", "b")+" FROM seg_interval_edges ORDER BY id", append([]string{"id"}, segIntervalNames...), append([]string{"INT4"}, segIntervalTypes...), [][]any{
				{int64(1), false, true, true, true, false, true, false, true}, {int64(2), true, true, true, true, true, false, true, true}, {int64(3), false, false, false, false, false, true, true, false}, {int64(4), false, false, true, true, true, true, true, true}, {int64(5), false, false, true, true, false, true, true, true},
			}, false)
			segIntervalBoth(t, p, b, c, check)
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				segIntervalBoth(t, p, b, c, check)
			}
			run(fixture.Exec("DROP TABLE seg_interval_edges", 0))
			run(fixture.Exec("DROP EXTENSION seg", 0))
			run(fixture.Exec("CREATE EXTENSION seg", 0))
			run(fixture.Exec("CREATE TABLE seg_interval_edges(id integer,a seg,b seg)", 0))
			run(fixture.Exec(insert, 5))
			segIntervalBoth(t, p, b, c, check)
		})
	})
}
