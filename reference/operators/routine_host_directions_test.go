package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func (runtime *hostNativeRuntime) directionDefinition(direction string) stuntgres.HostLanguageDefinition {
	d := runtime.definition()
	d.Provider = "reference-go-host-" + direction
	d.Name = "go_host_" + direction
	d.HandlerName.Name = d.Name + "_call_handler"
	for i := range d.Conversions {
		d.Conversions[i].FromName.Name = d.Name + "_from_" + d.Conversions[i].ID
		d.Conversions[i].ToName.Name = d.Name + "_to_" + d.Conversions[i].ID
	}
	execute := func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		if direction == "from" || direction == "unavailable" {
			m, err := hostMapObject(args)
			if err != nil {
				return stuntgres.HostObject{}, err
			}
			entries := make([]stuntgres.HostHstoreEntry, 0, len(m))
			for k, v := range m {
				if k == "a" {
					v.Text = "9"
				}
				entries = append(entries, stuntgres.HostHstoreEntry{Key: k, Value: v.Text, Null: v.Null})
			}
			return stuntgres.HostObject{Value: entries}, nil
		}
		entries, ok := args[0].Value.([]stuntgres.HostHstoreEntry)
		if !ok {
			return stuntgres.HostObject{}, errors.New("host: missing actual default entry list")
		}
		if direction == "none" {
			return stuntgres.HostObject{Value: slices.Clone(entries)}, nil
		}
		m := make(stuntgres.HostHstoreMap, len(entries))
		for _, entry := range entries {
			if entry.Key == "a" {
				entry.Value = "9"
			}
			m[entry.Key] = stuntgres.HostHstoreValue{Text: entry.Value, Null: entry.Null}
		}
		return stuntgres.HostObject{Value: m}, nil
	}
	p := stuntgres.HostProgramDefinition{ID: "direction-v1", Version: "1", Inputs: []stuntgres.HostType{stuntgres.HostHstore}, Result: stuntgres.HostHstore, InputConversions: []string{"map"}, ResultConversion: "map", Execute: execute}
	switch direction {
	case "from", "unavailable":
		d.Conversions[0].ToName = stuntgres.HostName{}
		d.Conversions[0].ToSQL = nil
	case "to":
		d.Conversions[0].FromName = stuntgres.HostName{}
		d.Conversions[0].FromSQL = nil
	case "none":
		d.Conversions = d.Conversions[1:]
		p.InputConversions = []string{""}
		p.ResultConversion = ""
	}
	if direction == "unavailable" {
		d.Defaults[0].ToID = ""
	}
	d.Programs = []stuntgres.HostProgramDefinition{p}
	return d
}
func registerHostNative(d *stuntgres.Driver, runtime *hostNativeRuntime) error {
	if err := d.RegisterHostLanguage(runtime.definition()); err != nil {
		return err
	}
	for _, direction := range []string{"from", "to", "none", "unavailable"} {
		if err := d.RegisterHostLanguage(runtime.directionDefinition(direction)); err != nil {
			return err
		}
	}
	return nil
}
func hostNativeDirections(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, direction := range []string{"from", "to", "none"} {
		lang := "host_" + direction
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS "+lang+" CASCADE")()
		hostExec(t, p, b, c, "CREATE LANGUAGE "+lang+" HANDLER go_host_"+direction+"_call_handler")
		hostExec(t, p, b, c, "CREATE FUNCTION host_direction_"+direction+"(v hstore) RETURNS hstore LANGUAGE "+lang+" AS 'direction-v1'")
		a := "9"
		count := int64(1)
		if direction == "none" {
			a = "7"
			count = 0
		}
		hostBag(t, p, b, c, "host_direction_"+direction+"('a=>7,b=>NULL'::hstore)", [][]any{{"a", a}, {"b", nil}})
		hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname=$1", []string{"v"}, []string{"INT8"}, [][]any{{count}}, lang)
	}
	hostNativeError(t, p.Ctx, c, "CREATE FUNCTION host_missing_direction(v hstore) RETURNS hstore LANGUAGE go_host_unavailable AS 'direction-v1'", "unavailable host TO SQL direction")
	hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='host_missing_direction'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
	hostError(t, p, c, "CREATE FUNCTION host_unavailable(v hstore) RETURNS hstore LANGUAGE absent_host AS 'increment-v1'", fixture.ControlsReferenceDiagnostic("42704", `language "absent_host" does not exist`))
}
