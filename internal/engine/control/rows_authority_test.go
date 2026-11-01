package control

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func dumpRowsAuthorityFixture(t *testing.T) (*dumpScript, dump.Event) {
	t.Helper()
	source, _, _, _ := fdwUnitSource()
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	if integer == nil {
		t.Fatal("missing genuine integer codec")
	}
	value, err := integer.Input(context.Background(), "7", -1)
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{
		ObjectAddress:   fdwUnitAddress(ids.RelationClassID, 94001),
		ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID),
		ObjectName:      "saved_rows",
		Owner:           source.Bootstrap,
		RelationKind:    catalog.TableRelation,
		Columns:        []catalog.Column{{Name: "i", Usage: integer.Usage()}},
		Attributes:     []int32{1},
	}
	source.State.SetDescriptor(relation.Address(), relation)
	captured := table.DumpRelation{
		Owner: "postgres", Schema: "public", Name: relation.Name(),
		Descriptor: relation, Columns: relation.Columns, Attributes: relation.Attributes,
		Frontier: 2,
		Versions: []storage.RowVersion{
			{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}},
			{Row: 2, Version: 2, Born: 2, Values: []sqltype.Value{value}},
			{Row: 3, Version: 3, Born: 3, Values: []sqltype.Value{value}},
		},
		Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 1, End: 2}}},
	}
	event := dump.Event{OID: relation.Address().ObjectID, Phase: dump.Rows}
	return &dumpScript{Capture: &Capture{
		OriginalCatalog: source.CatalogView,
		Tables:          []table.DumpRelation{captured},
		TableIndices:    map[ids.OID]int{event.OID: 0},
	}}, event
}

func TestDumpRowsAuthority(t *testing.T) {
	script, event := dumpRowsAuthorityFixture(t)
	before := script.Tables[0]
	descriptor := *before.Descriptor
	var out bytes.Buffer
	w := dumpWriter{ctx: context.Background(), dst: &out, currentOwner: "prior_owner"}
	if err := emitDumpRows(w.ctx, script, &w, event); err != nil {
		t.Fatal(err)
	}
	const want = "SET SESSION AUTHORIZATION DEFAULT;\nINSERT INTO \"public\".\"saved_rows\" VALUES (7);\nSET SESSION AUTHORIZATION 'postgres';\n"
	if out.String() != want || w.currentOwner != before.Owner {
		t.Fatalf("captured visible row replay = %q owner=%q, want %q", out.String(), w.currentOwner, want)
	}
	if !reflect.DeepEqual(before, script.Tables[0]) || !reflect.DeepEqual(descriptor, *before.Descriptor) {
		t.Fatal("row replay changed captured data, table identity or owner/ACL metadata")
	}
	const dependent = "CREATE VIEW public.dependent AS SELECT 1;\n"
	if err := emitDumpStatement(script, &w, 94002, dump.Statement{Owner: before.Owner, SQL: dependent}); err != nil {
		t.Fatal(err)
	}
	if out.String() != want+dependent || w.currentOwner != before.Owner {
		t.Fatal("row replay leaked authenticated authority into a dependent event", out.String(), w.currentOwner)
	}
}

func TestDumpRowsAuthorityEmptyAndCanceled(t *testing.T) {
	for _, mode := range []string{"empty", "invisible", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			script, event := dumpRowsAuthorityFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "empty":
				script.Tables[0].Versions = nil
			case "invisible":
				script.Tables[0].Frontier = 0
			case "canceled":
				cancel()
			}
			var out bytes.Buffer
			w := dumpWriter{ctx: ctx, dst: &out, currentOwner: "prior_owner"}
			err := emitDumpRows(ctx, script, &w, event)
			if mode == "canceled" && !errors.Is(err, context.Canceled) || mode != "canceled" && err != nil {
				t.Fatal("unexpected row replay error", err)
			}
			if out.Len() != 0 || w.currentOwner != "prior_owner" {
				t.Fatal("empty or canceled row replay changed output/authority", out.String(), w.currentOwner)
			}
		})
	}
}

type dumpRowsWriteFunc func([]byte) (int, error)

func (f dumpRowsWriteFunc) Write(p []byte) (int, error) { return f(p) }

func TestDumpRowsAuthorityFailure(t *testing.T) {
	for _, failure := range []string{"SET SESSION AUTHORIZATION DEFAULT", "INSERT INTO", "SET SESSION AUTHORIZATION 'postgres'"} {
		t.Run(failure, func(t *testing.T) {
			script, event := dumpRowsAuthorityFixture(t)
			sentinel := errors.New("row replay writer failure")
			var out bytes.Buffer
			w := dumpWriter{ctx: context.Background(), currentOwner: "prior_owner"}
			w.dst = dumpRowsWriteFunc(func(p []byte) (int, error) {
				if strings.HasPrefix(string(p), failure) {
					return 0, sentinel
				}
				return out.Write(p)
			})
			if err := emitDumpRows(w.ctx, script, &w, event); !errors.Is(err, sentinel) {
				t.Fatal("row replay lost writer failure", err)
			}
			wantOwner := ""
			if failure == "SET SESSION AUTHORIZATION DEFAULT" {
				wantOwner = "prior_owner"
			}
			if w.currentOwner != wantOwner || strings.Contains(out.String(), "SET SESSION AUTHORIZATION 'postgres'") {
				t.Fatal("failed row replay claimed an unwritten owner restoration", out.String(), w.currentOwner)
			}
		})
	}
}

func TestDumpRowsAuthorityCastWindow(t *testing.T) {
	script, event := dumpRowsAuthorityFixture(t)
	const create = "CREATE CAST (pg_catalog.int4 AS pg_catalog.text) WITH INOUT;\n"
	const drop = "DROP CAST (pg_catalog.int4 AS pg_catalog.text);\n"
	script.castWindows = map[castConsumer][]castReplay{{Event: event}: {{SQL: create, Drop: drop}}}
	script.eventOrder = []dump.Event{event, {OID: event.OID, Phase: dump.Defaults}}
	var out bytes.Buffer
	w := dumpWriter{ctx: context.Background(), dst: &out, currentOwner: "prior_owner"}
	if err := emitDumpEvents(w.ctx, script, &w); err != nil {
		t.Fatal(err)
	}
	const owner = "SET SESSION AUTHORIZATION 'postgres';\n"
	want := owner + create + "INSERT INTO \"public\".\"saved_rows\" VALUES (7);\n" + drop
	if out.String() != want || w.currentOwner != script.Tables[0].Owner {
		t.Fatal("row replay crossed its event cast window or leaked authority", out.String(), w.currentOwner)
	}
}

func TestDumpRowsAuthorityCanceledWrite(t *testing.T) {
	script, event := dumpRowsAuthorityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	w := dumpWriter{ctx: ctx, currentOwner: "prior_owner"}
	w.dst = dumpRowsWriteFunc(func(p []byte) (int, error) {
		n, err := out.Write(p)
		if bytes.HasPrefix(p, []byte("INSERT INTO")) {
			cancel()
		}
		return n, err
	})
	if err := emitDumpRows(ctx, script, &w, event); !errors.Is(err, context.Canceled) {
		t.Fatal("row write lost cancellation", err)
	}
	const want = "SET SESSION AUTHORIZATION DEFAULT;\nINSERT INTO \"public\".\"saved_rows\" VALUES (7);\n"
	if out.String() != want || w.currentOwner != "" {
		t.Fatal("canceled row write continued output or claimed owner restoration", out.String(), w.currentOwner)
	}
}
