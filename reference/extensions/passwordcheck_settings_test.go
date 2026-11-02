package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func passwordCheckTextMetadata(t *testing.T, rows *sql.Rows, pg, nullable bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil { t.Fatal(err) }
	for _, typ := range types {
		if typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeFor[string]() { t.Fatal("TEXT metadata", typ) }
		if n, known := typ.Nullable(); known == pg || n != (!pg && nullable) { t.Fatal("TEXT nullability", typ.Name(), n, known) }
		if n, known := typ.Length(); !known || n != math.MaxInt64 { t.Fatal("TEXT length", typ.Name(), n, known) }
		if p, s, known := typ.DecimalSize(); p != 0 || s != 0 || known { t.Fatal("TEXT decimal", typ.Name(), p, s, known) }
	}
}

func passwordCheckShowAll(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := c.PrepareContext(p.Ctx, "SHOW ALL")
		if e != nil { t.Fatal(e) }
		defer s.Close()
		rows, err = s.QueryContext(p.Ctx)
	} else { rows, err = c.QueryContext(p.Ctx, "SHOW ALL") }
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"name", "setting", "description"}) { t.Fatal(columns, err) }
	passwordCheckTextMetadata(t, rows, b.PG, false)
	found := 0
	for rows.Next() {
		var name, setting, description string
		if err := rows.Scan(&name, &setting, &description); err != nil { t.Fatal(err) }
		if name == passwordCheckSetting {
			found++
			if setting != "8B" || description != "Minimum allowed password length." { t.Fatal("SHOW ALL passwordcheck", setting, description) }
		}
	}
	if rows.Err() != nil || found != 1 { t.Fatal("SHOW ALL cardinality", found, rows.Err()) }
}

func passwordCheckInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, prepared bool) {
	t.Helper()
	run := passwordCheckRun(t, p, c, r, prepared)
	show := passwordCheckShow("8B")
	show.CheckMetadata = func(t *testing.T, rows *sql.Rows) { passwordCheckTextMetadata(t, rows, b.PG, false) }
	run(show)
	current := fixture.Select("SELECT current_setting('passwordcheck.min_password_length')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"8B"}})
	current.CheckMetadata = func(t *testing.T, rows *sql.Rows) { passwordCheckTextMetadata(t, rows, b.PG, true) }
	run(current)
	columns := []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "boot_val", "reset_val"}
	types := make([]string, len(columns))
	for i := range types { types[i] = "TEXT" }
	step := fixture.Select("SELECT name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,boot_val,reset_val FROM pg_settings WHERE name='passwordcheck.min_password_length'", columns, types,
		[][]any{{passwordCheckSetting, "8", "B", "Customized Options", "Minimum allowed password length.", nil, "superuser", "integer", "default", "0", "2147483647", "8", "8"}})
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { passwordCheckTextMetadata(t, rows, b.PG, true) }
	run(step)
	passwordCheckShowAll(t, p, b, c, prepared)
}

func TestReferencePasswordCheckSettings(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			passwordCheckScenario(t, []string{"pc_settings", "pc_held_create"}, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := passwordCheckRun(t, p, c, r, prepared)
				passwordCheckInventory(t, p, b, c, r, prepared)
				run(fixture.Exec("CREATE ROLE pc_settings", 0))
				run(fixture.Select("SELECT current_setting(NULL)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}}))
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer peer.Close()
				run(fixture.Exec("SET passwordcheck.min_password_length=6", 0))
				run(passwordCheckShow("6B"))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, passwordCheckShow("8B"))
				run(fixture.Exec("ALTER ROLE pc_settings PASSWORD 'Abcd12'", 0))
				for _, query := range []string{"CREATE ROLE pc_held_create PASSWORD 'Abcdef12'", "ALTER ROLE pc_settings PASSWORD 'Abcdef12'"} {
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil { t.Fatal("prepare before threshold change", err) }
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					run(fixture.Exec("SET passwordcheck.min_password_length=12", 0))
					fixture.RunStep(t, p.Ctx, held, r, passwordCheckShort(query, 12))
					run(fixture.Exec("SET passwordcheck.min_password_length=6", 0))
					fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(query, 0))
					if err := stmt.Close(); err != nil { t.Fatal(err) }
				}
				run(fixture.Exec("SET passwordcheck.min_password_length=12", 0))
				run(passwordCheckShort("ALTER ROLE pc_settings PASSWORD 'Abcdef12'", 12))
				run(fixture.Exec("ALTER ROLE pc_settings PASSWORD 'Abcdefghij12'", 0))
				passwordCheckPlainCredential(t, p, b, "pc_settings", "Abcdefghij12")
				run(fixture.Exec("SET passwordcheck.min_password_length=0", 0))
				run(passwordCheckShow("0"))
				for _, literal := range []string{"''", "'abcdefgh'"} { run(passwordCheckError("ALTER ROLE pc_settings PASSWORD "+literal, "password must contain both letters and nonletters", "")) }
				run(fixture.Exec("SET passwordcheck.min_password_length=2147483647", 0))
				run(fixture.Select("SELECT setting FROM pg_settings WHERE name='passwordcheck.min_password_length'", []string{"setting"}, []string{"TEXT"}, [][]any{{"2147483647"}}))
				run(fixture.Exec("SET passwordcheck.min_password_length='1kB'", 0))
				run(passwordCheckShow("1kB"))
				run(fixture.Select("SELECT setting FROM pg_settings WHERE name='passwordcheck.min_password_length'", []string{"setting"}, []string{"TEXT"}, [][]any{{"1024"}}))
				for _, bad := range []struct{ value, message, hint string }{
					{"-1", `-1 B is outside the valid range for parameter "passwordcheck.min_password_length" (0 B .. 2147483647 B)`, ""},
					{"2147483648", `invalid value for parameter "passwordcheck.min_password_length": "2147483648"`, "Value exceeds integer range."},
					{"1 MBx", `invalid value for parameter "passwordcheck.min_password_length": "1 MBx"`, `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`},
				} {
					run(fixture.ImplicitError("SET passwordcheck.min_password_length='"+bad.value+"'", fixture.Diagnostic{Code: "22023", Message: bad.message, Hint: bad.hint}))
					run(passwordCheckShow("1kB"))
				}
				run(fixture.Select("SELECT set_config('passwordcheck.min_password_length',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"8B"}}))
				run(fixture.Exec("SET passwordcheck.min_password_length=6", 0))
				run(fixture.Exec("RESET passwordcheck.min_password_length", 0))
				run(passwordCheckShow("8B"))
				run(fixture.Exec(`SET "PASSWORDCHECK.MIN_PASSWORD_LENGTH"=6`, 0))
				run(passwordCheckShow("6B"))
				// Unknown-prefix validation uses the original spelling, unlike
				// the case-insensitive lookup of the known setting above.
				run(fixture.ImplicitError("SET passwordcheck.unknown='x'", fixture.Diagnostic{Code: "42602", Message: `invalid configuration parameter name "passwordcheck.unknown"`, Detail: `"passwordcheck" is a reserved prefix.`}))
				run(fixture.Exec(`SET "PASSWORDCHECK.UNKNOWN"='x'`, 0))
				run(fixture.Select("SELECT current_setting('PASSWORDCHECK.UNKNOWN')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"x"}}))
				// Existing placeholders resolve through the same case-insensitive
				// lookup before absent-name prefix admission.
				run(fixture.Exec("SET passwordcheck.unknown='lowercase-existing'", 0))
				run(fixture.Select("SELECT current_setting('PASSWORDCHECK.UNKNOWN')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"lowercase-existing"}}))
				run(fixture.Exec("RESET passwordcheck.unknown", 0))
				run(fixture.Select("SELECT current_setting('PASSWORDCHECK.UNKNOWN')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{""}}))
				run(fixture.Exec("RESET ALL", 0))
				run(passwordCheckShow("8B"))
				for _, query := range []string{"BEGIN", "SET LOCAL passwordcheck.min_password_length=12", "SAVEPOINT pc_setting_save", "SET LOCAL passwordcheck.min_password_length=6", "ROLLBACK TO pc_setting_save"} { run(fixture.Exec(query, 0)) }
				run(passwordCheckShow("12B"))
				run(fixture.Exec("ROLLBACK", 0))
				run(passwordCheckShow("8B"))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(passwordCheckShow("8B")))
			})
		})
	}
}
