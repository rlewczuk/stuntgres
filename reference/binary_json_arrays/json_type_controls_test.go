package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The same fresh-reader assertions run after native Restore and after executing
// the actual emitted SQL on PostgreSQL. Declaration specs are independent of
// the retained cursors and metadata is checked even when the selection is empty.
func jsonTypeFreshStoredReaders(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, check fixture.Step, scalarColumns, keyColumns []fixture.JSONTypeSourceColumn) {
	t.Helper()
	jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(check, b.PG, scalarColumns...))
	empty := fixture.Select("SELECT "+strings.Join(check.Columns, ",")+" FROM jt_stored WHERE FALSE ORDER BY id", check.Columns, check.Types, [][]any{})
	jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(empty, b.PG, scalarColumns...))
	for _, view := range []struct {
		name string
		rows [][]any
	}{
		{"jt_key_view", [][]any{{"b", int64(1)}, {"a", int64(2)}}},
		{"jt_bkey_view", [][]any{{"a", int64(1)}, {"zz", int64(2)}}},
	} {
		read := fixture.Select("SELECT k,n FROM "+view.name+" ORDER BY n", []string{"k", "n"}, []string{"TEXT", "INT8"}, view.rows)
		jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(read, b.PG, keyColumns...))
		empty := fixture.Select("SELECT k,n FROM "+view.name+" WHERE FALSE ORDER BY n", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{})
		jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(empty, b.PG, keyColumns...))
	}
}

func jsonTypeFreshDefaultReaders(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	// These eight jt_stored columns all have nullable declarations, including
	// selected scalar defaults and stored generated kind/length results.
	columns := []fixture.JSONTypeSourceColumn{
		{Index: 0, Nullable: true}, {Index: 1, Nullable: true}, {Index: 2, Nullable: true},
		{Index: 3, Nullable: true}, {Index: 4, Nullable: true}, {Index: 5, Nullable: true},
		{Index: 6, Nullable: true}, {Index: 7, Nullable: true},
	}
	read := fixture.Select("SELECT dk,bk,dl,bl,jk,bt,jl,bn FROM jt_stored WHERE id=7", []string{"dk", "bk", "dl", "bl", "jk", "bt", "jl", "bn"}, []string{"TEXT", "TEXT", "INT4", "INT4", "TEXT", "TEXT", "INT4", "INT4"}, [][]any{{"null", "boolean", int64(0), int64(1), "array", "array", int64(1), int64(2)}})
	jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(read, b.PG, columns...))
	empty := fixture.Select("SELECT dk,bk,dl,bl,jk,bt,jl,bn FROM jt_stored WHERE FALSE", read.Columns, read.Types, [][]any{})
	jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(empty, b.PG, columns...))
}

func jsonTypeControlPhase(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // There is no PostgreSQL native-control API.
	oldScalar, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer oldScalar.Close()
	// Every selected jt_stored column is declared nullable, including stored
	// generated/default columns; these are column-derived output descriptors.
	scalarColumns := make([]fixture.JSONTypeSourceColumn, len(check.Columns))
	for i := range scalarColumns {
		scalarColumns[i] = fixture.JSONTypeSourceColumn{Index: i, Nullable: true}
	}
	fixture.JSONTypeMetadataCheck(t, oldScalar, false, check.Columns, check.Types, scalarColumns...)
	check = fixture.JSONTypeWithMetadata(check, b.PG, scalarColumns...)
	oldKeys, err := c.QueryContext(p.Ctx, "SELECT k,n FROM jt_key_view ORDER BY n")
	if err != nil {
		t.Fatal(err)
	}
	defer oldKeys.Close()
	oldMeta, err := oldKeys.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	// View source columns retain their native declaration metadata, whereas
	// the direct key SRF/ordinality projections checked elsewhere are computed.
	keyColumns := []fixture.JSONTypeSourceColumn{{Index: 0, Nullable: true}, {Index: 1, Nullable: true}}
	fixture.JSONTypeMetadataCheck(t, oldKeys, false, []string{"k", "n"}, []string{"TEXT", "INT8"}, keyColumns...)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jt_replacement(v int)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jt_replacement VALUES(99)", 1))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.JSONTypeMetadataCheck(t, oldScalar, false, check.Columns, check.Types, scalarColumns...)
	fixture.JSONTypeMetadataCheck(t, oldKeys, false, []string{"k", "n"}, []string{"TEXT", "INT8"}, keyColumns...)
	fixture.ScalarRows(t, false, oldScalar, check.Columns, check.Types, check.Rows, false)
	fixture.ScalarRows(t, false, oldKeys, []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"b", int64(1)}, {"a", int64(2)}}, false)
	fixture.ScalarAssertDescription(t, oldMeta, []string{"k", "n"}, []string{"TEXT", "INT8"})
	jsonTypeFreshStoredReaders(t, p, b, c, check, scalarColumns, keyColumns)
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("Restore changed symbolic dump")
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore=%v", err)
	}
	bad := fixture.ControlsReferenceDiagnostic("22023", "cannot get array length of a non-array")
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(`CREATE TABLE jt_failed(v int DEFAULT json_array_length('{}')); INSERT INTO jt_failed DEFAULT VALUES;`)), &bad)
	jsonTypeBoth(t, p, b, c, check)
	after.Reset()
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("failed replacement changed committed state")
	}
	// Replaying emitted SQL is a real PostgreSQL SQL witness, not dump-text matching.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		replay := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		jsonTypeFreshStoredReaders(t, p, replay, p.Postgres, check, scalarColumns, keyColumns)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO jt_stored(id) VALUES(7)", 1))
		jsonTypeFreshDefaultReaders(t, p, replay, p.Postgres)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jt_stored(id) VALUES(7)", 1))
	jsonTypeFreshDefaultReaders(t, p, b, c)
}

func TestReferenceJSONTypeControls(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonTypeStored(t, p, b, c)
		jsonTypeControlPhase(t, p, b, c, check)
	})
}
