package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type pgAttributeCancelRestoreReader struct {
	reader *bytes.Reader
	cancel context.CancelFunc
}

func (r pgAttributeCancelRestoreReader) Read(dst []byte) (int, error) {
	n, err := r.reader.Read(dst[:min(len(dst), 32)])
	r.cancel()
	return n, err
}

func TestReferencePGAttributeRankRepairSQL(t *testing.T) {
	for _, kind := range []string{"local_add_merge", "local_add_nested", "detached_type"} {
		t.Run(kind, func(t *testing.T) {
			pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				local := kind != "detached_type"
				nested := kind == "local_add_nested"
				restored := false
				target := "pa18_repair_child"
				if local {
					pgClassExec(t, p, b, b.DB,
						"CREATE TYPE pa18_repair_type AS(id integer)",
						"CREATE TABLE pa18_repair_root OF pa18_repair_type",
						"CREATE TABLE pa18_repair_child(a integer[][][] DEFAULT '{{1,2}}',note text DEFAULT 'early') INHERITS(pa18_repair_root)",
						"CREATE TABLE pa18_repair_grand() INHERITS(pa18_repair_child)",
						"ALTER TYPE pa18_repair_type ADD ATTRIBUTE a integer[][] CASCADE")
					if nested {
						pgClassExec(t, p, b, b.DB,
							"ALTER TYPE pa18_repair_type ALTER ATTRIBUTE a TYPE integer[][][] CASCADE",
							"ALTER TYPE pa18_repair_type DROP ATTRIBUTE a CASCADE",
							"ALTER TYPE pa18_repair_type ADD ATTRIBUTE a integer[][] CASCADE")
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pa18_repair_child(id) VALUES(9)", 1))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pa18_repair_grand(id) VALUES(10)", 1))
				} else {
					target = "pa18_repair_root"
					pgClassExec(t, p, b, b.DB,
						"CREATE TYPE pa18_repair_type AS(a integer[][])",
						"CREATE TABLE pa18_repair_root OF pa18_repair_type",
						"ALTER TYPE pa18_repair_type ALTER ATTRIBUTE a TYPE integer[][][] CASCADE",
						"ALTER TABLE pa18_repair_root NOT OF",
						"ALTER TYPE pa18_repair_type ALTER ATTRIBUTE a TYPE integer[][]",
						"ALTER TABLE pa18_repair_root OF pa18_repair_type")
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pa18_repair_root VALUES('{{1,2}}')", 1))
				}
				check := func(backend fixture.Backend, rank int64) {
					names := []string{"pa18_repair_type", "pa18_repair_root"}
					if target != "pa18_repair_root" {
						names = append(names, target, "pa18_repair_grand")
					}
					for _, name := range names {
						id := pgAttributeID(t, p, backend.DB, name)
						dimensions := int64(2)
						if name == target {
							dimensions = rank
						}
						if name == "pa18_repair_grand" {
							dimensions = 0
							if nested {
								dimensions = 3
							}
						}
						ordinal := int64(1)
						var want [][]any
						if local {
							ordinal = 2
							// Logical reconstruction intentionally remints deleted source holes.
							if nested && !restored && (name == "pa18_repair_type" || name == "pa18_repair_root") {
								ordinal = 3
							}
							first := pgAttributeRow(id, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0)
							if name == target || name == "pa18_repair_grand" {
								first[17], first[18] = false, int64(1)
							}
							want = append(want, first)
						}
						a := pgAttributeRow(id, "a", 1007, -1, ordinal, -1, dimensions, false, "i", "x", false, 0)
						if local && (name == target || name == "pa18_repair_grand") {
							a[12], a[18] = true, int64(1)
							if name == "pa18_repair_grand" {
								a[17] = false
							}
						}
						want = append(want, a)
						if local && (name == target || name == "pa18_repair_grand") {
							note := pgAttributeRow(id, "note", 25, -1, 3, -1, 0, false, "i", "x", false, 100)
							note[12] = true
							if name == "pa18_repair_grand" {
								note[17], note[18] = false, int64(1)
							}
							want = append(want, note)
						}
						pgAttributeRead(t, p, backend, backend.DB, id, " AND attnum>0 AND NOT attisdropped", want)
					}
					if local {
						pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,a FROM pa18_repair_root ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, [][]any{{int64(9), "{{1,2}}"}, {int64(10), "{{1,2}}"}}))
						pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT id,note FROM pa18_repair_child ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "early"}, {int64(10), "early"}}))
					} else {
						pgAttributeCheck(t, p, backend, backend.DB, fixture.Select("SELECT a FROM pa18_repair_root", []string{"a"}, []string{"_INT4"}, [][]any{{"{{1,2}}"}}))
					}
				}
				check(b, 3)
				// Public native catalog sources remain readonly. PostgreSQL executes the
				// genuine repair SQL; only native Restore interprets its bounded AST.
				zero := "UPDATE pg_catalog.pg_attribute SET attndims=0 WHERE attrelid='\"public\".\"" + target + "\"'::regclass AND attname='a'"
				three := "UPDATE pg_catalog.pg_attribute SET attndims=3 WHERE attrelid='\"public\".\"" + target + "\"'::regclass AND attname='a'"
				if b.PG {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(zero, 1))
					check(b, 0)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(three, 1))
					check(b, 3)
					return
				}
				for _, query := range []string{zero, three} {
					for _, prepared := range []bool{false, true} {
						denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
						denied.Prepared, denied.ErrorStage = prepared, "execute"
						if prepared {
							denied.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, denied)
						check(b, 3)
					}
				}
				dump := fixture.ViewDump(t, p)
				originalID := pgAttributeID(t, p, b.DB, target)
				old, err := b.DB.QueryContext(p.Ctx, "SELECT attndims FROM pg_attribute WHERE attrelid=$1 AND attname='a'", originalID)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				for _, invalid := range []struct{ sql, code, message string }{
					{strings.Replace(three, "attndims=3", "atttypid=23", 1), "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{strings.Replace(three, "attndims=3", "attndims=1+2", 1), "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{strings.Replace(three, "attndims=3", "attndims=3,attnotnull=true", 1), "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{three + " AND attnum>0", "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{strings.Replace(three, " AND ", " OR ", 1), "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{strings.Replace(three, " SET ", " AS a SET ", 1), "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{three + " RETURNING attndims", "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{strings.Replace(three, " WHERE ", " FROM pg_class WHERE ", 1), "0A000", "restore accepts only canonical pg_attribute rank repairs"},
					{strings.Replace(three, "attndims=3", "attndims=32768", 1), "22003", "attribute rank is out of range"},
					{strings.Replace(three, "attndims=3", "attndims=-1", 1), "22003", "attribute rank is out of range"},
					{strings.Replace(three, "attname='a'", "attname='absent'", 1), "42703", "column \"absent\" of relation \"" + target + "\" does not exist"},
					{strings.Replace(three, target, "pa18_repair_absent", 1), "42P01", "relation \"public.pa18_repair_absent\" does not exist"},
				} {
					err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(dump)+zero+";"+invalid.sql+";"))
					if err == nil {
						t.Fatal("invalid rank repair restored", invalid.sql)
					}
					want := fixture.ControlsReferenceDiagnostic(invalid.code, invalid.message)
					fixture.AssertDiagnostic(t, err, &want)
					check(b, 3)
					if pgAttributeID(t, p, b.DB, target) != originalID {
						t.Fatal("failed repair replaced relation identity")
					}
				}
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled rank restore", err)
				}
				check(b, 3)
				if pgAttributeID(t, p, b.DB, target) != originalID {
					t.Fatal("canceled repair replaced relation identity")
				}
				reading, cancelReading := context.WithCancel(p.Ctx)
				if err := stuntgres.Restore(reading, p.Native, pgAttributeCancelRestoreReader{reader: bytes.NewReader(dump), cancel: cancelReading}); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled staged rank input", err)
				}
				cancelReading()
				check(b, 3)
				if pgAttributeID(t, p, b.DB, target) != originalID {
					t.Fatal("staged cancellation replaced relation identity")
				}
				for range 2 {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal("rank repair restore", err)
					}
					restored = true
					check(b, 3)
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
							t.Fatal("actual rank repair dump PG replay", err)
						}
						pg := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
						check(pg, 3)
						cleanup := "DROP TABLE pa18_repair_root; DROP TYPE pa18_repair_type"
						if local {
							cleanup = "DROP TABLE pa18_repair_grand,pa18_repair_child,pa18_repair_root; DROP TYPE pa18_repair_type"
						}
						if _, err := p.Postgres.ExecContext(p.Ctx, cleanup); err != nil {
							t.Fatal("rank repair replay cleanup", err)
						}
					}
					dump = fixture.ViewDump(t, p)
				}
				var retained int64
				if !old.Next() {
					t.Fatal("retained rank capture missing", old.Err())
				}
				if err := old.Scan(&retained); err != nil {
					t.Fatal(err)
				}
				if retained != 3 || old.Next() || old.Err() != nil {
					t.Fatal("retained rank capture changed", retained, old.Err())
				}
			})
		})
	}
}
