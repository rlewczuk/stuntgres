package reference_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const pgShadowMD5 = "md50123456789abcdef0123456789abcdef"

type pgShadowQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

// No password SQL, verifier, or returned secret enters a recorder or failure.
func pgShadowSetPassword(t *testing.T, p *fixture.Pair, b fixture.Backend, role string, password *string) {
	t.Helper()
	value := "NULL"
	if password != nil { value = "'" + strings.ReplaceAll(*password, "'", "''") + "'" }
	if _, err := b.DB.ExecContext(p.Ctx, "ALTER ROLE "+role+" PASSWORD "+value); err != nil { t.Fatal("credential setup failed") }
}

func pgShadowPassword(t *testing.T, p *fixture.Pair, executor pgShadowQuerier, prepared bool, role string, want *string) string {
	t.Helper()
	q := "SELECT passwd FROM pg_catalog.pg_shadow WHERE usename=$1"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := executor.PrepareContext(p.Ctx, q)
		if e != nil { t.Fatal("credential query prepare failed") }
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx, role)
	} else { rows, err = executor.QueryContext(p.Ctx, q, role) }
	if err != nil { t.Fatal("credential query failed") }
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].Name() != "passwd" || meta[0].DatabaseTypeName() != "TEXT" { t.Fatal("credential metadata mismatch") }
	if !rows.Next() { t.Fatal("credential row missing") }
	var got sql.NullString
	if err := rows.Scan(&got); err != nil { t.Fatal("credential scan failed") }
	if rows.Next() || rows.Err() != nil { t.Fatal("credential cardinality mismatch") }
	if want == nil {
		if got.Valid { t.Fatal("absent password not SQL NULL") }
	} else if !got.Valid || got.String != *want { t.Fatal("stored credential changed") }
	return got.String
}

func pgShadowKeys(t *testing.T, password string, salt []byte, iterations int) ([]byte, []byte) {
	t.Helper()
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil { t.Fatal("independent PBKDF failed") }
	mac := hmac.New(sha256.New, salted); mac.Write([]byte("Client Key")); stored := sha256.Sum256(mac.Sum(nil))
	mac = hmac.New(sha256.New, salted); mac.Write([]byte("Server Key"))
	return stored[:], mac.Sum(nil)
}

func pgShadowSCRAM(t *testing.T) string {
	salt := []byte("0123456789abcdef")
	stored, server := pgShadowKeys(t, "shadow-fixture", salt, 4096)
	return fmt.Sprintf("SCRAM-SHA-256$4096:%s$%s:%s", base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(stored), base64.StdEncoding.EncodeToString(server))
}

func pgShadowCheckPlaintext(t *testing.T, p *fixture.Pair, b fixture.Backend, role, password string) {
	t.Helper()
	var verifier string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT passwd FROM pg_shadow WHERE usename=$1", role).Scan(&verifier); err != nil { t.Fatal("stored verifier read failed") }
	parts := strings.Split(verifier, "$")
	if len(parts) != 3 || parts[0] != "SCRAM-SHA-256" || verifier == password { t.Fatal("plaintext not stored as SCRAM") }
	middle, keys := strings.Split(parts[1], ":"), strings.Split(parts[2], ":")
	if len(middle) != 2 || len(keys) != 2 { t.Fatal("malformed SCRAM storage") }
	iterations, err := strconv.Atoi(middle[0]); if err != nil || iterations != 4096 { t.Fatal("wrong SCRAM iteration count") }
	salt, err := base64.StdEncoding.DecodeString(middle[1]); if err != nil || len(salt) != 16 { t.Fatal("wrong SCRAM salt") }
	stored, server := pgShadowKeys(t, password, salt, iterations)
	for i, want := range [][]byte{stored, server} { got, err := base64.StdEncoding.DecodeString(keys[i]); if err != nil || !bytes.Equal(got, want) { t.Fatal("independent SCRAM key mismatch") } }
	for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, b.DB, prepared, role, &verifier) }
}

func TestReferencePGShadowCredentials(t *testing.T) {
	names := []string{"pgs_credentials", "pgs_hidden", "pgs_privileged"}
	pgUserScenario(t, names, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, names[0])
		pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{pgShadowRow(names[0], id, nil, nil)}, id))
		for _, password := range []string{pgShadowMD5, pgShadowSCRAM(t)} {
			pgShadowSetPassword(t, p, b, names[0], &password)
			for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, b.DB, prepared, names[0], &password) }
			pgUserExec(t, p, b, "ALTER ROLE "+names[0]+" CREATEDB")
			pgShadowPassword(t, p, b.DB, true, names[0], &password)
			pgUserBoth(t, p, b, fixture.Select("SELECT passwd FROM pg_user WHERE usename='pgs_credentials'", []string{"passwd"}, []string{"TEXT"}, [][]any{{"********"}}))
			pgUserBoth(t, p, b, fixture.Select("SELECT rolpassword FROM pg_roles WHERE rolname='pgs_credentials'", []string{"rolpassword"}, []string{"TEXT"}, [][]any{{"********"}}))
		}
		plaintext := "shadow-plaintext-fixture"
		pgShadowSetPassword(t, p, b, names[0], &plaintext)
		pgShadowCheckPlaintext(t, p, b, names[0], plaintext)
		pgShadowSetPassword(t, p, b, names[0], nil)
		for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, b.DB, prepared, names[0], nil) }
		pgUserExec(t, p, b, "CREATE ROLE pgs_hidden")
		password := pgShadowMD5; pgShadowSetPassword(t, p, b, names[1], &password)
		pgUserBoth(t, p, b, fixture.Select("SELECT usename FROM pg_shadow WHERE usename='pgs_hidden'", []string{"usename"}, []string{"NAME"}, [][]any{}))
		pgUserExec(t, p, b, "ALTER ROLE pgs_hidden LOGIN")
		pgShadowPassword(t, p, b.DB, true, names[1], &password)
		pgUserExec(t, p, b, "ALTER ROLE pgs_hidden NOLOGIN")
		pgUserExec(t, p, b, "ALTER ROLE pgs_credentials NOLOGIN")
		pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{}, id))
		pgUserExec(t, p, b, "ALTER ROLE pgs_credentials LOGIN NOCREATEDB CONNECTION LIMIT 0 VALID UNTIL '-infinity'")
		privileged := pgUserCreate(t, p, b, names[2])
		pgUserExec(t, p, b, "ALTER ROLE pgs_privileged CREATEDB SUPERUSER REPLICATION BYPASSRLS")
		pgUserExec(t, p, b, "GRANT pgs_privileged TO pgs_credentials")
		row := pgShadowRow(names[2], privileged, nil, nil); for i := 2; i <= 5; i++ { row[i] = true }
		pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{row}, privileged))
		pgUserBoth(t, p, b, fixture.Select("SELECT usename,usecreatedb,usesuper,userepl,usebypassrls,passwd,valuntil::text AS expiry,useconfig FROM pg_shadow WHERE usesysid=$1", []string{"usename", "usecreatedb", "usesuper", "userepl", "usebypassrls", "passwd", "expiry", "useconfig"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "_TEXT"}, [][]any{{names[0], false, false, false, false, nil, "-infinity", nil}}, id))
		for _, name := range names { pgUserExec(t, p, b, "DROP ROLE "+name) }
	})
}

func TestReferencePGShadowTimeValues(t *testing.T) {
	pgUserScenario(t, []string{"pgs_time"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, "pgs_time")
		pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{pgShadowRow("pgs_time", id, nil, nil)}, id))
		pgUserExec(t, p, b, "ALTER ROLE pgs_time VALID UNTIL '2030-01-02 03:04:05+00'")
		pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{pgShadowRow("pgs_time", id, nil, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))}, id))
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		q := "SELECT valuntil::text AS expiry FROM pg_shadow WHERE usesysid=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for _, tc := range []struct{ zone, text string }{{"UTC", "2030-01-02 03:04:05+00"}, {"America/New_York", "2030-01-01 22:04:05-05"}} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"expiry"}, []string{"TEXT"}, [][]any{{tc.text}}, id))
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET TIME ZONE", 0))
		for _, expiry := range []string{"infinity", "-infinity", "2000-01-01 00:00:00+00"} {
			pgUserExec(t, p, b, "ALTER ROLE pgs_time VALID UNTIL '"+expiry+"'")
			pgUserBoth(t, p, b, fixture.Select(q, []string{"expiry"}, []string{"TEXT"}, [][]any{{expiry}}, id))
			if strings.Contains(expiry, "infinity") { pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{pgShadowRow("pgs_time", id, nil, expiry)}, id)) }
		}
		pgUserExec(t, p, b, "DROP ROLE pgs_time")
	})
}
