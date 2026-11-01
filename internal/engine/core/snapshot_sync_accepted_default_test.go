package core_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

// Inspect capture on the genuine accepted portal, not a synthetic Binding with
// a hand-selected demand bit. The token must use that pre-execution backing.
func TestSnapshotSyncAcceptedDefaultSelectedDemand(t *testing.T) {
	for _, language := range []string{"standard", "quoted", "pl"} {
		for _, exporting := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exporting-predecessor=%v", language, exporting), func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				declaration := func(export bool) string {
					body := "'quiet'::text"
					if export {
						body = "pg_export_snapshot()"
					}
					if export && language != "pl" {
						body = "snapshot_accepted_export()"
					}
					switch language {
					case "quoted":
						return "LANGUAGE SQL AS $$SELECT " + body + "$$"
					case "pl":
						return "LANGUAGE plpgsql AS $$BEGIN RETURN " + body + "; END$$"
					default:
						return "RETURN " + body
					}
				}
				for _, query := range []string{
					"CREATE TABLE snapshot_accepted_unread(id integer PRIMARY KEY)",
					"CREATE FUNCTION snapshot_accepted_export() RETURNS text STABLE LANGUAGE plpgsql AS $$BEGIN RETURN pg_export_snapshot(); END$$",
					"CREATE FUNCTION snapshot_accepted() RETURNS text STABLE " + declaration(exporting),
					"CREATE TABLE snapshot_accepted_rows(v text DEFAULT snapshot_accepted())",
					"BEGIN",
				} {
					enginetest.SessionSQL(t, s, query)
				}
				defer enginetest.SessionSQL(t, s, "ROLLBACK")
				unread := enginetest.CatalogRef(t, e, e.CatalogRead(), "snapshot_accepted_unread").Address
				cycle := s.NewProtocolCycle()
				command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
				command(func(scope *core.StatementScope) error {
					_, err := scope.ParseProtocol("snapshot", "INSERT INTO snapshot_accepted_rows DEFAULT VALUES RETURNING v", nil)
					return err
				})
				command(func(scope *core.StatementScope) error {
					_, err := scope.DescribeProtocolStatement("snapshot")
					return err
				})
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal("old", "snapshot", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
				if core.EngineExportCount(e) != 0 {
					t.Fatal("Parse/Describe/Bind exported a snapshot")
				}
				binding := core.PayloadOf(s).PreparedSQL.Binding("old")
				if binding == nil || binding.Folds == nil {
					t.Fatal("missing actual accepted binding")
				}
				owner := reflect.ValueOf(binding).Elem().FieldByName("defaults")
				if owner.IsNil() || owner.Elem().FieldByName("entries").Len() != 1 {
					t.Fatal("fixture omitted genuine source-bound DEFAULT ownership")
				}
				artifact := owner.Elem().FieldByName("entries").Index(0).Elem()
				ownerPointer, rootPointer := owner.Pointer(), artifact.FieldByName("root").Pointer()
				folds := binding.Folds
				accepted := *folds
				entries := slices.Clone(folds.Entries)
				enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION snapshot_accepted() RETURNS text STABLE "+declaration(!exporting))
				selectedExport := exporting
				if language == "pl" {
					selectedExport = !exporting
				}
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal("old", 0, func(fields []session.ProtocolField) error {
						if !selectedExport {
							return nil
						}
						if len(fields) != 1 || fields[0].Null {
							return fmt.Errorf("exporting accepted DEFAULT lost its text result")
						}
						roots, _, snapshot, frontier := core.ScopeExportCapture(scope)
						saved, value, cut := core.EngineExportCapture(e, string(fields[0].Bytes))
						if roots == nil || roots[unread].Storage == nil || snapshot == nil || reflect.ValueOf(roots).Pointer() != reflect.ValueOf(saved).Pointer() || snapshot != value || cut != frontier {
							return fmt.Errorf("selected accepted executable did not use its acquired immutable pair")
						}
						return nil
					}, nil, nil, core.CopyProtocolHooks{})
					return err
				})
				wantCount := 0
				if selectedExport {
					wantCount = 1
				}
				if core.EngineExportCount(e) != wantCount {
					t.Fatal("accepted executable selection exported the wrong number of snapshots")
				}
				if owner.Pointer() != ownerPointer || artifact.FieldByName("root").Pointer() != rootPointer || folds.Generation != accepted.Generation || !reflect.DeepEqual(folds.Entries, entries) {
					t.Fatal("capture changed accepted owner, source root or generation")
				}
			})
		}
	}
}
