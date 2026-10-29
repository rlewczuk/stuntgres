package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionVersionControls(t *testing.T) {
	for _, version := range []string{"1.1", "1.2"} {
		t.Run(version, func(t *testing.T) {
			extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE USER ev_owner SUPERUSER", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION ev_owner", 0))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
				run(fixture.Exec("CREATE TABLE ev_base(id int, v text DEFAULT soundex('John'), CONSTRAINT ev_base_check CHECK(levenshtein(v,'J500') >= 0))", 0))
				run(fixture.Exec("INSERT INTO ev_base(id) VALUES(1),(2)", 2))
				run(fixture.Exec("CREATE VIEW ev_base_view AS SELECT id,v,metaphone('GUMBO',4) AS code FROM ev_base", 0))
				base := fixture.Select("SELECT id,v,code FROM ev_base_view ORDER BY id", []string{"id", "v", "code"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "J500", "KM"}, {int64(2), "J500", "KM"}})
				extensionVersionBoth(t, p, b, c, base)
				before := extensionVersionInventory(t, p, b, c, "1.1")
				owner := fixture.CatalogRelationsRoleOID(t, p, c, "ev_owner")
				if before.Owner != owner {
					t.Fatal("named extension owner", before.Owner, owner)
				}
				for id, member := range before.Members {
					if member[2] != owner {
						t.Fatal("named base-member owner", id, member[2], owner)
					}
				}
				check := base
				if version == "1.2" {
					run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
					extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.2"))
					run(fixture.Exec("CREATE TABLE ev_data(id int, v text, d text[] DEFAULT daitch_mokotoff('George'), g text[] GENERATED ALWAYS AS (daitch_mokotoff(v)) STORED, CONSTRAINT ev_data_check CHECK(v IS NULL OR daitch_mokotoff(v) IS NOT NULL))", 0))
					run(fixture.Exec("INSERT INTO ev_data(id,v) VALUES(1,'John'),(2,'George'),(3,NULL)", 3))
					run(fixture.Exec("CREATE VIEW ev_codes AS SELECT id,v,d,g,daitch_mokotoff(v) AS code FROM ev_data", 0))
					check = fixture.ArrayReferenceSelect("SELECT id,v,d,g,code FROM ev_codes ORDER BY id", []string{"id", "v", "d", "g", "code"}, []string{"INT4", "TEXT", "_TEXT", "_TEXT", "_TEXT"}, []string{"INT4", "TEXT", "_TEXT", "_TEXT", "_TEXT"}, [][]any{{int64(1), "John", "{595000}", "{160000,460000}", "{160000,460000}"}, {int64(2), "George", "{595000}", "{595000}", "{595000}"}, {int64(3), nil, "{595000}", nil, nil}}, b.PG)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				extensionVersionInventory(t, p, b, c, version)
				extensionVersionBoth(t, p, b, c, check)
				if version == "1.1" {
					extensionVersionMissing(t, p, b, c)
				} else {
					daitchNamedOwners(t, p.Ctx, c, "ev_owner", "ev_owner")
				}
				if b.PG {
					return
				} // No fictitious PostgreSQL control API.
				dump := extensionVersionRoundTrip(t, p, b, c, version, check)
				extensionVersionBoth(t, p, b, c, base)
				extensionControlsFailures(t, p, c, b.Recorder, "fuzzystrmatch", dump, check)
				// Replay may genuinely CREATE the base 1.1 recipe and UPDATE to
				// 1.2 before applying current identity/ownership. The authoritative
				// logical version is the strict owner record, not a CREATE literal.
				start, end, payload := fixture.CatalogExtensionOwnerDirective(t, dump, "fuzzystrmatch", true)
				var record map[string]any
				if err := json.Unmarshal(payload, &record); err != nil {
					t.Fatal(err)
				}
				if record["version"] != version {
					t.Fatal("dump logical installed version", record["version"], version)
				}
				var create string
				for _, line := range strings.Split(dump, "\n") {
					if strings.HasPrefix(line, "CREATE EXTENSION ") && strings.Contains(line, "fuzzystrmatch") {
						create = line
						break
					}
				}
				if create == "" || !strings.Contains(create, "VERSION '") {
					t.Fatal("missing genuine versioned recipe creation", dump)
				}
				if version == "1.2" {
					// Destination ev_owner may be NOSUPERUSER. Keep CREATE's
					// captured owner but run the real update script as the replay
					// administrator, before importing final member ownership.
					update := "ALTER EXTENSION \"fuzzystrmatch\" UPDATE TO '1.2';"
					if !strings.Contains(dump, create+"\nSET SESSION AUTHORIZATION postgres;\n"+update) {
						t.Fatal("version replay did not restore administrator before UPDATE", dump)
					}
				}
				versionStart := strings.Index(create, "VERSION '") + len("VERSION '")
				versionEnd := versionStart + strings.Index(create[versionStart:], "'")
				for _, invalid := range []string{"1.0", "missing"} {
					badCreate := create[:versionStart] + invalid + create[versionEnd:]
					badSQL := strings.Replace(dump, create, badCreate, 1)
					record["version"] = invalid
					badPayload, err := json.Marshal(record)
					if err != nil {
						t.Fatal(err)
					}
					badMetadata := dump[:start] + "-- stuntgres-extension-owners-v3 " + base64.StdEncoding.EncodeToString(badPayload) + dump[end:]
					for _, bad := range []string{badSQL, badMetadata} {
						if bad == dump {
							t.Fatal("invalid version fixture unchanged")
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
							t.Fatal("invalid installed version accepted", invalid)
						}
						if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
							t.Fatal("invalid version changed complete state")
						}
						extensionVersionInventory(t, p, b, c, version)
						extensionVersionBoth(t, p, b, c, check)
						extensionVersionBoth(t, p, b, c, base)
					}
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				err := stuntgres.Restore(ctx, p.Native, &daitchRestoreCancelReader{cancel: cancel})
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatal("staged-prefix cancellation", err)
				}
				run(check)
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
					t.Fatal("canceled Restore changed complete state")
				}
				extensionVersionRestoreOwnerConflict(t, p, b, version, dump)
				if version == "1.1" {
					committed := fixture.ExtensionDump(t, p.Ctx, p.Native)
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE", 0))
					extensionVersionInventory(t, p, b, c, "1.2")
					if fixture.ExtensionDump(t, p.Ctx, p.Native) != committed {
						t.Fatal("private update leaked into Dump")
					}
					run(fixture.Exec("ROLLBACK", 0))
					extensionVersionInventory(t, p, b, c, "1.1")
					extensionVersionMissing(t, p, b, c)
				}
				other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "extension_version_"+version)
				if _, err := other.ExecContext(p.Ctx, "CREATE USER ev_owner NOSUPERUSER"); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
					t.Fatal("independent Driver restore", err)
				}
				extensionVersionPlainCheck(t, p.Ctx, other, check)
				extensionVersionPlainCheck(t, p.Ctx, other, base)
				oc, err := other.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				extensionVersionInventory(t, p, b, oc, version)
				if version == "1.2" {
					daitchNamedOwners(t, p.Ctx, oc, "ev_owner", "ev_owner")
				}
				oc.Close()
				if fixture.ExtensionDump(t, p.Ctx, other) != dump {
					t.Fatal("independent Driver symbolic redump differs")
				}
				missing := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "extension_version_missing_"+version)
				if _, err := missing.ExecContext(p.Ctx, "CREATE TABLE ev_sentinel(v int); INSERT INTO ev_sentinel VALUES(77)"); err != nil {
					t.Fatal(err)
				}
				missingBefore := fixture.ExtensionDump(t, p.Ctx, missing)
				if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(dump)); err == nil {
					t.Fatal("missing named owner admitted")
				}
				if fixture.ExtensionDump(t, p.Ctx, missing) != missingBefore {
					t.Fatal("missing owner changed destination")
				}
				// SQL emitted by native controls must also replay actual released
				// definitions on the pinned server. Owner comments are native-only.
				if p.Postgres != nil {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					for _, q := range []string{"DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP TABLE IF EXISTS ev_base CASCADE", "DROP TABLE IF EXISTS ev_data CASCADE", "CREATE USER ev_owner SUPERUSER"} {
						if _, err := pg.ExecContext(p.Ctx, q); err != nil {
							t.Fatal("oracle replay setup", q, err)
						}
					}
					defer func() {
						for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP TABLE IF EXISTS ev_base CASCADE", "DROP TABLE IF EXISTS ev_data CASCADE", "DROP OWNED BY ev_owner", "DROP ROLE ev_owner"} {
							if _, err := pg.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}()
					if _, err := pg.ExecContext(p.Ctx, dump); err != nil {
						t.Fatal("real PostgreSQL emitted SQL replay", err)
					}
					pgCheck := check
					if version == "1.2" {
						pgCheck = fixture.ArrayReferenceSelect(check.Query, check.Columns, check.Types, check.Types, check.Rows, true)
					}
					extensionVersionPlainCheck(t, p.Ctx, pg, pgCheck)
					extensionVersionPlainCheck(t, p.Ctx, pg, base)
				}
			})
		})
	}
	t.Run("TemporaryPortability", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			owner := fixture.ExtensionTemporaryOwner(t, p, b.PG)
			namespace := fixture.ExtensionTempSeed(t, p, owner)
			fixture.RunStep(t, p.Ctx, owner.Conn, owner.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA "+fixture.ExtensionTempIdentifier(namespace)+" VERSION '1.1'", 0))
			fixture.RunStep(t, p.Ctx, owner.Conn, owner.Recorder, fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
			extensionVersionInventory(t, p, b, owner.Conn, "1.2")
			fixture.RunStep(t, p.Ctx, owner.Conn, owner.Recorder, daitchSelect("SELECT "+fixture.ExtensionTempIdentifier(namespace)+".daitch_mokotoff('John')", "{160000,460000}", b.PG))
			if !b.PG {
				var out bytes.Buffer
				err := stuntgres.Dump(p.Ctx, p.Native, &out)
				if err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" || out.Len() != 0 {
					t.Fatal("temporary portability must fail before writer bytes", out.String(), err)
				}
			}
		})
	})
}

func extensionVersionRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, version string, check fixture.Step) string {
	t.Helper()
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	rows, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	daitchCount(t, p, b, c, 0)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	for _, want := range check.Rows {
		if !rows.Next() {
			t.Fatal("retained snapshot row", rows.Err())
		}
		got := make([]any, len(want))
		ptr := make([]any, len(want))
		for i := range got {
			ptr[i] = &got[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			t.Fatal(err)
		}
		for i := range got {
			got[i] = fixture.Normalize(got[i])
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("retained snapshot mutated", got, want)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained snapshot EOF", rows.Err())
	}
	rows.Close()
	extensionVersionBoth(t, p, b, c, check)
	extensionVersionInventory(t, p, b, c, version)
	if version == "1.1" {
		extensionVersionMissing(t, p, b, c)
	} else {
		extensionVersionDM(t, p, b, c)
	}
	return dump
}

// Independent Driver and unwrapped PostgreSQL replay use actual plain public
// routes, not an invented middleware recorder attached to a different pool.
func extensionVersionPlainCheck(t *testing.T, ctx context.Context, c fixture.Executor, s fixture.Step) {
	t.Helper()
	rows, err := c.QueryContext(ctx, s.Query, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, s.Columns) {
		t.Fatal("plain replay columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(s.Types) {
		t.Fatal("plain replay metadata", types, err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != s.Types[i] {
			t.Fatal("plain replay type", i, typ.DatabaseTypeName(), s.Types[i])
		}
	}
	if s.CheckMetadata != nil {
		s.CheckMetadata(t, rows)
	}
	for _, want := range s.Rows {
		if !rows.Next() {
			t.Fatal("plain replay missing row", rows.Err())
		}
		got := make([]any, len(want))
		ptr := make([]any, len(want))
		for i := range got {
			ptr[i] = &got[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			t.Fatal(err)
		}
		for i := range got {
			got[i] = fixture.Normalize(got[i])
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("plain replay values", got, want)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("plain replay EOF", rows.Err())
	}
}

// Restore captures destination principal incarnations before reading, not an
// all-DDL catalog predecessor. Exercise that established authority fence with a
// real named extension owner while retaining complete incumbent version state.
func extensionVersionRestoreOwnerConflict(t *testing.T, p *fixture.Pair, b fixture.Backend, version, source string) {
	t.Helper()
	target := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "extension_version_owner_conflict_"+version)
	for _, q := range []string{
		"CREATE USER ev_owner NOSUPERUSER",
		"CREATE EXTENSION fuzzystrmatch VERSION '1.1'",
		"CREATE TABLE ev_conflict(id int,v text DEFAULT soundex('John'))",
		"INSERT INTO ev_conflict(id) VALUES(1),(2)",
		"CREATE VIEW ev_conflict_view AS SELECT id,v,levenshtein(v,'J500') AS distance FROM ev_conflict",
	} {
		if _, err := target.ExecContext(p.Ctx, q); err != nil {
			t.Fatal("captured owner conflict setup", q, err)
		}
	}
	conn, err := target.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := fixture.ExtensionDump(t, p.Ctx, target)
	identity := extensionVersionInventory(t, p, b, conn, "1.1")
	var ownerBefore, table, view int64
	if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='ev_owner'").Scan(&ownerBefore); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ev_conflict'").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ev_conflict_view'").Scan(&view); err != nil {
		t.Fatal(err)
	}
	check := fixture.Select("SELECT id,v,distance FROM ev_conflict_view ORDER BY id", []string{"id", "v", "distance"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "J500", int64(0)}, {int64(2), "J500", int64(0)}})
	extensionVersionPlainCheck(t, p.Ctx, target, check)
	reader := &extensionMutationReader{reader: strings.NewReader(source), mutate: func() error {
		// ev_owner is deliberately unused by the postgres-owned incumbent,
		// so this is actual permitted DROP/CREATE, not a simulated revision.
		for _, q := range []string{"DROP USER ev_owner", "CREATE USER ev_owner NOSUPERUSER"} {
			if _, err := target.ExecContext(p.Ctx, q); err != nil {
				return err
			}
		}
		return nil
	}}
	err = stuntgres.Restore(p.Ctx, target, reader)
	if err == nil {
		t.Fatal("Restore adopted a recreated captured extension owner")
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("captured principal conflict must remain an ordinary control error", err)
	}
	var ownerAfter int64
	if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='ev_owner'").Scan(&ownerAfter); err != nil || ownerAfter == ownerBefore {
		t.Fatal("owner was not genuinely recreated", ownerBefore, ownerAfter, err)
	}
	if fixture.ExtensionDump(t, p.Ctx, target) != before {
		t.Fatal("failed owner-fenced Restore changed complete incumbent snapshot")
	}
	extensionVersionPreserved(t, identity, extensionVersionInventory(t, p, b, conn, "1.1"))
	extensionVersionPlainCheck(t, p.Ctx, target, check)
	extensionVersionPlainCheck(t, p.Ctx, target, extensionVersionBase())
	extensionVersionPlainCheck(t, p.Ctx, target, fixture.Select("SELECT oid::bigint FROM pg_class WHERE relname='ev_conflict'", []string{"oid"}, []string{"INT8"}, [][]any{{table}}))
	extensionVersionPlainCheck(t, p.Ctx, target, fixture.Select("SELECT oid::bigint FROM pg_class WHERE relname='ev_conflict_view'", []string{"oid"}, []string{"INT8"}, [][]any{{view}}))
	extensionVersionPlainCheck(t, p.Ctx, target, fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='daitch_mokotoff'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	missing := extensionMissing("SELECT daitch_mokotoff('John')", "daitch_mokotoff", "unknown")
	rows, err := target.QueryContext(p.Ctx, missing.Query)
	if rows != nil {
		rows.Close()
	}
	fixture.AssertDiagnostic(t, err, missing.Diagnostic)
	statement, err := target.PrepareContext(p.Ctx, missing.Query)
	if statement != nil {
		statement.Close()
	}
	fixture.AssertDiagnostic(t, err, missing.Diagnostic)
}
