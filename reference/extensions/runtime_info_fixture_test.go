package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)
func loadSQLLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }


func runtimeSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, columns, types []string, rows [][]any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(query, columns, types, rows, args...)
		s.Prepared = prepared
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			ct, err := r.ColumnTypes(); if err != nil { t.Fatal(err) }
			for i, column := range ct {
				var want reflect.Type
				switch types[i] { case "TEXT": want = reflect.TypeOf(""); case "BOOL": want = reflect.TypeOf(false); case "INT8": want = reflect.TypeOf(int64(0)); default: continue }
				if column.ScanType() != want { t.Errorf("%s ScanType=%v want %v", columns[i], column.ScanType(), want) }
				inquiry := strings.Contains(query, "pg_current_logfile(") || strings.Contains(query, "pg_jit_available(") || strings.Contains(query, "pg_numa_available(") || strings.Contains(query, "pg_get_loaded_modules(")
				if nullable, known := column.Nullable(); inquiry && (nullable || known) { t.Errorf("inquiry %s Nullable=(%v,%v), want (false,false)", columns[i], nullable, known) }
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func runtimeHeldSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement, columns, types []string, rows [][]any, args ...any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, columns, types, rows, args...))
}

func runtimeFormatError(format string) fixture.Diagnostic {
	d := loadDiagnostic("22023", "log format \""+format+"\" is not supported")
	d.Hint = "The supported log formats are \"stderr\", \"csvlog\", and \"jsonlog\"."
	return d
}

func runtimeDenied(name string) fixture.Diagnostic { return loadDiagnostic("42501", "permission denied for function "+name) }

func runtimeModules(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	if b.PG {
		loadExec(t, p, b, c, "LOAD 'plpgsql'")
		loadExec(t, p, b, c, "LOAD 'plpgsql'")
		// The pinned oracle supplies PG_VERSION, including its Debian vendor suffix.
		runtimeSelect(t, p, b, c, "SELECT * FROM pg_get_loaded_modules() WHERE module_name = 'plpgsql'", []string{"module_name", "version", "file_name"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"plpgsql", "18.6 (Debian 18.6-1.pgdg12+2)", "plpgsql.so"}})
	} else {
		loadError(t, p, b, c, "LOAD 'plpgsql'", loadDiagnostic("0A000", loadUnsupported), false)
		runtimeSelect(t, p, b, c, "SELECT * FROM pg_get_loaded_modules()", []string{"module_name", "version", "file_name"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
	}
}
