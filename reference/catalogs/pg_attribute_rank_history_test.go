package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAttributeControlsRankHistory(t *testing.T) {
	for _, kind := range []string{"partition", "inherited"} {
		t.Run(kind, func(t *testing.T) { pgAttributeRankHistory(t, kind) })
	}
}

func TestReferencePGAttributeControlsPartitionRank(t *testing.T) {
	pgAttributeRankHistory(t, "partition_minimal")
}

func pgAttributeRankHistory(t *testing.T, kind string) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		partition := kind != "inherited"
		defaults := kind != "partition_minimal"
		var cleanup string
		if partition {
			parentColumns := "id integer NOT NULL DEFAULT 7,matrix integer[][] DEFAULT '{{1,2},{3,4}}'"
			childColumns := "id integer NOT NULL DEFAULT 7,matrix integer[][][] DEFAULT '{{1,2},{3,4}}'"
			if !defaults {
				parentColumns = "id integer,matrix integer[][]"
				childColumns = "id integer,matrix integer[][][]"
			}
			pgClassExec(t, p, b, b.DB,
				"CREATE TABLE pa18_hist_parent("+parentColumns+") PARTITION BY RANGE(id)",
				"CREATE TABLE pa18_hist_attached("+childColumns+")",
				"ALTER TABLE pa18_hist_parent ATTACH PARTITION pa18_hist_attached FOR VALUES FROM(0) TO(10)",
				"CREATE TABLE pa18_hist_copied PARTITION OF pa18_hist_parent FOR VALUES FROM(10) TO(20)")
			insert := "INSERT INTO pa18_hist_parent(id) VALUES(2),(12)"
			if !defaults {
				insert = "INSERT INTO pa18_hist_parent(id,matrix) VALUES(2,'{{1,2},{3,4}}'),(12,'{{1,2},{3,4}}')"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(insert, 2))
			cleanup = "DROP TABLE pa18_hist_parent CASCADE"
		} else {
			pgClassExec(t, p, b, b.DB,
				"CREATE TYPE pa18_hist_type AS(id integer,a integer[][])",
				"CREATE TABLE pa18_hist_old OF pa18_hist_type",
				"ALTER TABLE pa18_hist_old ALTER id SET DEFAULT 11",
				"ALTER TABLE pa18_hist_old ALTER a SET DEFAULT '{{1,2}}'",
				"CREATE TABLE pa18_hist_early(note text DEFAULT 'early') INHERITS(pa18_hist_old)",
				"CREATE TABLE pa18_hist_grand() INHERITS(pa18_hist_early)",
				"ALTER TYPE pa18_hist_type ALTER ATTRIBUTE a TYPE integer[][][] CASCADE",
				"CREATE TABLE pa18_hist_late(note text DEFAULT 'late') INHERITS(pa18_hist_old)",
				"CREATE TABLE pa18_hist_typed_late OF pa18_hist_type")
			for _, insert := range []string{
				"INSERT INTO pa18_hist_old(id) VALUES(1)",
				"INSERT INTO pa18_hist_early(id) VALUES(2)",
				"INSERT INTO pa18_hist_grand(id) VALUES(3)",
				"INSERT INTO pa18_hist_late(id) VALUES(4)",
				"INSERT INTO pa18_hist_typed_late VALUES(5,'{{5,6}}')",
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(insert, 1))
			}
			cleanup = "DROP TABLE pa18_hist_grand,pa18_hist_early,pa18_hist_late,pa18_hist_old,pa18_hist_typed_late; DROP TYPE pa18_hist_type"
		}
		check := func(backend fixture.Backend) {
			if partition {
				for _, relation := range []struct {
					name      string
					rank      int64
					inherited bool
				}{
					{"pa18_hist_parent", 2, false}, {"pa18_hist_attached", 3, true}, {"pa18_hist_copied", 0, true},
				} {
					id := pgAttributeID(t, p, backend.DB, relation.name)
					want := [][]any{
						pgAttributeRow(id, "id", 23, 4, 1, -1, 0, true, "i", "p", defaults, 0),
						pgAttributeRow(id, "matrix", 1007, -1, 2, -1, relation.rank, false, "i", "x", false, 0),
					}
					for _, row := range want {
						row[12] = defaults
						if relation.inherited {
							row[17], row[18] = false, int64(1)
						}
					}
					pgAttributeRead(t, p, backend, backend.DB, id, " AND attnum>0", want)
				}
				rows := fixture.Select("SELECT id,matrix FROM pa18_hist_parent ORDER BY id", []string{"id", "matrix"}, []string{"INT4", "_INT4"}, [][]any{{int64(2), "{{1,2},{3,4}}"}, {int64(12), "{{1,2},{3,4}}"}})
				pgAttributeCheck(t, p, backend, backend.DB, rows)
				for _, leaf := range []struct {
					name string
					id   int64
				}{{"pa18_hist_attached", 2}, {"pa18_hist_copied", 12}} {
					pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,matrix FROM "+leaf.name, []string{"id", "matrix"}, []string{"INT4", "_INT4"}, [][]any{{leaf.id, "{{1,2},{3,4}}"}}))
				}
				pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT relname,relispartition,relhassubclass FROM pg_class WHERE relname IN ('pa18_hist_parent','pa18_hist_attached','pa18_hist_copied') ORDER BY relname", []string{"relname", "relispartition", "relhassubclass"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"pa18_hist_attached", true, false}, {"pa18_hist_copied", true, false}, {"pa18_hist_parent", false, true}}))
			} else {
				for _, relation := range []struct {
					name                                 string
					rank                                 int64
					inherited, note, localNote, defaults bool
				}{
					{"pa18_hist_type", 3, false, false, false, false},
					{"pa18_hist_old", 3, false, false, false, true},
					{"pa18_hist_early", 3, true, true, true, true},
					{"pa18_hist_grand", 3, true, true, false, true},
					{"pa18_hist_late", 0, true, true, true, true},
					{"pa18_hist_typed_late", 0, false, false, false, false},
				} {
					id := pgAttributeID(t, p, backend.DB, relation.name)
					want := [][]any{pgAttributeRow(id, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0), pgAttributeRow(id, "a", 1007, -1, 2, -1, relation.rank, false, "i", "x", false, 0)}
					for _, row := range want {
						row[12] = relation.defaults
						if relation.inherited {
							row[17], row[18] = false, int64(1)
						}
					}
					if relation.note {
						note := pgAttributeRow(id, "note", 25, -1, 3, -1, 0, false, "i", "x", false, 100)
						note[12] = true
						if !relation.localNote {
							note[17], note[18] = false, int64(1)
						}
						want = append(want, note)
					}
					pgAttributeRead(t, p, backend, backend.DB, id, " AND attnum>0", want)
				}
				for _, relation := range []struct {
					name string
					rows [][]any
				}{
					{"pa18_hist_old", [][]any{{int64(1), "{{1,2}}"}, {int64(2), "{{1,2}}"}, {int64(3), "{{1,2}}"}, {int64(4), "{{1,2}}"}}},
					{"pa18_hist_early", [][]any{{int64(2), "{{1,2}}"}, {int64(3), "{{1,2}}"}}},
					{"pa18_hist_grand", [][]any{{int64(3), "{{1,2}}"}}},
					{"pa18_hist_late", [][]any{{int64(4), "{{1,2}}"}}},
					{"pa18_hist_typed_late", [][]any{{int64(5), "{{5,6}}"}}},
				} {
					pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,a FROM "+relation.name+" ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, relation.rows))
				}
				for _, relation := range []struct {
					name string
					id   int64
					note string
				}{{"pa18_hist_early", 2, "early"}, {"pa18_hist_grand", 3, "early"}, {"pa18_hist_late", 4, "late"}} {
					pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,note FROM ONLY "+relation.name, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{relation.id, relation.note}}))
				}
				typeRelation := pgAttributeID(t, p, backend.DB, "pa18_hist_type")
				var typeID int64
				if err := backend.DB.QueryRowContext(p.Ctx, "SELECT reltype::bigint FROM pg_class WHERE oid=$1", typeRelation).Scan(&typeID); err != nil || typeID <= 0 {
					t.Fatal("own composite type identity", typeID, err)
				}
				pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT relname,reloftype::bigint,relhassubclass FROM pg_class WHERE relname IN ('pa18_hist_old','pa18_hist_typed_late','pa18_hist_early','pa18_hist_grand','pa18_hist_late') ORDER BY relname", []string{"relname", "reloftype", "relhassubclass"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"pa18_hist_early", int64(0), true}, {"pa18_hist_grand", int64(0), false}, {"pa18_hist_late", int64(0), false}, {"pa18_hist_old", typeID, true}, {"pa18_hist_typed_late", typeID, false}}))
			}
		}
		check(b)
		if b.PG {
			return
		}
		dump := fixture.ViewDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		check(b)
		redump := fixture.ViewDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(redump)); err != nil {
			t.Fatal(err)
		}
		check(b)
		if p.Postgres != nil {
			pg := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
			for _, emitted := range [][]byte{dump, redump} {
				if _, err := p.Postgres.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("actual rank-history dump PG replay", err)
				}
				check(pg)
				if _, err := p.Postgres.ExecContext(p.Ctx, cleanup); err != nil {
					t.Fatal("rank-history replay cleanup", err)
				}
			}
		}
	})
}
