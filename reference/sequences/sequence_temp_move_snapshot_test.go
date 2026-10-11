package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceTempMove(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					s := fixture.NewTempReferenceSession(t, p, backend.DB, backend.Recorder, prepared)
					for _, q := range []string{"CREATE SCHEMA temp_move_a", "CREATE SCHEMA temp_move_b", "CREATE SCHEMA temp_move_dst", "CREATE SEQUENCE public.movable START 1000 CACHE 1", "CREATE TEMP SEQUENCE movable START 31 CACHE 5", "CREATE SEQUENCE temp_move_b.s START 10 CACHE 1", "CREATE SEQUENCE public.persistent_move START 51 CACHE 5", "CREATE TABLE public.link_table(id INT)", "CREATE SEQUENCE public.linked OWNED BY public.link_table.id", "SET search_path TO temp_move_a,temp_move_b,public"} {
						s.Exec(s.C, q, 0)
					}
					s.Schemas("temp_move_a", "temp_move_b", "temp_move_dst")
					h := s.Held(s.C, "SELECT nextval('movable'),nextval('s')")
					s.HeldValues(h, []string{"nextval", "nextval"}, 31, 10)
					s.Exec(s.C, "CREATE SEQUENCE temp_move_a.s START 100 CACHE 1", 0)
					log := s.State(s.C, "pg_temp.movable", 35, true)
					q := "SELECT last_value,is_called FROM pg_temp.movable"
					rows, err := s.C.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, s.Record), q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					// pq has one active query per session. Native's detached snapshot stays unread.
					if backend.PG {
						s.Snapshot(rows, q)
					}
					for _, q := range []string{"ALTER SEQUENCE pg_temp.movable SET SCHEMA public", "ALTER SEQUENCE public.persistent_move SET SCHEMA pg_temp", "ALTER SEQUENCE pg_temp.movable SET SCHEMA pg_temp"} {
						s.Utility(q, fixture.TempReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas", "", ""))
						if after := s.State(s.C, "pg_temp.movable", 35, true); after != log {
							t.Fatal("rejection changed log_cnt", log, after)
						}
						s.State(s.C, "public.persistent_move", 51, false)
						s.Value(s.C, "SELECT currval('pg_temp.movable')", 31)
						s.Value(s.C, "SELECT lastval()", 10)
						s.Value(s.Peer, "SELECT last_value FROM public.movable", 1000)
					}
					for _, destination := range []string{"temp_move_missing", "pg_temp"} {
						s.Utility("ALTER SEQUENCE public.linked SET SCHEMA "+destination, fixture.TempReferenceDiagnostic("0A000", "cannot move an owned sequence into another schema", `Sequence "linked" is linked to table "link_table".`, ""))
					}
					s.Utility("ALTER SEQUENCE pg_temp.movable SET SCHEMA temp_move_missing", fixture.TempReferenceDiagnostic("3F000", `schema "temp_move_missing" does not exist`, "", ""))
					s.HeldValues(h, []string{"nextval", "nextval"}, 32, 11)
					s.State(s.C, "pg_temp.movable", 35, true)
					s.State(s.C, "temp_move_a.s", 100, false)
					s.State(s.C, "temp_move_b.s", 11, true)
					s.Value(s.C, "SELECT nextval('s')", 100)
					if !backend.PG {
						s.Snapshot(rows, q)
					}
				})
			}
		})
	}
}

func TestReferenceSequenceTempMoveSnapshotAndLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					s := fixture.NewTempReferenceSession(t, p, backend.DB, backend.Recorder, prepared)
					for _, q := range []string{"CREATE SCHEMA temp_move_dst", "CREATE SEQUENCE public.movable START 1000 CACHE 1", "CREATE TEMP SEQUENCE movable START 31 CACHE 5", "CREATE TEMP SEQUENCE owner_only START 71 CACHE 1", "CREATE SEQUENCE public.persistent_move START 51 CACHE 5"} {
						s.Exec(s.C, q, 0)
					}
					s.Schemas("temp_move_dst")
					s.Exec(s.Peer, "CREATE TEMP SEQUENCE peer_temp START 81", 0)
					h := s.Held(s.C, "SELECT nextval('movable')")
					s.HeldValues(h, []string{"nextval"}, 31)
					s.HeldValues(h, []string{"nextval"}, 32)
					s.State(s.C, "pg_temp.movable", 35, true)
					s.Value(s.C, "SELECT nextval('public.persistent_move')", 51)
					s.Utility("ALTER SEQUENCE public.persistent_move SET SCHEMA temp_move_dst", nil)
					s.State(s.C, "temp_move_dst.persistent_move", 55, true)
					s.Value(s.C, "SELECT currval('temp_move_dst.persistent_move')", 51)
					s.Value(s.C, "SELECT nextval('temp_move_dst.persistent_move')", 52)
					if err := h.Statement.Close(); err != nil {
						t.Fatal(err)
					}
					s.Retire()
					s.Value(s.Peer, "SELECT nextval('public.movable')", 1000)
					s.Value(s.Peer, "SELECT nextval('peer_temp')", 81)
					s.Missing(s.Peer, "public.owner_only", "last_value", "24")
				})
			}
		})
	}
}
