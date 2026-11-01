//go:build !noservertests

package reference_test

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

func snapshotDefaultMixedDemand(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
			peer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
			importer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
			triggerProtocolCode(t, w.query(t, snapshotDefaultSetup), "")
			triggerProtocolCode(t, w.query(t,
				"CREATE FUNCTION snapshot_mix_sql(a integer) RETURNS text STABLE "+snapshotDefaultDeclaration("standard", true)+"; "+
				"CREATE FUNCTION snapshot_mix_pl(a integer) RETURNS text STABLE "+snapshotDefaultDeclaration("pl", false)+"; "+
				"CREATE FUNCTION snapshot_mix_export(a integer) RETURNS text STABLE "+snapshotDefaultDeclaration("standard", true)+"; "+
				"CREATE FUNCTION snapshot_mix_echo(a text) RETURNS text STABLE RETURN a; "+
				"CREATE FUNCTION snapshot_mix_hidden(a text DEFAULT snapshot_mix_export(5)) RETURNS text STABLE LANGUAGE plpgsql AS $$BEGIN RETURN a; END$$; "+
				"CREATE FUNCTION snapshot_mix_body(a integer) RETURNS text STABLE LANGUAGE plpgsql AS $$BEGIN RETURN snapshot_mix_export(a); END$$; "+
				"CREATE TABLE snapshot_mix_rows(s text DEFAULT snapshot_mix_sql(snapshot_default_arg(1+3)),p text DEFAULT snapshot_mix_pl(snapshot_default_arg(1+3)),nested text DEFAULT snapshot_mix_echo(snapshot_mix_sql(snapshot_default_arg(1+3))),siblings text DEFAULT snapshot_mix_sql(snapshot_default_arg(1+3))||'/'||snapshot_mix_pl(snapshot_default_arg(1+3)),argument text DEFAULT snapshot_mix_echo(snapshot_mix_export(snapshot_default_arg(1+3))),hidden text DEFAULT snapshot_mix_hidden(),body text DEFAULT snapshot_mix_body(snapshot_default_arg(1+3)))"), "")
			triggerProtocolCode(t, w.query(t, "BEGIN; INSERT INTO snapshot_default_cut VALUES(9,90)"), "")
			defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
			frames := w.cycle(t,
				&pgproto3.Parse{Name: "mixed", Query: "INSERT INTO snapshot_mix_rows DEFAULT VALUES RETURNING s,p,nested,siblings,argument,hidden,body"},
				&pgproto3.Describe{ObjectType: 'S', Name: "mixed"},
				&pgproto3.Bind{DestinationPortal: "mixed_old", PreparedStatement: "mixed"},
				&pgproto3.Parse{Name: "mixed_empty", Query: "INSERT INTO snapshot_mix_rows(s) SELECT 'unused' WHERE false"},
				&pgproto3.Bind{DestinationPortal: "mixed_empty", PreparedStatement: "mixed_empty"})
			triggerProtocolCode(t, frames, "")
			routineDefaultPortalDescription(t, frames, []string{"s", "p", "nested", "siblings", "argument", "hidden", "body"}, []uint32{25, 25, 25, 25, 25, 25, 25})
			snapshotDefaultNoNotice(t, frames)
			triggerProtocolScalar(t, w, "SELECT is_called::text FROM snapshot_default_seq", "false")
			triggerProtocolCode(t, w.query(t,
				"CREATE OR REPLACE FUNCTION snapshot_mix_sql(a integer) RETURNS text STABLE "+snapshotDefaultDeclaration("standard", false)+"; "+
				"CREATE OR REPLACE FUNCTION snapshot_mix_pl(a integer) RETURNS text STABLE "+snapshotDefaultDeclaration("pl", true)+"; "+
				"CREATE OR REPLACE FUNCTION snapshot_mix_echo(a text) RETURNS text STABLE RETURN 'quiet-echo'::text; "+
				"CREATE OR REPLACE FUNCTION snapshot_mix_hidden(a text DEFAULT snapshot_mix_export(5)) RETURNS text STABLE LANGUAGE plpgsql AS $$BEGIN RETURN a; END$$; "+
				"CREATE OR REPLACE FUNCTION snapshot_mix_body(a integer) RETURNS text STABLE LANGUAGE plpgsql AS $$BEGIN RETURN snapshot_mix_export(a); END$$; "+
				"CREATE OR REPLACE FUNCTION snapshot_default_arg(a integer) RETURNS integer IMMUTABLE RETURN a+10"), "")
			frames = w.cycle(t, &pgproto3.Execute{Portal: "mixed_empty"})
			triggerProtocolCode(t, frames, "")
			triggerProtocolCompletions(t, frames, "INSERT 0 0")
			snapshotDefaultNoNotice(t, frames)
			triggerProtocolScalar(t, w, "SELECT is_called::text FROM snapshot_default_seq", "false")
			for i, name := range []string{"mixed_old", "mixed_fresh"} {
				messages := []pgproto3.FrontendMessage{&pgproto3.Execute{Portal: name}}
				if i == 1 {
					messages = append([]pgproto3.FrontendMessage{&pgproto3.Bind{DestinationPortal: name, PreparedStatement: "mixed"}}, messages...)
				}
				frames = snapshotDefaultBlockedExecute(t, f, b, w, peer, messages, i+1)
				triggerProtocolCode(t, frames, "")
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolReady(t, frames, 'T')
				snapshotDefaultNoNotice(t, frames)
				var values []string
				for _, frame := range frames {
					if frame[0] != 'D' {
						continue
					}
					if values != nil {
						t.Fatal("mixed DEFAULT returned more than one row")
					}
					var row pgproto3.DataRow
					if err := row.Decode(frame[5:]); err != nil {
						t.Fatal(err)
					}
					for _, value := range row.Values {
						if value == nil {
							t.Fatal("mixed DEFAULT returned NULL")
						}
						values = append(values, string(value))
					}
				}
				if len(values) != 7 {
					t.Fatalf("mixed DEFAULT values=%v", values)
				}
				argument := "5"
				if i == 1 {
					argument = "14"
					if values[0] != "quiet:14" || values[2] != "quiet-echo" || values[4] != "quiet-echo" {
						t.Fatalf("fresh SQL/default argument selection=%v", values)
					}
				}
				siblings := strings.Split(values[3], "/")
				if len(siblings) != 2 || i == 1 && siblings[0] != "quiet:14" {
					t.Fatalf("mixed sibling selection=%v", siblings)
				}
				exports := []string{values[1], siblings[1], values[6]}
				if i == 0 {
					exports = append(exports, values[0], values[2], siblings[0], values[4])
				}
				// Every written sibling, exporting actual argument and current
				// PL body borrows the same outer F, despite different body modes.
				for _, value := range exports {
					token := snapshotDefaultToken(t, value, argument)
					triggerProtocolCode(t, importer.query(t, "BEGIN ISOLATION LEVEL REPEATABLE READ; SET TRANSACTION SNAPSHOT '"+token+"'"), "")
					want := "10"
					if i == 1 {
						want = "11"
					}
					triggerProtocolValues(t, importer.query(t, "SELECT id,v FROM snapshot_default_cut ORDER BY id"), [][]string{{"1", want}, {"3", "30"}})
					triggerProtocolScalar(t, importer, "SELECT v FROM snapshot_default_unread", want)
					count := "0"
					if i == 1 {
						count = "1"
					}
					triggerProtocolScalar(t, importer, "SELECT count(*) FROM snapshot_default_empty", count)
					triggerProtocolCode(t, importer.query(t, "ROLLBACK"), "")
				}
				// The hidden formal default has its own immutable argument5,
				// rather than inheriting a written sibling's argument14 or mode.
				token := snapshotDefaultToken(t, values[5], "5")
				triggerProtocolCode(t, importer.query(t, "BEGIN ISOLATION LEVEL REPEATABLE READ; SET TRANSACTION SNAPSHOT '"+token+"'"), "")
				triggerProtocolScalar(t, importer, "SELECT count(*) FROM snapshot_default_cut WHERE id=9", "0")
				triggerProtocolCode(t, importer.query(t, "ROLLBACK"), "")
			}
			triggerProtocolScalar(t, w, "SELECT count(*) FROM snapshot_mix_rows", "2")
		})
	}
}
