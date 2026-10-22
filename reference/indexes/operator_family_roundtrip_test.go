package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorFamilyRoundtrip(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"family_roundtrip_owner"})
	fixture.SchemaReferenceCleanup(t, "family_roundtrip")
	var emitted string
	familyBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`CREATE ROLE family_roundtrip_owner`, 0))
		run(fixture.Exec(`CREATE SCHEMA family_roundtrip`, 0))
		run(fixture.Exec(`CREATE OPERATOR family_roundtrip.=== (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4eq)`, 0))
		run(fixture.Exec(`CREATE OPERATOR FAMILY family_roundtrip.old_name USING btree`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY family_roundtrip.old_name USING btree ADD OPERATOR 3 family_roundtrip.===(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY family_roundtrip.old_name USING btree RENAME TO current_name`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY family_roundtrip.current_name USING btree OWNER TO family_roundtrip_owner`, 0))
		run(fixture.Exec(`CREATE TABLE family_roundtrip.t(n integer)`, 0))
		run(fixture.Exec(`INSERT INTO family_roundtrip.t VALUES(2),(1),(NULL),(2)`, 4))
		run(fixture.Exec(`CREATE INDEX family_roundtrip_b ON family_roundtrip.t USING btree(n)`, 0))
		run(fixture.Exec(`CREATE INDEX family_roundtrip_h ON family_roundtrip.t USING hash(n)`, 0))
		const metadata = `SELECT f.opfname,r.rolname,a.amopstrategy,a.amoplefttype::bigint,a.amoprighttype::bigint,o.oprname,p.amprocnum,p.amproc::oid::bigint FROM pg_opfamily f JOIN pg_roles r ON r.oid=f.opfowner JOIN pg_amop a ON a.amopfamily=f.oid JOIN pg_operator o ON o.oid=a.amopopr JOIN pg_amproc p ON p.amprocfamily=f.oid WHERE f.opfnamespace='family_roundtrip'::regnamespace`
		metadataStep := func() fixture.Step {
			return fixture.Select(metadata, []string{"opfname", "rolname", "amopstrategy", "amoplefttype", "amoprighttype", "oprname", "amprocnum", "amproc"}, []string{"NAME", "NAME", "INT2", "INT8", "INT8", "NAME", "INT2", "INT8"}, [][]any{{"current_name", "family_roundtrip_owner", int64(3), int64(23), int64(23), "===", int64(1), int64(351)}})
		}
		check := func() {
			run(metadataStep())
			run(fixture.Select(`SELECT n FROM family_roundtrip.t ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {nil}}))
		}
		check()
		identity := func(db interface {
			QueryRowContext(context.Context, string, ...any) *sql.Row
		}, ctx context.Context) int64 {
			var oid int64
			if err := db.QueryRowContext(ctx, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='current_name' AND opfnamespace='family_roundtrip'::regnamespace`).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			return oid
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			for _, want := range []string{`CREATE OPERATOR FAMILY "family_roundtrip"."current_name" USING btree`, `ALTER OPERATOR FAMILY "family_roundtrip"."current_name" USING btree`, `"family_roundtrip".===`} {
				if !strings.Contains(emitted, want) {
					t.Fatalf("missing dump definition %q\n%s", want, emitted)
				}
			}
			if strings.Contains(emitted, `old_name`) {
				t.Fatal("stale family name dumped")
			}
			retained, err := p.Native.QueryContext(p.Ctx, `SELECT opfname FROM pg_opfamily WHERE opfname='current_name' AND opfnamespace='family_roundtrip'::regnamespace`)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			foreign := fixture.NewFixture(t, false, true)
			// Destination identities/principals exist already; restoration must remap,
			// not borrow the source descriptor graph or provider captures.
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec(`CREATE ROLE family_roundtrip_owner`, 0))
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec(`CREATE OPERATOR FAMILY public.destination_collision USING hash`, 0))
			if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("foreign Restore", err)
			}
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, metadataStep())
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec(`ALTER OPERATOR FAMILY family_roundtrip.current_name USING btree DROP FUNCTION 1(integer)`, 0))
			check() // destination mutation cannot change source family/member/roots
			var before, after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+`ALTER OPERATOR FAMILY family_roundtrip.current_name USING btree ADD FUNCTION 7 pg_catalog.btint4cmp(integer,integer);`))
			want := fixture.ControlsReferenceDiagnostic("42P17", `invalid function number 7, must be between 1 and 6`)
			fixture.AssertDiagnostic(t, err, &want)
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != before.String() {
				t.Fatal("failed Restore changed source", err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			if got := identity(conn, p.Ctx); got == 0 {
				t.Fatal("restored family has no identity")
			}
			if !retained.Next() {
				t.Fatal("retained source rows lost", retained.Err())
			}
			var name string
			if err := retained.Scan(&name); err != nil || name != "current_name" {
				t.Fatal(name, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained rows changed", retained.Err())
			}
			retained.Close()
		} else if emitted != "" {
			// Replay actual native bytes, never a reconstructed PostgreSQL imitation.
			run(fixture.Exec(`DROP SCHEMA family_roundtrip CASCADE`, 0))
			if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("PostgreSQL rejected native family dump", err)
			}
		}
		// In the oracle-only round emitted is empty: validate the standard SQL
		// contract above without pretending PostgreSQL has native controls.
		check()
		run(fixture.Exec(`INSERT INTO family_roundtrip.t VALUES(0)`, 1))
		run(fixture.Exec(`DELETE FROM family_roundtrip.t WHERE n=1`, 1))
		run(fixture.Select(`SELECT n FROM family_roundtrip.t ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(2)}, {int64(2)}, {nil}}))
	})
}
