package reference_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentReplay(t *testing.T) {
	for _, kind := range []string{"range", "list", "hash", "nested", "quoted"} {
		for _, pending := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "complete", true: "pending"}[pending], func(t *testing.T) {
				if kind == "quoted" {
					fixture.SchemaReferenceCleanup(t, "dc replay")
				}
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					strategy, bound := "RANGE(k)", "FROM(0) TO(4)"
					if kind == "list" {
						strategy, bound = "LIST(k)", "IN(1,2,NULL)"
					}
					if kind == "hash" {
						strategy, bound = "HASH(k)", "WITH(MODULUS 1,REMAINDER 0)"
					}
					parent, child, local := "dc_parent", "dc_leave", "dc_local"
					if kind == "quoted" {
						run(fixture.Exec(`CREATE SCHEMA "dc replay"`, 0))
						parent, child, local = `"dc replay"."P é"`, `"dc replay"."C é"`, `"dc replay".dc_local`
					}
					run(fixture.Exec("CREATE TABLE "+parent+"(k INT NOT NULL,payload TEXT,CONSTRAINT dc_pk PRIMARY KEY(k),CONSTRAINT dc_saved CHECK(k>0)) PARTITION BY "+strategy, 0))
					suffix := ""
					if kind == "nested" {
						suffix = " PARTITION BY RANGE(k)"
					}
					run(fixture.Exec("CREATE TABLE "+child+" PARTITION OF "+parent+" FOR VALUES "+bound+suffix, 0))
					if kind == "nested" {
						run(fixture.Exec("CREATE TABLE dc_leaf PARTITION OF dc_leave FOR VALUES FROM(0) TO(4)", 0))
					}
					run(fixture.Exec("CREATE UNIQUE INDEX dc_plain ON "+parent+"(k,payload)", 0))
					if kind == "nested" {
						run(fixture.Exec("CREATE UNIQUE INDEX dc_local ON "+child+"(payload,k)", 0))
						run(fixture.Exec("CREATE INDEX dc_leaf_local ON dc_leaf(payload)", 0))
					} else {
						run(fixture.Exec("CREATE INDEX dc_local ON "+child+"(payload,k)", 0))
					}
					run(fixture.Exec("INSERT INTO "+parent+" VALUES(1,'one'),(2,'two')", 2))
					if pending {
						detachConcurrentPendingRelations(t, p, b, parent, child, true)
					} else {
						detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE "+parent+" DETACH PARTITION "+child+" CONCURRENTLY", true)
					}
					check := func(pair *fixture.Pair, backend fixture.Backend) {
						r := func(s fixture.Step) { fixture.RunStep(t, pair.Ctx, backend.DB, backend.Recorder, s) }
						r(detachConcurrentGraph(parent, child, pending, pending))
						r(detachConcurrentRows(child, 1, 2))
						r(fixture.Select("SELECT relispartition,relpartbound IS NULL AS bound_null FROM pg_class WHERE oid='"+child+"'::regclass", []string{"relispartition", "bound_null"}, []string{"BOOL", "BOOL"}, [][]any{{pending, !pending}}))
						n := int64(2)
						if kind == "hash" {
							n = 1
						}
						r(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='"+child+"'::regclass AND contype='c' AND convalidated AND conenforced", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
						if kind == "nested" {
							r(partitionConstraintIndexState(local, true))
							r(detachConcurrentPlainIndex("dc_leaf_local"))
						} else {
							r(detachConcurrentPlainIndex(local))
						}
						if kind == "nested" {
							r(detachConcurrentGraph("dc_leave", "dc_leaf", true, false))
						}
					}
					check(p, b)
					if b.PG {
						return
					} // Only native controls lack a server equivalent.
					var stream bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
						t.Fatal(err)
					}
					var marker []byte
					if pending {
						payload := detachConcurrentRepairPayload{ParentSchema: "public", ParentTable: "dc_parent", ChildSchema: "public", ChildTable: "dc_leave", Strategy: "r"}
						if kind == "list" {
							payload.Strategy = "l"
						}
						if kind == "hash" {
							payload.Strategy = "h"
						}
						if kind == "quoted" {
							payload.ParentSchema = "dc replay"
							payload.ChildSchema = "dc replay"
							payload.ParentTable = "P é"
							payload.ChildTable = "C é"
						}
						marker = bytes.Clone(detachConcurrentRepair(t, stream.Bytes()))
						if want := detachConcurrentRepairLine(t, payload); !bytes.Equal(marker, want) {
							t.Fatalf("typed logical repair: got %q want %q", marker, want)
						}
						run(fixture.Exec("CREATE TABLE dc_marker_literal(v TEXT)", 0))
						run(fixture.Exec("INSERT INTO dc_marker_literal VALUES($1)", 1, string(marker)))
						stream.Reset()
						if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
							t.Fatal(err)
						}
					}
					if p.Postgres != nil {
						// The exact native stream remains ordinary PostgreSQL SQL. Its private
						// comment is inert there: pending is reconstructed as attached, not as
						// an asynchronous detach that accidentally completes during replay.
						pg := p.Postgres
						if _, err := pg.ExecContext(p.Ctx, stream.String()); err != nil {
							t.Fatal("unchanged stream PostgreSQL replay", err)
						}
						fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, detachConcurrentGraph(parent, child, pending, false))
						fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, detachConcurrentRows(child, 1, 2))
						values := []int64(nil)
						if pending {
							values = []int64{1, 2}
						}
						fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, detachConcurrentRows(parent, values...))
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
						if kind == "quoted" {
							if _, err := pg.ExecContext(p.Ctx, `DROP SCHEMA "dc replay" CASCADE`); err != nil {
								t.Fatal(err)
							}
						}
					}
					fresh := fixture.NewFixture(t, false, true)
					fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
					for range 2 {
						if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(stream.Bytes())); err != nil {
							t.Fatal(err)
						}
						check(fresh, fb)
						if pending {
							fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Select("SELECT v FROM dc_marker_literal", []string{"v"}, []string{"TEXT"}, [][]any{{string(marker)}}))
						}
						var again bytes.Buffer
						if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &again); err != nil || !bytes.Equal(stream.Bytes(), again.Bytes()) {
							t.Fatal("noncanonical pending replay", err, string(again.Bytes()))
						}
					}
					if pending {
						fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("ALTER TABLE "+parent+" DETACH PARTITION "+child+" FINALIZE", 0))
						fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, detachConcurrentGraph(parent, child, false, false))
					}
					// Failed replacement retains destination data and an already-held handle.
					fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("CREATE TABLE dc_sentinel(k INT)", 0))
					fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("INSERT INTO dc_sentinel VALUES(91)", 1))
					stmt, err := fresh.Memory.PrepareContext(fresh.Ctx, "SELECT k FROM dc_sentinel ORDER BY k")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					for _, damage := range []string{"truncated", "duplicate_repair", "unresolved_repair", "wrong_kind", "wrong_strategy", "default_parent", "unknown_version", "bad_base64", "unknown_field", "duplicate_field", "missing_field", "null_field", "empty_field", "nonstring_field", "invalid_strategy", "nonobject", "trailing_json", "trailing_token", "canceled", "io_failure"} {
						data := append([]byte(nil), stream.Bytes()...)
						ctx := fresh.Ctx
						switch damage {
						case "truncated":
							data = append(data, []byte("\nCREATE TABLE broken (\n")...)
						case "duplicate_repair":
							if !pending {
								continue
							}
							data = append(data, marker...)
						case "unresolved_repair", "wrong_kind", "wrong_strategy", "default_parent", "unknown_version", "bad_base64", "unknown_field", "duplicate_field", "missing_field", "null_field", "empty_field", "nonstring_field", "invalid_strategy", "nonobject", "trailing_json", "trailing_token":
							if !pending {
								continue
							}
							var payload detachConcurrentRepairPayload
							raw, err := base64.StdEncoding.Strict().DecodeString(string(bytes.TrimSpace(bytes.TrimPrefix(marker, []byte(detachConcurrentRepairPrefix)))))
							if err != nil {
								t.Fatal(err)
							}
							if err := json.Unmarshal(raw, &payload); err != nil {
								t.Fatal(err)
							}
							replacement := append([]byte(nil), marker...)
							switch damage {
							case "unresolved_repair":
								payload.ChildTable = "dc_absent"
								replacement = detachConcurrentRepairLine(t, payload)
							case "wrong_kind":
								payload.ChildSchema = payload.ParentSchema
								payload.ChildTable = "dc_pk"
								replacement = detachConcurrentRepairLine(t, payload)
							case "wrong_strategy":
								payload.Strategy = map[bool]string{false: "h", true: "r"}[kind == "hash"]
								replacement = detachConcurrentRepairLine(t, payload)
							case "default_parent":
								if kind == "hash" {
									continue
								}
								replacement = append([]byte("CREATE TABLE dc_replay_default PARTITION OF "+parent+" DEFAULT;\n"), marker...)
							case "unknown_version":
								replacement = bytes.Replace(marker, []byte("-v1 "), []byte("-v2 "), 1)
							case "bad_base64":
								replacement = []byte(detachConcurrentRepairPrefix + "!not-base64\n")
							case "unknown_field":
								raw = append(append([]byte(nil), raw[:len(raw)-1]...), []byte(",\"extra\":\"x\"}")...)
							case "duplicate_field":
								raw = append(append([]byte(nil), raw[:len(raw)-1]...), []byte(",\"strategy\":\"r\"}")...)
							case "missing_field":
								raw = []byte(`{"parent_schema":"public","parent_table":"dc_parent","child_schema":"public","child_table":"dc_leave"}`)
							case "null_field":
								raw = bytes.Replace(raw, []byte(`"strategy":"`+payload.Strategy+`"`), []byte(`"strategy":null`), 1)
							case "empty_field":
								payload.ChildTable = ""
								replacement = detachConcurrentRepairLine(t, payload)
							case "invalid_strategy":
								payload.Strategy = "x"
								replacement = detachConcurrentRepairLine(t, payload)
							case "nonstring_field":
								raw = bytes.Replace(raw, []byte(`"strategy":"`+payload.Strategy+`"`), []byte(`"strategy":1`), 1)
							case "nonobject":
								raw = []byte(`[]`)
							case "trailing_json":
								raw = append(raw, []byte("{}")...)
							case "trailing_token":
								replacement = append(append([]byte(nil), bytes.TrimSuffix(marker, []byte("\n"))...), []byte(" extra\n")...)
							}
							if damage == "unknown_field" || damage == "duplicate_field" || damage == "missing_field" || damage == "null_field" || damage == "nonstring_field" || damage == "nonobject" || damage == "trailing_json" {
								replacement = []byte(detachConcurrentRepairPrefix + base64.StdEncoding.EncodeToString(raw) + "\n")
							}
							// The identical marker text also lives in a SQL string.
							// Damage the final real repair comment, not that value.
							at := bytes.LastIndex(data, marker)
							if at < 1 || data[at-1] != '\n' {
								t.Fatal("missing final repair comment", damage)
							}
							data = append(append(append([]byte(nil), data[:at]...), replacement...), data[at+len(marker):]...)
						case "canceled":
							var cancel context.CancelFunc
							ctx, cancel = context.WithCancel(ctx)
							cancel()
						}
						var restoreErr error
						if damage == "io_failure" {
							cause := errors.New("detach replay reader failed")
							restoreErr = stuntgres.Restore(ctx, fresh.Native, io.MultiReader(bytes.NewReader(data[:len(data)/2]), detachConcurrentErrorReader{cause}))
							if !errors.Is(restoreErr, cause) {
								t.Fatal("lost ordinary reader failure", restoreErr)
							}
						} else {
							restoreErr = stuntgres.Restore(ctx, fresh.Native, bytes.NewReader(data))
						}
						if restoreErr == nil {
							t.Fatal("invalid replacement accepted", damage)
						}
						fixture.RunStep(t, fresh.Ctx, fixture.HeldStatement{Executor: fresh.Memory, Statement: stmt, SQL: "SELECT k FROM dc_sentinel ORDER BY k"}, fresh.MemoryRecord, detachConcurrentRows("dc_sentinel", 91))
					}
				})
			})
		}
	}
}

const detachConcurrentRepairPrefix = "-- stuntgres-partition-detach-pending-v1 "

type detachConcurrentRepairPayload struct {
	ParentSchema string `json:"parent_schema"`
	ParentTable  string `json:"parent_table"`
	ChildSchema  string `json:"child_schema"`
	ChildTable   string `json:"child_table"`
	Strategy     string `json:"strategy"`
}

func detachConcurrentRepairLine(t *testing.T, payload detachConcurrentRepairPayload) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(detachConcurrentRepairPrefix + base64.StdEncoding.EncodeToString(raw) + "\n")
}

func detachConcurrentRepair(t *testing.T, data []byte) []byte {
	t.Helper()
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if bytes.HasPrefix(line, []byte(detachConcurrentRepairPrefix)) {
			return line
		}
	}
	t.Fatal("pending dump omitted typed repair comment")
	return nil
}

type detachConcurrentErrorReader struct{ err error }

func (r detachConcurrentErrorReader) Read([]byte) (int, error) { return 0, r.err }
