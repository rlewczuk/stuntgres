//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesProtocol(t *testing.T) {
	definitions := []struct{ query, tag, call, value, probe string }{
		{"CREATE FUNCTION public.txr_wire(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION", "SELECT public.txr_wire(4) AS v", "5", "SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_wire'"},
		{"CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)", "CREATE OPERATOR", "SELECT 2 OPERATOR(public.#~#) 3 AS v", "5", "SELECT count(*) FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#~#'"},
		{"CREATE AGGREGATE public.txr_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE AGGREGATE", "SELECT public.txr_sum(v) AS v FROM(VALUES(1),(2),(3)) s(v)", "6", "SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_sum'"},
	}
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("extended/commit=%v", commit), func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w, peer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b)), triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
					defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
					for i, d := range definitions {
						frames := w.cycle(t, &pgproto3.Parse{Name: fmt.Sprint(i), Query: d.query}, &pgproto3.Describe{ObjectType: 'S', Name: fmt.Sprint(i)})
						triggerProtocolTags(t, frames, "1tnZ")
						triggerProtocolReady(t, frames, 'I')
						triggerProtocolScalar(t, w, d.probe, "0")
					}
					triggerProtocolCode(t, w.query(t, "BEGIN"), "")
					for i, d := range definitions {
						portal := fmt.Sprintf("utility%d", i)
						frames := w.cycle(t, &pgproto3.Bind{DestinationPortal: portal, PreparedStatement: fmt.Sprint(i)}, &pgproto3.Describe{ObjectType: 'P', Name: portal})
						triggerProtocolTags(t, frames, "2nZ")
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolScalar(t, w, d.probe, "0")
						frames = w.cycle(t, &pgproto3.Execute{Portal: portal})
						triggerProtocolTags(t, frames, "CZ")
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolCompletions(t, frames, d.tag)
						triggerProtocolScalar(t, w, d.call, d.value)
						triggerProtocolScalar(t, peer, d.probe, "0")
						name := fmt.Sprintf("call%d", i)
						frames = w.cycle(t, &pgproto3.Parse{Name: name, Query: d.call}, &pgproto3.Describe{ObjectType: 'S', Name: name})
						triggerProtocolTags(t, frames, "1tTZ")
						transactionalRoutineWireDescription(t, frames)
						frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: name, PreparedStatement: name}, &pgproto3.Describe{ObjectType: 'P', Name: name}, &pgproto3.Execute{Portal: name})
						triggerProtocolTags(t, frames, "2TDCZ")
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolValues(t, frames, [][]string{{d.value}})
						triggerProtocolCompletions(t, frames, "SELECT 1")
						transactionalRoutineWireDescription(t, frames)
					}
					triggerProtocolCode(t, w.query(t, "SAVEPOINT recovery"), "")
					frames := w.query(t, "SELECT 1/0")
					triggerProtocolError(t, frames, "22012", "division by zero", "")
					triggerProtocolReady(t, frames, 'E')
					frames = w.query(t, "SELECT txr_wire(4)")
					triggerProtocolError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
					triggerProtocolReady(t, frames, 'E')
					triggerProtocolCode(t, w.query(t, "ROLLBACK TO recovery; RELEASE recovery"), "")
					end, visible := "ROLLBACK", "0"
					if commit {
						end, visible = "COMMIT", "1"
					}
					frames = w.query(t, end)
					triggerProtocolReady(t, frames, 'I')
					triggerProtocolCompletions(t, frames, end)
					for _, d := range definitions {
						triggerProtocolScalar(t, peer, d.probe, visible)
						if commit {
							triggerProtocolScalar(t, peer, d.call, d.value)
						}
					}
					if !commit {
						frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "again", PreparedStatement: "0"}, &pgproto3.Execute{Portal: "again"})
						triggerProtocolCompletions(t, frames, "CREATE FUNCTION")
						triggerProtocolReady(t, frames, 'I')
						triggerProtocolScalar(t, w, definitions[0].call, "5")
					}
				})
			}
		})
	}
	for _, boundary := range []string{"implicit-error", "begin-rollback", "commit-error", "autocommit"} {
		t.Run(boundary, func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w, peer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b)), triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
					message := ""
					for _, d := range definitions {
						message += d.query + "; " + d.call + "; "
					}
					switch boundary {
					case "implicit-error":
						message += "SELECT 1/0"
					case "begin-rollback":
						message += "BEGIN"
					case "commit-error":
						message += "BEGIN; COMMIT; SELECT 1/0"
					}
					frames := w.query(t, message)
					if boundary == "implicit-error" || boundary == "commit-error" {
						triggerProtocolError(t, frames, "22012", "division by zero", "")
					} else {
						triggerProtocolCode(t, frames, "")
					}
					ready := byte('I')
					if boundary == "begin-rollback" {
						ready = 'T'
					}
					triggerProtocolReady(t, frames, ready)
					visible := "1"
					if boundary == "implicit-error" || boundary == "begin-rollback" {
						visible = "0"
					}
					for _, d := range definitions {
						triggerProtocolScalar(t, peer, d.probe, visible)
					}
					if boundary == "begin-rollback" {
						for _, d := range definitions {
							triggerProtocolScalar(t, w, d.call, d.value)
						}
						triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
					}
					if visible == "1" {
						for _, d := range definitions {
							triggerProtocolScalar(t, peer, d.call, d.value)
						}
					}
				})
			}
		})
	}
}

func transactionalRoutineWireDescription(t *testing.T, frames [][]byte) {
	t.Helper()
	count := 0
	for _, frame := range frames {
		if frame[0] != 'T' {
			continue
		}
		var description pgproto3.RowDescription
		if err := description.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		count++
		if len(description.Fields) != 1 {
			t.Fatal("routine result width", description.Fields)
		}
		f := description.Fields[0]
		if string(f.Name) != "v" || f.DataTypeOID != 23 || f.DataTypeSize != 4 || f.TypeModifier != -1 || f.Format != 0 || f.TableOID != 0 || f.TableAttributeNumber != 0 {
			t.Fatal("routine declared result metadata", f)
		}
	}
	if count != 1 {
		t.Fatal("routine result description count", count)
	}
}
