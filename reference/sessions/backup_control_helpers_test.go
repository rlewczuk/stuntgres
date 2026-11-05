package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var backupIdentities = []string{"pg_backup_start(text,boolean)", "pg_backup_stop(boolean)"}
var backupFields = []string{"lsn", "labelfile", "spcmapfile"}
var backupTypes = []string{"PG_LSN", "TEXT", "TEXT"}

func backupRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	if s.State != "" && s.ErrorStage == "" {
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		s.Prepared, s.ErrorStage = true, "prepare"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		return
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	recoveryHeld(t, p, b, c, s)
}
func backupError(q, code, message, hint string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Hint: hint})
	s.SelectRows = true
	s.ErrorStage = "execute"
	s.Args = args
	return s
}
func backupUnsupported(q, name string, args ...any) fixture.Step {
	return backupError(q, "0A000", name+" physical online backup lifecycle is unsupported in embedded mode", "", args...)
}
func backupAbsent(q string, pg bool) fixture.Step {
	if !pg {
		return backupUnsupported(q, "pg_backup_stop")
	}
	return backupError(q, "55000", "backup is not in progress", "Did you call pg_backup_start()?")
}
func backupSelect(pg bool, q string, names, types []string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, want, args...)
	s.CheckMetadata = backupMetadata(pg, false, names, types)
	s.CheckScan = func(t *testing.T, r *sql.Rows, _ int) {
		values := make([]any, len(types))
		dest := make([]any, len(types))
		for i := range dest {
			dest[i] = &values[i]
		}
		if err := r.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			if v != nil {
				fixture.AssertRawValue(t, pg, types[i], v)
			}
		}
	}
	return s
}

// Stored view columns carry native nullability; computed expressions do not.
// lib/pq reports unknown nullability in either context (docs/TESTING.md).
func backupMetadata(pg, stored bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, r *sql.Rows) {
		t.Helper()
		cols, err := r.ColumnTypes()
		if err != nil || len(cols) != len(types) {
			t.Fatal(cols, err)
		}
		for i, col := range cols {
			scan := reflect.TypeFor[string]()
			if pg && (types[i] == "PG_LSN" || types[i] == "RECORD") {
				scan = reflect.TypeFor[any]()
			}
			if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan {
				t.Fatal("backup metadata", i, col.Name(), col.DatabaseTypeName(), col.ScanType())
			}
			wantNullable := stored && !pg
			if n, ok := col.Nullable(); n != wantNullable || ok != wantNullable {
				t.Fatal("backup nullability", stored, n, ok)
			}
			length, known := col.Length()
			wantLength := int64(0)
			if types[i] == "TEXT" {
				wantLength = 1<<63 - 1
			}
			if length != wantLength || known != (types[i] == "TEXT") {
				t.Fatal("length", length, known)
			}
			if _, _, ok := col.DecimalSize(); ok {
				t.Fatal("unexpected precision")
			}
		}
	}
}
func backupLSN(t *testing.T, s string) uint64 {
	t.Helper()
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		t.Fatal("invalid LSN", s)
	}
	var words [2]uint64
	for i, part := range parts {
		word, err := strconv.ParseUint(part, 16, 32)
		if err != nil || strings.ToUpper(strconv.FormatUint(word, 16)) != part {
			t.Fatal("noncanonical LSN", s, err)
		}
		words[i] = word
	}
	return words[0]<<32 | words[1]
}

// Each successful physical start owns its cleanup on the same retained Conn.
// Cleanup uses a fresh deadline even if the scenario failed or was canceled.
func backupPair(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, executor fixture.Executor, start, label, stop string, second bool, args ...any) {
	t.Helper()
	if !b.PG {
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, backupUnsupported(start, "pg_backup_start", args...))
		if second {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, backupUnsupported("SELECT pg_backup_start('again',true)", "pg_backup_start"))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, backupUnsupported(stop, "pg_backup_stop"))
		return
	}
	var begin string
	rows, err := executor.QueryContext(p.Ctx, start, args...)
	if err != nil {
		t.Fatal(err)
	}
	active := false
	defer func() {
		if active {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r, err := c.QueryContext(ctx, "SELECT * FROM pg_backup_stop(false)")
			if err != nil {
				t.Error("backup cleanup", err)
				return
			}
			for r.Next() {
			}
			if err := r.Err(); err != nil {
				t.Error("backup cleanup", err)
			}
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing physical start", rows.Err())
	}
	active = true
	if err := rows.Scan(&begin); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("non-scalar start", rows.Err())
	}
	rows.Close()
	startLSN := backupLSN(t, begin)
	if startLSN == 0 {
		t.Fatal("invented zero LSN")
	}
	if second {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, backupError("SELECT pg_backup_start('again',true)", "55000", "a backup is already in progress in this session", ""))
	}
	r, err := c.QueryContext(p.Ctx, stop)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil || len(cols) != 3 {
		t.Fatal(cols, err)
	}
	for i, col := range cols {
		if col.Name() != backupFields[i] || col.DatabaseTypeName() != backupTypes[i] {
			t.Fatal("physical record metadata", cols)
		}
	}
	if !r.Next() {
		t.Fatal("missing physical stop", r.Err())
	}
	var end, labelfile, spcmap string
	if err := r.Scan(&end, &labelfile, &spcmap); err != nil {
		t.Fatal(err)
	}
	active = false
	if backupLSN(t, end) < startLSN || !strings.Contains(labelfile, "START WAL LOCATION: "+begin) || !strings.Contains(labelfile, "BACKUP METHOD: streamed\n") || !strings.Contains(labelfile, "LABEL: "+label+"\n") || spcmap != "" {
		t.Fatal("genuine backup file framing", begin, end, labelfile, spcmap)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("non-scalar stop", r.Err())
	}
}
func backupRestore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, roles ...string) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
			return
		}
		// A raw '{}' cast is zero-dimensional on stock PostgreSQL, unlike an
		// empty ACL made by REVOKE. Repair malformed shapes before ACL operations,
		// preserving valid grants so REVOKE can remove their shared dependencies.
		if b.PG {
			if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=ARRAY['stuntgres=X/stuntgres']::aclitem[] WHERE oid IN (2172,2739) AND proacl IS NOT NULL AND array_ndims(proacl) IS DISTINCT FROM 1"); err != nil {
				t.Error(err)
			}
		}
		for _, id := range backupIdentities {
			for _, role := range roles {
				if _, err := c.ExecContext(ctx, "REVOKE ALL ON FUNCTION "+id+" FROM "+role); err != nil {
					t.Error(err)
				}
			}
			if _, err := c.ExecContext(ctx, "REVOKE ALL ON FUNCTION "+id+" FROM PUBLIC"); err != nil {
				t.Error(err)
			}
		}
		if b.PG {
			if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=ARRAY['stuntgres=X/stuntgres']::aclitem[] WHERE oid IN (2172,2739)"); err != nil {
				t.Error(err)
			}
		}
	}
}
