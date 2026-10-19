package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func extensionDump(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var b bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
func extensionControlsFailures(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, dump string, check fixture.Step) {
	t.Helper()
	if again := extensionDump(t, p.Ctx, p.Native); again != dump {
		t.Fatal("redump changed installed symbolic identities")
	}
	start := strings.Index(dump, "-- stuntgres-extension-owners-v1 ")
	if start < 0 {
		t.Fatal("missing owner metadata")
	}
	end := start + strings.Index(dump[start:], "\n")
	payload := strings.TrimPrefix(dump[start:end], "-- stuntgres-extension-owners-v1 ")
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	mutate := func(key string, value any) string {
		copy := map[string]any{}
		for k, v := range record {
			copy[k] = v
		}
		copy[key] = value
		b, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		return dump[:start] + "-- stuntgres-extension-owners-v1 " + base64.StdEncoding.EncodeToString(b) + dump[end:]
	}
	for _, script := range []string{
		strings.Replace(dump, "stuntgres-extension-owners-v1", "stuntgres-extension-owners-v2", 1),
		dump[:start] + "-- stuntgres-extension-owners-v1 !!!!" + dump[end:],
		mutate("owner", "ext_missing_owner"), mutate("schema", "wrong_namespace"), mutate("version", "missing"), mutate("members", []any{}),
		dump + dump[start:end] + "\n", "CREATE EXTENSION nonexistent_provider;",
	} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid owner/provider replay accepted", script)
		}
		fixture.RunStep(t, p.Ctx, c, r, check)
		if extensionDump(t, p.Ctx, p.Native) != dump {
			t.Fatal("failed replay changed target")
		}
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	if err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal("reader cancellation", err)
	}
	fixture.RunStep(t, p.Ctx, c, r, check)
}

// Controls are native APIs, not fictitious PostgreSQL SQL commands. The SQL
// ownership witness is shared; independent Driver replay exercises the documented
// native named-owner codec and destination-role attachment guarantees.
func extensionNamedOwnerControls(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("DROP VIEW translated", 0))
	run(fixture.Exec("DROP TABLE source", 0))
	run(fixture.Exec("DROP EXTENSION unaccent", 0))
	run(fixture.Exec("CREATE USER ext_installer SUPERUSER", 0))
	run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
	run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	extensionNamedOwners(t, p.Ctx, c, "ext_installer", "ext_installer")
	extensionInventory(t, p, c)
	if pg {
		return
	}
	dump := extensionDump(t, p.Ctx, p.Native)
	other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "ext_owner_destination")
	if _, err := other.ExecContext(p.Ctx, "CREATE USER ext_installer NOSUPERUSER"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal("changed-attribute named owner replay", err)
	}
	target, err := other.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	extensionNamedOwners(t, p.Ctx, target, "ext_installer", "ext_installer")
	extensionInventory(t, p, target)
	var hotel string
	if err := target.QueryRowContext(p.Ctx, "SELECT ext_addon.unaccent('Hôtel')").Scan(&hotel); err != nil || hotel != "Hotel" {
		t.Fatal(hotel, err)
	}
	if extensionDump(t, p.Ctx, other) != dump {
		t.Fatal("independent named-owner redump changed")
	}
	// A new Driver lacking the named principal must fail without replacing its
	// existing executable extension installation.
	missing := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "ext_missing_destination")
	if _, err := missing.ExecContext(p.Ctx, "CREATE EXTENSION unaccent"); err != nil {
		t.Fatal(err)
	}
	before := extensionDump(t, p.Ctx, missing)
	if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(dump)); err == nil {
		t.Fatal("missing owner accepted")
	}
	if extensionDump(t, p.Ctx, missing) != before {
		t.Fatal("missing owner replaced target")
	}
	// Target roles are captured before external I/O. Dropping/recreating a name
	// inside the reader is genuine public role mutation, not a synthetic revision.
	fresh := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "ext_recreated_destination")
	if _, err := fresh.ExecContext(p.Ctx, "CREATE USER ext_installer"); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.ExecContext(p.Ctx, "CREATE EXTENSION unaccent"); err != nil {
		t.Fatal(err)
	}
	before = extensionDump(t, p.Ctx, fresh)
	reader := &extensionMutationReader{reader: strings.NewReader(dump), mutate: func() error {
		for _, q := range []string{"DROP USER ext_installer", "CREATE USER ext_installer"} {
			if _, err := fresh.ExecContext(p.Ctx, q); err != nil {
				return err
			}
		}
		return nil
	}}
	if err := stuntgres.Restore(p.Ctx, fresh, reader); err == nil {
		t.Fatal("drop/recreated owner authority accepted")
	}
	if extensionDump(t, p.Ctx, fresh) != before {
		t.Fatal("owner fence replaced target")
	}
	// Plain streams do not claim to preserve original member ownership: a trusted
	// ordinary installer gets bootstrap-owned members even with the same name.
	plain := "CREATE SCHEMA ext_addon; SET SESSION AUTHORIZATION ext_installer; CREATE EXTENSION unaccent SCHEMA ext_addon; SET SESSION AUTHORIZATION postgres;"
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(plain)); err != nil {
		t.Fatal(err)
	}
	extensionNamedOwners(t, p.Ctx, target, "ext_installer", "postgres")
	// Destination database CREATE survives Reset, and revocation really prevents
	// a subsequent installation rather than merely following a duplicate branch.
	var database string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("ALTER USER ext_installer NOSUPERUSER", 0))
	quoted := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
	run(fixture.Exec("GRANT CREATE ON DATABASE "+quoted+" TO ext_installer", 0))
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("REVOKE CREATE ON DATABASE "+quoted+" FROM ext_installer", 0))
	run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
	run(fixture.ImplicitError("CREATE EXTENSION unaccent", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "unaccent"`, Hint: "Must have CREATE privilege on current database to create this extension."}))
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	extensionCount(t, p, c, r, 0)
	// Dump is committed-only while the session sees its private seven identities.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("CREATE EXTENSION unaccent", 0))
	extensionCount(t, p, c, r, 1)
	if text := extensionDump(t, p.Ctx, p.Native); strings.Contains(text, "CREATE EXTENSION") {
		t.Fatal("Dump exposed private installation")
	}
	run(fixture.Exec("ROLLBACK", 0))
	extensionCount(t, p, c, r, 0)
}
func extensionNamedOwners(t *testing.T, ctx context.Context, c *sql.Conn, extension, member string) {
	t.Helper()
	var owner string
	if err := c.QueryRowContext(ctx, "SELECT rolname FROM pg_roles WHERE oid=(SELECT extowner FROM pg_extension WHERE extname='unaccent')").Scan(&owner); err != nil || owner != extension {
		t.Fatal("extension named owner", owner, err)
	}
	rows, err := c.QueryContext(ctx, "SELECT rolname FROM pg_roles JOIN pg_proc ON pg_roles.oid=pg_proc.proowner WHERE proname='unaccent' OR proname='unaccent_init' OR proname='unaccent_lexize'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if err := rows.Scan(&owner); err != nil || owner != member {
			t.Fatal("member named owner", owner, err)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 4 {
		t.Fatal("owned procedure count", count, err)
	}
	rows.Close()
	if err := c.QueryRowContext(ctx, "SELECT rolname FROM pg_roles WHERE oid=(SELECT dictowner FROM pg_ts_dict WHERE dictname='unaccent')").Scan(&owner); err != nil || owner != member {
		t.Fatal("dictionary named owner", owner, err)
	}
}

type extensionMutationReader struct {
	reader io.Reader
	mutate func() error
	done   bool
}

func (r *extensionMutationReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		if err := r.mutate(); err != nil {
			return 0, err
		}
	}
	return r.reader.Read(p)
}
