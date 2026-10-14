package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Exercise the shared fixture's real ownership producer, not a permissive seed
// or internal authorization hook. The tested actor keeps its physical handle.
func pgNamespaceFixtureOwnerScope(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	public := pgNamespaceID(t, p, c, "public")
	var prior int64
	if err := c.QueryRowContext(p.Ctx, "SELECT nspowner FROM pg_namespace WHERE oid=$1", public).Scan(&prior); err != nil {
		t.Fatal(err)
	}
	reader := pgNamespaceRoleID(t, p, c, "ns18_reader")
	pgNamespaceExec(t, p, b, c, "SET ROLE ns18_reader")
	actor := func() {
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"ns18_reader"}}))
	}
	restored := func() {
		actor()
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT oid,nspowner FROM pg_namespace WHERE nspname='public'", []string{"oid", "nspowner"}, []string{"OID", "OID"}, [][]any{{public, prior}}))
	}
	for _, prepared := range []bool{false, true} {
		name := "ns18_scope_direct"
		if prepared {
			name = "ns18_scope_prepared"
		}
		fixture.WithPublicSchemaOwner(t, p, b, "ns18_reader", func() {
			actor()
			pgNamespaceRow(t, p, b, c, "public", public, reader, "{ns18_reader=UC/ns18_reader,=U/ns18_reader}")
			s := fixture.Exec("CREATE TABLE public."+name+"(i INT)", 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Exec("INSERT INTO public."+name+" VALUES(18)", 1)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
		restored()
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT relnamespace,relowner FROM pg_class WHERE relname=$1", []string{"relnamespace", "relowner"}, []string{"OID", "OID"}, [][]any{{public, reader}}, name))
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT i FROM public."+name, []string{"i"}, []string{"INT4"}, [][]any{{int64(18)}}))
		denied := fixture.SchemaReferenceCreationError("CREATE TABLE public.ns18_scope_denied(i INT)", "42501", "permission denied for schema public", prepared, "14")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
	}
	// A body panic propagates, but restoration still occurs before the caller's
	// recovery/next assertion. Fatal-test unwinding uses the same deferred path.
	marker := new(int)
	func() {
		defer func() {
			if got := recover(); got != marker {
				t.Fatalf("scope panic propagation: %v", got)
			}
		}()
		fixture.WithPublicSchemaOwner(t, p, b, "ns18_reader", func() { actor(); panic(marker) })
	}()
	restored()
	pgNamespaceExec(t, p, b, c, "RESET ROLE")
}
