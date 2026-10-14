package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAttributeRankRepairGraphs(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE USER pa18_graph_owner", "CREATE SCHEMA pa18_graph AUTHORIZATION pa18_graph_owner")
		func() {
			owner, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			defer owner.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			pgClassExec(t, p, b, owner,
				"SET SESSION AUTHORIZATION pa18_graph_owner",
				"CREATE TYPE pa18_graph.source AS(id integer,a integer[][],b integer[])",
				"CREATE TABLE pa18_graph.root OF pa18_graph.source",
				"ALTER TABLE pa18_graph.root ALTER id SET DEFAULT 11",
				"ALTER TABLE pa18_graph.root ALTER a SET DEFAULT '{{1,2}}'",
				"ALTER TABLE pa18_graph.root ALTER b SET DEFAULT '{3,4}'",
				"CREATE TABLE pa18_graph.other(id integer DEFAULT 11)",
				"CREATE TABLE pa18_graph.early(note text DEFAULT 'early') INHERITS(pa18_graph.root,pa18_graph.other)",
				"CREATE TABLE pa18_graph.grand() INHERITS(pa18_graph.early)",
				"ALTER TYPE pa18_graph.source ALTER ATTRIBUTE a TYPE integer[][][] CASCADE",
				"ALTER TYPE pa18_graph.source ALTER ATTRIBUTE b TYPE integer[][] CASCADE",
				"CREATE TABLE pa18_graph.late(note text DEFAULT 'late') INHERITS(pa18_graph.root,pa18_graph.other)",
				"CREATE TABLE pa18_graph.typed_late OF pa18_graph.source")
			for i, name := range []string{"root", "other", "early", "grand", "late"} {
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO pa18_graph."+name+"(id) VALUES($1)", 1, i+1))
			}
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO pa18_graph.typed_late VALUES(6,'{{9,10}}','{11,12}')", 1))
		}()
		check := func(backend fixture.Backend) {
			var ownerID int64
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname='pa18_graph_owner'").Scan(&ownerID); err != nil || ownerID <= 0 {
				t.Fatal("own owner identity", ownerID, err)
			}
			rootID := pgAttributeID(t, p, backend.DB, "pa18_graph.root")
			var namespaceID int64
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT relnamespace::bigint FROM pg_class WHERE oid=$1", rootID).Scan(&namespaceID); err != nil || namespaceID <= 0 {
				t.Fatal("own relation namespace identity", namespaceID, err)
			}
			for _, relation := range []struct {
				name                                string
				a, b, count                         int64
				note, localNote, defaults, children bool
			}{
				{"source", 3, 2, 0, false, false, false, false},
				{"root", 3, 2, 0, false, false, true, true},
				{"other", 0, 0, 0, false, false, true, true},
				{"early", 3, 2, 2, true, true, true, true},
				{"grand", 3, 2, 1, true, false, true, false},
				{"late", 0, 0, 2, true, true, true, false},
				{"typed_late", 0, 0, 0, false, false, false, false},
			} {
				id := pgAttributeID(t, p, backend.DB, "pa18_graph."+relation.name)
				want := [][]any{pgAttributeRow(id, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0)}
				if relation.name != "other" {
					want = append(want, pgAttributeRow(id, "a", 1007, -1, 2, -1, relation.a, false, "i", "x", false, 0), pgAttributeRow(id, "b", 1007, -1, 3, -1, relation.b, false, "i", "x", false, 0))
				}
				for i, row := range want {
					row[12] = relation.defaults
					if relation.count > 0 {
						count := relation.count
						if i > 0 && count == 2 {
							count = 1
						}
						row[17], row[18] = false, count
					}
				}
				if relation.note {
					note := pgAttributeRow(id, "note", 25, -1, 4, -1, 0, false, "i", "x", false, 100)
					note[12] = true
					if !relation.localNote {
						note[17], note[18] = false, int64(1)
					}
					want = append(want, note)
				}
				pgAttributeRead(t, p, backend, backend.DB, id, " AND attnum>0", want)
				pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT oid::bigint,relname,relnamespace::bigint,relowner::bigint,relacl,relhassubclass FROM pg_class WHERE oid=$1", []string{"oid", "relname", "relnamespace", "relowner", "relacl", "relhassubclass"}, []string{"INT8", "NAME", "INT8", "INT8", "_ACLITEM", "BOOL"}, [][]any{{id, relation.name, namespaceID, ownerID, nil, relation.children}}, id))
			}
			// Native does not expose pg_namespace. Its admitted relation rows
			// above retain the exact owner/ACL and common qualified namespace
			// relationship; retain PostgreSQL's independent schema witness too.
			if backend.PG {
				pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT nspowner::bigint,nspacl FROM pg_namespace WHERE nspname='pa18_graph'", []string{"nspowner", "nspacl"}, []string{"INT8", "_ACLITEM"}, [][]any{{ownerID, nil}}))
			}
			for _, relation := range []struct {
				name string
				ids  []int64
			}{
				{"root", []int64{1, 3, 4, 5}}, {"early", []int64{3, 4}}, {"grand", []int64{4}}, {"late", []int64{5}},
			} {
				rows := make([][]any, len(relation.ids))
				for i, id := range relation.ids {
					rows[i] = []any{id, "{{1,2}}", "{3,4}"}
				}
				pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,a,b FROM pa18_graph."+relation.name+" ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "_INT4", "_INT4"}, rows))
			}
			pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id FROM pa18_graph.other ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
			pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,a,b FROM pa18_graph.typed_late", []string{"id", "a", "b"}, []string{"INT4", "_INT4", "_INT4"}, [][]any{{int64(6), "{{9,10}}", "{11,12}"}}))
			pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,note FROM pa18_graph.early ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "early"}, {int64(4), "early"}}))
			pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,note FROM pa18_graph.late", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(5), "late"}}))
		}
		check(b)
		if b.PG {
			return
		}
		dump := fixture.ViewDump(t, p)
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "CREATE USER pa18_graph_owner"); err != nil {
				t.Fatal("replay owner prerequisite", err)
			}
		}
		for range 2 {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal("graph restore", err)
			}
			check(b)
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("actual owner graph PG replay", err)
				}
				pg := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
				check(pg)
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA pa18_graph CASCADE"); err != nil {
					t.Fatal("graph replay cleanup", err)
				}
			}
			dump = fixture.ViewDump(t, p)
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP ROLE pa18_graph_owner"); err != nil {
				t.Fatal("graph replay owner cleanup", err)
			}
		}
	})
}
