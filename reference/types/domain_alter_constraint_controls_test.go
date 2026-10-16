package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"domain_alter_owner"})
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE ROLE domain_alter_owner",
			"CREATE DOMAIN domain_s.d AS integer CONSTRAINT upper_bound CHECK(VALUE<10)",
			"CREATE DOMAIN domain_s.satisfying AS integer",
			"CREATE TABLE domain_s.t(id integer,v domain_s.d,w domain_s.satisfying)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(1,-1,2),(2,NULL,NULL)", 2))
		for _, q := range []string{
			"CREATE DOMAIN domain_s.provider AS integer",
			"ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID",
			"ALTER DOMAIN domain_s.d ADD CONSTRAINT provider_check CHECK(VALUE::domain_s.provider<100) NOT VALID",
			"ALTER DOMAIN domain_s.satisfying ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID",
			"ALTER DOMAIN domain_s.d OWNER TO domain_alter_owner",
			"CREATE TEMP SEQUENCE domain_alter_temp",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT nextval('domain_alter_temp') AS v FROM public.one", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		data := fixture.Select("SELECT id,v,w FROM domain_s.t ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(-1), int64(2)}, {int64(2), nil, nil}})
		check := func(t *testing.T, x fixture.Executor, recorder *fixture.Recorder) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := data
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, x, recorder, s)
				fixture.RunStep(t, p.Ctx, x, recorder, domainAlterFlags("d", [][]any{{"positive", false}, {"provider_check", false}, {"upper_bound", true}}))
				fixture.RunStep(t, p.Ctx, x, recorder, domainAlterFlags("satisfying", [][]any{{"positive", false}}))
				t.Run(map[bool]string{false: "direct_reject", true: "prepared_reject"}[prepared], func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, x, recorder, domainCheckFailure("SELECT (-2)::domain_s.d AS v FROM public.one", "d", "positive", true, prepared))
				})
			}
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.provider because other objects depend on it")
			want.Detail, want.Hint = "constraint provider_check depends on type domain_s.provider", "Use DROP ... CASCADE to drop the dependent objects too."
			t.Run("provider_dependency", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, x, recorder, fixture.ErrorDiagnostic("DROP DOMAIN domain_s.provider RESTRICT", want))
			})
		}
		// A real namespace USAGE grant is required for the ordinary owner to
		// resolve this type. Keep that explicit ACL off states that will be
		// dumped: namespace-ACL serialization is a separate native boundary.
		checkOwner := func(t *testing.T, x fixture.Executor, recorder *fixture.Recorder) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, x, recorder, fixture.Exec("GRANT USAGE ON SCHEMA domain_s TO domain_alter_owner", 0))
			fixture.RunStep(t, p.Ctx, x, recorder, fixture.Exec("SET ROLE domain_alter_owner", 0))
			defer func() {
				if _, err := x.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
					t.Error("RESET ROLE", err)
				}
			}()
			want := fixture.ControlsReferenceDiagnostic("42704", `constraint "ownership_probe" of domain "domain_s.d" does not exist`)
			fixture.RunStep(t, p.Ctx, x, recorder, fixture.ErrorDiagnostic("ALTER DOMAIN domain_s.d DROP CONSTRAINT ownership_probe", want))
		}
		check(t, c, r)
		// PG has no process-local native controls. It executes the same portable
		// contract above; the native phase below also replays its real emitted SQL.
		if b.PG {
			checkOwner(t, c, r)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "NOT VALID") {
			t.Fatal("dump lost invalid CHECK declaration")
		}
		check(t, c, r)
		retained, err := b.DB.QueryContext(p.Ctx, data.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		domainReferenceCheckMetadata(t, retained, false, false, data.Types)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(t, c, r)
		var same bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &same); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), same.Bytes()) {
			t.Fatal("same-driver Restore changed emitted state/owners")
		}
		run(fixture.Select("SELECT nextval('domain_alter_temp') AS v FROM public.one", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
		for _, want := range []struct {
			id   int64
			v, w sql.NullInt64
		}{{1, sql.NullInt64{Int64: -1, Valid: true}, sql.NullInt64{Int64: 2, Valid: true}}, {2, sql.NullInt64{}, sql.NullInt64{}}} {
			if !retained.Next() {
				t.Fatal("retained data lost", retained.Err())
			}
			var id int64
			var v, w sql.NullInt64
			if err := retained.Scan(&id, &v, &w); err != nil {
				t.Fatal(err)
			}
			if id != want.id || v != want.v || w != want.w {
				t.Fatalf("retained %d,%+v,%+v want %+v", id, v, w, want)
			}
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained trailing/error", retained.Err())
		}
		retained.Close()
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE domain_alter_owner", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE DOMAIN spacer AS text", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(t, target.Memory, target.MemoryRecord)
		var again bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatalf("redump changed\noriginal:%s\nagain:%s", dump.String(), again.String())
		}
		// This fresh restored clone has finished its deterministic redump.
		// The ownership probe may now add its required ACL without weakening
		// permission checks or contaminating any later Dump source.
		checkOwner(t, target.Memory, target.MemoryRecord)
		// Execute the actual native stream, not a manually reconstructed PG copy.
		if p.Postgres != nil {
			// Register teardown before actual replay: a fatal assertion must not
			// leak its newly created role into the next backend scenario.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cleanup, err := p.Postgres.Conn(ctx)
				if err != nil {
					t.Errorf("replay cleanup connection: %v", err)
					return
				}
				defer func() {
					if err := cleanup.Close(); err != nil {
						t.Error("replay cleanup close", err)
					}
				}()
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP SCHEMA IF EXISTS domain_s CASCADE", "DROP TABLE IF EXISTS public.one", "DROP ROLE IF EXISTS domain_alter_owner"} {
					if _, err := cleanup.ExecContext(ctx, q); err != nil {
						t.Errorf("replay cleanup %s: %v", q, err)
					}
				}
			})
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			for _, q := range []string{"DROP SCHEMA IF EXISTS domain_s CASCADE", "DROP TABLE IF EXISTS public.one", "CREATE ROLE domain_alter_owner"} {
				if _, err := pg.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("emitted invalid-CHECK SQL replay", err)
			}
			check(t, pg, p.PostgresRecord)
			checkOwner(t, pg, p.PostgresRecord)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO sentinel VALUES(42)", 1))
		sentinel := fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		heldSentinel, err := target.Memory.QueryContext(target.Ctx, sentinel.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer heldSentinel.Close()
		bad := "CREATE SCHEMA staged; CREATE DOMAIN staged.d AS integer; CREATE TABLE staged.t(v staged.d); INSERT INTO staged.t VALUES(-1); ALTER DOMAIN staged.d ADD CONSTRAINT positive CHECK(missing>0) NOT VALID;"
		fixture.State(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), "42703")
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, sentinel)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT 1::staged.d FROM sentinel", "3F000"))
		check(t, target.Memory, target.MemoryRecord)
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore %v", err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, sentinel)
		ctx, cancel = context.WithCancel(target.Ctx)
		input := domainAlterCancelReader{Reader: bytes.NewReader(dump.Bytes()), cancel: cancel}
		if err := stuntgres.Restore(ctx, target.Native, input); !errors.Is(err, context.Canceled) {
			cancel()
			t.Fatalf("input-time canceled Restore %v", err)
		}
		cancel()
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, sentinel)
		// A reader failure after a real SQL prefix must not publish that prefix.
		broken := io.MultiReader(strings.NewReader("CREATE SCHEMA staged;"), domainAlterBrokenReader{})
		if err := stuntgres.Restore(target.Ctx, target.Native, broken); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("reader failure %v", err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, sentinel)
		if !heldSentinel.Next() {
			t.Fatal("held sentinel missing", heldSentinel.Err())
		}
		var value int64
		if err := heldSentinel.Scan(&value); err != nil || value != 42 {
			t.Fatalf("held sentinel %d,%v", value, err)
		}
		if heldSentinel.Next() || heldSentinel.Err() != nil {
			t.Fatal("held sentinel trailing/error", heldSentinel.Err())
		}
		heldSentinel.Close()
		check(t, target.Memory, target.MemoryRecord)
		// Validated and dropped states are separate real snapshots, not inferred
		// from the invalid roundtrip. Satisfying-but-invalid above must stay false.
		run(fixture.Exec("UPDATE domain_s.t SET v=1 WHERE id=1", 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", 0))
		for _, drop := range []bool{false, true} {
			if drop {
				run(fixture.Exec("ALTER DOMAIN domain_s.d DROP CONSTRAINT positive", 0))
			}
			var state bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &state); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(state.Bytes())); err != nil {
				t.Fatal(err)
			}
			flags := [][]any{{"positive", true}, {"provider_check", false}, {"upper_bound", true}}
			if drop {
				flags = [][]any{{"provider_check", false}, {"upper_bound", true}}
			}
			run(domainAlterFlags("d", flags))
			run(domainAlterFlags("satisfying", [][]any{{"positive", false}}))
			if drop {
				run(fixture.Select("SELECT (-2)::domain_s.d AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(-2)}}))
			}
		}
	})
}

type domainAlterBrokenReader struct{}

func (domainAlterBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type domainAlterCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r domainAlterCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}
