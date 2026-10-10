package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func formatEncodingBackends(t *testing.T, body func(*referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		conn, err := backend.db.Conn(p.ctx)
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

func formatEncodingText(query, column, value string, args ...any) referenceStep {
	return referenceSelect(query, []string{column}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func formatEncodingInvalid(name, value, detail string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("invalid value for parameter %q: %q", name, value), Detail: detail}
}

func formatEncodingPermission() referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `permission denied to set parameter "lc_messages"`}
}

func formatEncodingHeld(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn, step referenceStep, before func()) {
	t.Helper()
	count := len(b.recorder.operations)
	stmt, err := conn.PrepareContext(p.ctx, step.query)
	if err != nil {
		t.Fatal("prepare", step.query, err)
	}
	defer stmt.Close()
	if len(b.recorder.operations) != count {
		t.Fatal("Prepare executed a settings mutation/query")
	}
	if before != nil {
		before()
	}
	referenceRunStep(t, p.ctx, referenceHeldStatement{statement: stmt, query: step.query}, b.recorder, step)
}
func TestReferenceFormatEncodingSettings(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
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
		}
		for _, tc := range settings {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				run(formatEncodingText("SHOW "+tc.name, tc.name, tc.boot))
				run(formatEncodingText("SELECT current_setting($1)", "current_setting", tc.boot, tc.name))
				if b.pg {
					run(referenceSelect("SELECT name,setting,short_desc FROM pg_settings WHERE name=$1", []string{"name", "setting", "short_desc"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{tc.name, tc.boot, tc.description}}, tc.name))
				}
				want := tc.alternate
				if tc.name == "client_encoding" {
					want = "UTF8"
				}
				step := referenceExec("SET "+tc.name+" TO '"+tc.alternate+"'", 0)
				formatEncodingHeld(t, p, b, c, step, func() { run(formatEncodingText("SHOW "+tc.name, tc.name, tc.boot)) })
				run(formatEncodingText("SHOW "+tc.name, tc.name, want))
				run(referenceExec("RESET "+tc.name, 0))
				run(referencePrepared(formatEncodingText("SELECT set_config($1,$2,false)", "set_config", want, tc.name, tc.alternate)))
				run(referencePrepared(referenceExec("SET "+tc.name+" TO DEFAULT", 0)))
				run(formatEncodingText("SHOW "+tc.name, tc.name, tc.boot))
			})
		}
		if !b.pg {
			rows, err := c.QueryContext(p.ctx, "SHOW ALL")
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
			if count != 26 {
				t.Fatalf("native registry has %d rows, want 26", count)
			}
			if selected != 7 {
				t.Fatalf("native registry has %d selected definitions, want 7", selected)
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("RESET ALL", 0))
		for _, tc := range settings {
			referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW "+tc.name, tc.name, tc.boot))
		}
	})
}

func TestReferenceFormatEncodingICU(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, value := range []string{"disabled", "debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "DEBUG"} {
			want := value
			if value == "DEBUG" {
				want = "debug2"
			}
			run(referenceExec("SET icu_validation_level TO '"+strings.ToUpper(value)+"'", 0))
			run(formatEncodingText("SHOW icu_validation_level", "icu_validation_level", want))
			run(referencePrepared(formatEncodingText("SELECT set_config('icu_validation_level',$1,false)", "set_config", want, value)))
		}
		bad := formatEncodingInvalid("icu_validation_level", "bogus", "")
		bad.Hint = "Available values: disabled, debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."
		run(referenceImplicitError("SET icu_validation_level TO bogus", bad))
		run(formatEncodingText("SHOW icu_validation_level", "icu_validation_level", "debug2"))
		run(referenceExec("RESET icu_validation_level", 0))
		run(formatEncodingText("SHOW icu_validation_level", "icu_validation_level", "warning"))
		formatEncodingHeld(t, p, b, c, referenceExec("SET icu_validation_level TO DEFAULT", 0), nil)
	})
}

func TestReferenceFormatEncodingLocales(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, _ *sql.Conn) {
		var notices []referenceDiagnostic
		_, c, recorder := noticeReferencePool(t, p, b.pg, &notices)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, recorder, s) }
		for _, name := range []string{"lc_messages", "lc_monetary", "lc_numeric", "lc_time"} {
			for _, value := range []string{"C", "POSIX", ""} {
				t.Run(name+"/"+value, func(t *testing.T) {
					step := referenceExec("SET "+name+" TO '"+value+"'", 0)
					if name == "lc_messages" && value == "" {
						step = referenceImplicitError(step.query, formatEncodingInvalid(name, value, ""))
					}
					referenceRunStep(t, p.ctx, c, recorder, step)
					if name != "lc_messages" || value != "" {
						referenceRunStep(t, p.ctx, c, recorder, formatEncodingText("SHOW "+name, name, value))
					}
				})
			}
			run(referenceExec("SET "+name+" TO 'C'", 0))
			for _, value := range []string{"stuntgres_missing_locale_18", "é🙂"} {
				t.Run(name+"/invalid/"+value, func(t *testing.T) {
					notices = nil
					t.Cleanup(func() {
						if t.Failed() {
							t.Logf("oracle ordered locale notice records: %#v", notices)
						}
					})
					detail := "Only C and POSIX locales are supported by stuntgres."
					if b.pg {
						detail = ""
					}
					referenceRunStep(t, p.ctx, c, recorder, referenceImplicitError("SET "+name+" TO '"+value+"'", formatEncodingInvalid(name, value, detail)))
					var want []referenceDiagnostic
					if value == "é🙂" {
						want = []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `locale name "é🙂" contains non-ASCII characters`}}
					}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("locale notices ordered records = %#v, want %#v", notices, want)
					}
					referenceRunStep(t, p.ctx, c, recorder, formatEncodingText("SHOW "+name, name, "C"))
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
				step := referenceExec("SET LOCAL lc_time TO '"+value+"'", 0)
				if value != "POSIX" {
					detail := "Only C and POSIX locales are supported by stuntgres."
					if b.pg {
						detail = ""
					}
					step = referenceImplicitError(step.query, formatEncodingInvalid("lc_time", value, detail))
				}
				referenceRunStep(t, p.ctx, c, recorder, step)
				want := []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}}
				if value == "é🙂" {
					want = append(want, referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `locale name "é🙂" contains non-ASCII characters`})
				}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("LOCAL notice order=%#v, want %#v", notices, want)
				}
				referenceRunStep(t, p.ctx, c, recorder, formatEncodingText("SHOW lc_time", "lc_time", "C"))
			})
		}
		// Installed-locale capability witness is not a universal validity claim.
		// A missing C.UTF-8 is exposed as an oracle observation, not a skipped test.
		if b.pg {
			t.Run("installed-C.UTF-8", func(t *testing.T) {
				rows, err := c.QueryContext(p.ctx, "SELECT collname,collcollate FROM pg_collation WHERE collprovider='c' ORDER BY collname")
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
						_, err := c.ExecContext(p.ctx, "SET "+name+" TO 'C.UTF-8'")
						if err != nil {
							t.Fatalf("pinned C.UTF-8 unavailable for %s: diagnostic=%#v libc collation inventory=%#v; root must capture locale -a in pinned container", name, referenceExtractDiagnostic(t, err), inventory)
						}
						referenceRunStep(t, p.ctx, c, recorder, formatEncodingText("SHOW "+name, name, "C.UTF-8"))
						referenceRunStep(t, p.ctx, c, recorder, referenceExec("SET "+name+" TO 'C'", 0))
					})
				}
			})
		} else {
			for _, name := range []string{"lc_messages", "lc_monetary", "lc_numeric", "lc_time"} {
				run(referenceImplicitError("SET "+name+" TO 'C.UTF-8'", formatEncodingInvalid(name, "C.UTF-8", "Only C and POSIX locales are supported by stuntgres.")))
				run(formatEncodingText("SHOW "+name, name, "C"))
			}
		}
		// Warning suppression must not suppress the primary error or publication fence.
		run(referenceExec("SET client_min_messages TO error", 0))
		notices = nil
		detail := "Only C and POSIX locales are supported by stuntgres."
		if b.pg {
			detail = ""
		}
		run(referenceImplicitError("SET lc_time TO 'é🙂'", formatEncodingInvalid("lc_time", "é🙂", detail)))
		if len(notices) != 0 {
			t.Fatalf("filtered warning records=%#v", notices)
		}
		run(formatEncodingText("SHOW lc_time", "lc_time", "C"))
	})
}
