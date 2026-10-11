package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func formatEncodingBackends(t *testing.T, body func(*fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		body(p, backend, conn)
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func formatEncodingText(query, column, value string, args ...any) fixture.Step {
	return fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func formatEncodingInvalid(name, value, detail string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("invalid value for parameter %q: %q", name, value), Detail: detail}
}

func formatEncodingPermission() fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `permission denied to set parameter "lc_messages"`}
}

func formatEncodingHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, step fixture.Step, before func()) {
	t.Helper()
	count := len(b.Recorder.Operations)
	stmt, err := conn.PrepareContext(p.Ctx, step.Query)
	if err != nil {
		t.Fatal("prepare", step.Query, err)
	}
	defer stmt.Close()
	if len(b.Recorder.Operations) != count {
		t.Fatal("Prepare executed a settings mutation/query")
	}
	if before != nil {
		before()
	}
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: step.Query}, b.Recorder, step)
}
func TestReferenceFormatEncodingSettings(t *testing.T) {
	formatEncodingBackends(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Inferred from release definitions; mismatches expose all captured values
		// through independent subtests, rather than learning expectations at runtime.
		settings := []struct{ name, boot, description, alternate string }{
			{"client_encoding", "UTF8", "Sets the client's character set encoding.", "utf-8"},
			{"lc_messages", "C", "Sets the language in which messages are displayed.", "POSIX"},
			{"lc_monetary", "C", "Sets the locale for formatting monetary amounts.", "POSIX"},
			{"lc_numeric", "C", "Sets the locale for formatting numbers.", "POSIX"},
			{"lc_time", "C", "Sets the locale for formatting date and time values.", "POSIX"},
			{"icu_validation_level", "warning", "Log level for reporting invalid ICU locale strings.", "notice"},
			{"extra_float_digits", "1", "Sets the number of digits displayed for floating-point values.", "-1"},
			{"quote_all_identifiers", "off", "When generating SQL fragments, quote all identifiers.", "on"},
		}
		for _, tc := range settings {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(formatEncodingText("SHOW "+tc.name, tc.name, tc.boot))
				run(formatEncodingText("SELECT current_setting($1)", "current_setting", tc.boot, tc.name))
				if b.PG {
					run(fixture.Select("SELECT name,setting,short_desc FROM pg_settings WHERE name=$1", []string{"name", "setting", "short_desc"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{tc.name, tc.boot, tc.description}}, tc.name))
				}
				want := tc.alternate
				if tc.name == "client_encoding" {
					want = "UTF8"
				}
				step := fixture.Exec("SET "+tc.name+" TO '"+tc.alternate+"'", 0)
				formatEncodingHeld(t, p, b, c, step, func() { run(formatEncodingText("SHOW "+tc.name, tc.name, tc.boot)) })
				run(formatEncodingText("SHOW "+tc.name, tc.name, want))
				run(fixture.Exec("RESET "+tc.name, 0))
				run(fixture.Prepared(formatEncodingText("SELECT set_config($1,$2,false)", "set_config", want, tc.name, tc.alternate)))
				run(fixture.Prepared(fixture.Exec("SET "+tc.name+" TO DEFAULT", 0)))
				run(formatEncodingText("SHOW "+tc.name, tc.name, tc.boot))
			})
		}
		if !b.PG {
			rows, err := c.QueryContext(p.Ctx, "SHOW ALL")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count, previous := 0, ""
			selected := 0
			for rows.Next() {
				var name, value, description string
				if err := rows.Scan(&name, &value, &description); err != nil {
					t.Fatal(err)
				}
				for _, tc := range settings {
					if name == tc.name {
						selected++
						if value != tc.boot || description != tc.description {
							t.Fatalf("native registry %s=(%q,%q), want (%q,%q)", name, value, description, tc.boot, tc.description)
						}
					}
				}
				if strings.ToLower(name) <= previous {
					t.Fatalf("registry order: %s after %s", name, previous)
				}
				previous = strings.ToLower(name)
				count++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if count != 30 {
				t.Fatalf("native registry has %d rows, want 30", count)
			}
			if selected != 8 {
				t.Fatalf("native registry has %d selected definitions, want 8", selected)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ALL", 0))
		for _, tc := range settings {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SHOW "+tc.name, tc.name, tc.boot))
		}
	})
}

func TestReferenceFormatEncodingICU(t *testing.T) {
	formatEncodingBackends(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, value := range []string{"disabled", "debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "DEBUG"} {
			want := value
			if value == "DEBUG" {
				want = "debug2"
			}
			run(fixture.Exec("SET icu_validation_level TO '"+strings.ToUpper(value)+"'", 0))
			run(formatEncodingText("SHOW icu_validation_level", "icu_validation_level", want))
			run(fixture.Prepared(formatEncodingText("SELECT set_config('icu_validation_level',$1,false)", "set_config", want, value)))
		}
		bad := formatEncodingInvalid("icu_validation_level", "bogus", "")
		bad.Hint = "Available values: disabled, debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."
		run(fixture.ImplicitError("SET icu_validation_level TO bogus", bad))
		run(formatEncodingText("SHOW icu_validation_level", "icu_validation_level", "debug2"))
		run(fixture.Exec("RESET icu_validation_level", 0))
		run(formatEncodingText("SHOW icu_validation_level", "icu_validation_level", "warning"))
		formatEncodingHeld(t, p, b, c, fixture.Exec("SET icu_validation_level TO DEFAULT", 0), nil)
	})
}

func TestReferenceFormatEncodingLocales(t *testing.T) {
	formatEncodingBackends(t, func(p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
		for _, name := range []string{"lc_messages", "lc_monetary", "lc_numeric", "lc_time"} {
			for _, value := range []string{"C", "POSIX", ""} {
				t.Run(name+"/"+value, func(t *testing.T) {
					step := fixture.Exec("SET "+name+" TO '"+value+"'", 0)
					if name == "lc_messages" && value == "" {
						step = fixture.ImplicitError(step.Query, formatEncodingInvalid(name, value, ""))
					}
					fixture.RunStep(t, p.Ctx, c, recorder, step)
					if name != "lc_messages" || value != "" {
						fixture.RunStep(t, p.Ctx, c, recorder, formatEncodingText("SHOW "+name, name, value))
					}
				})
			}
			run(fixture.Exec("SET "+name+" TO 'C'", 0))
			for _, value := range []string{"stuntgres_missing_locale_18", "é🙂"} {
				t.Run(name+"/invalid/"+value, func(t *testing.T) {
					notices = nil
					t.Cleanup(func() {
						if t.Failed() {
							t.Logf("oracle ordered locale notice records: %#v", notices)
						}
					})
					detail := "Only C and POSIX locales are supported by stuntgres."
					if b.PG {
						detail = ""
					}
					fixture.RunStep(t, p.Ctx, c, recorder, fixture.ImplicitError("SET "+name+" TO '"+value+"'", formatEncodingInvalid(name, value, detail)))
					var want []fixture.Diagnostic
					if value == "é🙂" {
						want = []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `locale name "é🙂" contains non-ASCII characters`}}
					}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("locale notices ordered records = %#v, want %#v", notices, want)
					}
					fixture.RunStep(t, p.Ctx, c, recorder, formatEncodingText("SHOW "+name, name, "C"))
				})
			}
		}
		for _, value := range []string{"POSIX", "stuntgres_missing_locale_18", "é🙂"} {
			t.Run("outside-LOCAL/"+value, func(t *testing.T) {
				notices = nil
				t.Cleanup(func() {
					if t.Failed() {
						t.Logf("oracle ordered LOCAL notice records: %#v", notices)
					}
				})
				step := fixture.Exec("SET LOCAL lc_time TO '"+value+"'", 0)
				if value != "POSIX" {
					detail := "Only C and POSIX locales are supported by stuntgres."
					if b.PG {
						detail = ""
					}
					step = fixture.ImplicitError(step.Query, formatEncodingInvalid("lc_time", value, detail))
				}
				fixture.RunStep(t, p.Ctx, c, recorder, step)
				want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}}
				if value == "é🙂" {
					want = append(want, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `locale name "é🙂" contains non-ASCII characters`})
				}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("LOCAL notice order=%#v, want %#v", notices, want)
				}
				fixture.RunStep(t, p.Ctx, c, recorder, formatEncodingText("SHOW lc_time", "lc_time", "C"))
			})
		}
		// Installed-locale capability witness is not a universal validity claim.
		// A missing C.UTF-8 is exposed as an oracle observation, not a skipped test.
		if b.PG {
			t.Run("installed-C.UTF-8", func(t *testing.T) {
				rows, err := c.QueryContext(p.Ctx, "SELECT collname,collcollate FROM pg_collation WHERE collprovider='c' ORDER BY collname")
				if err != nil {
					t.Fatal(err)
				}
				var inventory [][2]string
				for rows.Next() {
					var name string
					var locale sql.NullString
					if err := rows.Scan(&name, &locale); err != nil {
						t.Fatal(err)
					}
					inventory = append(inventory, [2]string{name, locale.String})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				for _, name := range []string{"lc_messages", "lc_monetary", "lc_numeric", "lc_time"} {
					t.Run(name, func(t *testing.T) {
						_, err := c.ExecContext(p.Ctx, "SET "+name+" TO 'C.UTF-8'")
						if err != nil {
							t.Fatalf("pinned C.UTF-8 unavailable for %s: diagnostic=%#v libc collation inventory=%#v; root must capture locale -a in pinned container", name, fixture.ExtractDiagnostic(t, err), inventory)
						}
						fixture.RunStep(t, p.Ctx, c, recorder, formatEncodingText("SHOW "+name, name, "C.UTF-8"))
						fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec("SET "+name+" TO 'C'", 0))
					})
				}
			})
		} else {
			for _, name := range []string{"lc_messages", "lc_monetary", "lc_numeric", "lc_time"} {
				run(fixture.ImplicitError("SET "+name+" TO 'C.UTF-8'", formatEncodingInvalid(name, "C.UTF-8", "Only C and POSIX locales are supported by stuntgres.")))
				run(formatEncodingText("SHOW "+name, name, "C"))
			}
		}
		// Warning suppression must not suppress the primary error or publication fence.
		run(fixture.Exec("SET client_min_messages TO error", 0))
		notices = nil
		detail := "Only C and POSIX locales are supported by stuntgres."
		if b.PG {
			detail = ""
		}
		run(fixture.ImplicitError("SET lc_time TO 'é🙂'", formatEncodingInvalid("lc_time", "é🙂", detail)))
		if len(notices) != 0 {
			t.Fatalf("filtered warning records=%#v", notices)
		}
		run(formatEncodingText("SHOW lc_time", "lc_time", "C"))
	})
}
