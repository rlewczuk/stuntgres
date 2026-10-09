package reference_test

import (
	"context"
	"testing"
)

func TestReferenceSequenceTempMove(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					s := newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)
					for _, q := range []string{"CREATE SCHEMA temp_move_a", "CREATE SCHEMA temp_move_b", "CREATE SCHEMA temp_move_dst", "CREATE SEQUENCE public.movable START 1000 CACHE 1", "CREATE TEMP SEQUENCE movable START 31 CACHE 5", "CREATE SEQUENCE temp_move_b.s START 10 CACHE 1", "CREATE SEQUENCE public.persistent_move START 51 CACHE 5", "CREATE TABLE public.link_table(id INT)", "CREATE SEQUENCE public.linked OWNED BY public.link_table.id", "SET search_path TO temp_move_a,temp_move_b,public"} {
						s.exec(s.c, q, 0)
					}
					s.schemas("temp_move_a", "temp_move_b", "temp_move_dst")
					h := s.held(s.c, "SELECT nextval('movable'),nextval('s')")
					s.heldValues(h, []string{"nextval", "nextval"}, 31, 10)
					s.exec(s.c, "CREATE SEQUENCE temp_move_a.s START 100 CACHE 1", 0)
					log := s.state(s.c, "pg_temp.movable", 35, true)
					q := "SELECT last_value,is_called FROM pg_temp.movable"
					rows, err := s.c.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, s.record), q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					// pq has one active query per session. Native's detached snapshot stays unread.
					if backend.pg {
						s.snapshot(rows, q)
					}
					for _, q := range []string{"ALTER SEQUENCE pg_temp.movable SET SCHEMA public", "ALTER SEQUENCE public.persistent_move SET SCHEMA pg_temp", "ALTER SEQUENCE pg_temp.movable SET SCHEMA pg_temp"} {
						s.utility(q, tempReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas", "", ""))
						if after := s.state(s.c, "pg_temp.movable", 35, true); after != log {
							t.Fatal("rejection changed log_cnt", log, after)
						}
						s.state(s.c, "public.persistent_move", 51, false)
						s.value(s.c, "SELECT currval('pg_temp.movable')", 31)
						s.value(s.c, "SELECT lastval()", 10)
						s.value(s.peer, "SELECT last_value FROM public.movable", 1000)
					}
					for _, destination := range []string{"temp_move_missing", "pg_temp"} {
						s.utility("ALTER SEQUENCE public.linked SET SCHEMA "+destination, tempReferenceDiagnostic("0A000", "cannot move an owned sequence into another schema", `Sequence "linked" is linked to table "link_table".`, ""))
					}
					s.utility("ALTER SEQUENCE pg_temp.movable SET SCHEMA temp_move_missing", tempReferenceDiagnostic("3F000", `schema "temp_move_missing" does not exist`, "", ""))
					s.heldValues(h, []string{"nextval", "nextval"}, 32, 11)
					s.state(s.c, "pg_temp.movable", 35, true)
					s.state(s.c, "temp_move_a.s", 100, false)
					s.state(s.c, "temp_move_b.s", 11, true)
					s.value(s.c, "SELECT nextval('s')", 100)
					if !backend.pg {
						s.snapshot(rows, q)
					}
				})
			}
		})
	}
}

func TestReferenceSequenceTempMoveSnapshotAndLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					s := newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)
					for _, q := range []string{"CREATE SCHEMA temp_move_dst", "CREATE SEQUENCE public.movable START 1000 CACHE 1", "CREATE TEMP SEQUENCE movable START 31 CACHE 5", "CREATE TEMP SEQUENCE owner_only START 71 CACHE 1", "CREATE SEQUENCE public.persistent_move START 51 CACHE 5"} {
						s.exec(s.c, q, 0)
					}
					s.schemas("temp_move_dst")
					s.exec(s.peer, "CREATE TEMP SEQUENCE peer_temp START 81", 0)
					h := s.held(s.c, "SELECT nextval('movable')")
					s.heldValues(h, []string{"nextval"}, 31)
					s.heldValues(h, []string{"nextval"}, 32)
					s.state(s.c, "pg_temp.movable", 35, true)
					s.value(s.c, "SELECT nextval('public.persistent_move')", 51)
					s.utility("ALTER SEQUENCE public.persistent_move SET SCHEMA temp_move_dst", nil)
					s.state(s.c, "temp_move_dst.persistent_move", 55, true)
					s.value(s.c, "SELECT currval('temp_move_dst.persistent_move')", 51)
					s.value(s.c, "SELECT nextval('temp_move_dst.persistent_move')", 52)
					if err := h.statement.Close(); err != nil {
						t.Fatal(err)
					}
					s.retire()
					s.value(s.peer, "SELECT nextval('public.movable')", 1000)
					s.value(s.peer, "SELECT nextval('peer_temp')", 81)
					s.missing(s.peer, "public.owner_only", "last_value", "24")
				})
			}
		})
	}
}
