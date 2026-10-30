package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func hostReplayDefinition(runtime *hostNativeRuntime) stuntgres.HostLanguageDefinition {
	d := runtime.definition()
	d.Programs = append(d.Programs, stuntgres.HostProgramDefinition{ID: "default-map-v1", Version: "1", Inputs: []stuntgres.HostType{stuntgres.HostHstore}, Result: stuntgres.HostHstore, Execute: func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		entries, ok := args[0].Value.([]stuntgres.HostHstoreEntry)
		if !ok {
			return stuntgres.HostObject{}, fmt.Errorf("actual surviving default entry list required")
		}
		m := make(stuntgres.HostHstoreMap, len(entries)+1)
		for _, entry := range entries {
			m[entry.Key] = stuntgres.HostHstoreValue{Text: entry.Value, Null: entry.Null}
		}
		n, err := strconv.Atoi(m["a"].Text)
		if err != nil {
			return stuntgres.HostObject{}, err
		}
		m["a"] = stuntgres.HostHstoreValue{Text: strconv.Itoa(n * 2)}
		m["d"] = stuntgres.HostHstoreValue{Text: "second"}
		return stuntgres.HostObject{Value: m}, nil
	}})
	return d
}
func hostReplayAbsenceResults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, lang := range []string{"host_replay_origin", "host_replay_before"} {
		hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname=$1", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}, lang)
	}
	for _, lang := range []string{"host_replay_after", "go_host"} {
		hostRows(t, p, b, c, "SELECT t.trffromsql=0 AS no_from,t.trftosql<>0 AS has_to FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname=$1", []string{"no_from", "has_to"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, lang)
	}
	hostBag(t, p, b, c, "host_replay_changed('a=>7,b=>NULL'::hstore)", [][]any{{"a", "14"}, {"b", nil}, {"d", "second"}})
	hostBag(t, p, b, c, "(SELECT v FROM host_replay_view)", [][]any{{"a", "14"}, {"b", nil}, {"d", "second"}})
	hostNativeError(t, p.Ctx, c, "CREATE FUNCTION host_replay_unavailable(v hstore) RETURNS hstore LANGUAGE host_replay_after AS 'text-default-v1'", "unavailable host TO SQL direction")
	hostError(t, p, c, "CREATE FUNCTION host_replay_pair_missing(v hstore) RETURNS hstore LANGUAGE host_replay_before TRANSFORM FOR TYPE hstore AS 'default-map-v1'", fixture.ControlsReferenceDiagnostic("42704", "transform for type hstore language \"host_replay_before\" does not exist"))
	hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname IN ('go_host_from_hstore','go_host_default_to','host_replay_erased','host_replay_unavailable','host_replay_pair_missing')", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
}
func hostReplayAbsenceControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	definition := hostReplayDefinition(new(hostNativeRuntime))
	source, b := hostOpenNative(t, &definition)
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE EXTENSION hstore", "CREATE LANGUAGE host_replay_before HANDLER go_host_call_handler", "CREATE FUNCTION host_replay_erased(v hstore) RETURNS hstore LANGUAGE go_host AS 'text-default-v1'", "DROP FUNCTION go_host_from_hstore(internal) CASCADE", "CREATE LANGUAGE host_replay_after HANDLER go_host_call_handler", "DROP FUNCTION go_host_default_to(internal) CASCADE", "ALTER LANGUAGE go_host RENAME TO host_replay_origin", "CREATE LANGUAGE go_host HANDLER go_host_call_handler", "ALTER FUNCTION go_host_call_handler() RENAME TO host_replay_handler", "ALTER FUNCTION go_host_to_hstore(internal) RENAME TO host_replay_to", "CREATE FUNCTION host_replay_changed(v hstore) RETURNS hstore LANGUAGE host_replay_after TRANSFORM FOR TYPE hstore AS 'default-map-v1'", "CREATE TABLE host_replay_data(v hstore DEFAULT 'a=>7,b=>NULL'::hstore)", "CREATE VIEW host_replay_view AS SELECT host_replay_changed(v) AS v FROM host_replay_data"} {
		hostExec(t, p, b, c, q)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_replay_data DEFAULT VALUES", 1))
	hostReplayAbsenceResults(t, p, b, c)
	var stream bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, source, &stream); err != nil {
		t.Fatalf("absence source dump: %v", err)
	}
	for round := range 2 {
		fresh := hostReplayDefinition(new(hostNativeRuntime))
		destination, recorded := hostOpenNative(t, &fresh)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatalf("absence fresh driver replay round %d: %v", round, err)
		}
		current, err := recorded.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		hostReplayAbsenceResults(t, p, recorded, current)
		current.Close()
		var next bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, destination, &next); err != nil {
			t.Fatalf("absence round %d dump: %v", round, err)
		}
		stream.Reset()
		stream.Write(next.Bytes())
	}
	hostReplayCorruptAbsences(t, p, b, definition, stream.Bytes())
	hostReplayNamespaceControls(t, p, b)
	// Dropped registered baseline language and handler remain absent after two
	// matching fresh registrations; the original spelling may be ordinary SQL.
	hostExec(t, p, b, c, "DROP FUNCTION host_replay_handler() CASCADE")
	hostExec(t, p, b, c, "CREATE FUNCTION go_host_call_handler() RETURNS integer LANGUAGE SQL AS 'SELECT 73'")
	stream.Reset()
	if err := stuntgres.Dump(p.Ctx, source, &stream); err != nil {
		t.Fatalf("dropped handler source dump: %v", err)
	}
	for round := range 2 {
		fresh := hostReplayDefinition(new(hostNativeRuntime))
		destination, recorded := hostOpenNative(t, &fresh)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatalf("dropped handler fresh driver replay round %d: %v", round, err)
		}
		current, err := recorded.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		hostRows(t, p, recorded, current, "SELECT count(*) AS v FROM pg_language WHERE lanname IN ('host_replay_origin','host_replay_before','host_replay_after','go_host')", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostRows(t, p, recorded, current, "SELECT go_host_call_handler() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}})
		current.Close()
		var next bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, destination, &next); err != nil {
			t.Fatalf("dropped handler round %d dump: %v", round, err)
		}
		stream.Reset()
		stream.Write(next.Bytes())
	}
}

type hostAbsenceWire struct {
	Provider, ABI, Language string
	Type                    string
	SQLName                 struct{ Schema, Name string }
}
type hostAbsencesWire struct{ Pairs []hostAbsenceWire }

func hostReplayCorruptAbsences(t *testing.T, p *fixture.Pair, b fixture.Backend, definition stuntgres.HostLanguageDefinition, data []byte) {
	t.Helper()
	const prefix = "-- stuntgres-host-pair-absence-v1 "
	lines := strings.Split(string(data), "\n")
	index := -1
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("absence record missing")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(lines[index], prefix))
	if err != nil {
		t.Fatal(err)
	}
	var record hostAbsencesWire
	if err = json.Unmarshal(raw, &record); err != nil || len(record.Pairs) == 0 {
		t.Fatal("missing distinguishing absence", err)
	}
	for _, kind := range []string{"duplicate-pair", "orphan-language", "incompatible-abi", "duplicate-boundary", "missing-boundary", "orphan-boundary"} {
		t.Run(kind, func(t *testing.T) {
			candidate := append([]string(nil), lines...)
			copy := record
			copy.Pairs = append([]hostAbsenceWire(nil), record.Pairs...)
			switch kind {
			case "duplicate-pair":
				copy.Pairs = append(copy.Pairs, copy.Pairs[0])
			case "orphan-language":
				copy.Pairs[0].Language = "absent_replay_language"
			case "incompatible-abi":
				copy.Pairs[0].ABI = "incompatible"
			}
			encoded, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			candidate[index] = prefix + base64.RawStdEncoding.EncodeToString(encoded)
			switch kind {
			case "duplicate-boundary":
				candidate[index] += "\n" + candidate[index]
			case "missing-boundary":
				candidate[index] = ""
			case "orphan-boundary":
				if index+1 >= len(candidate) {
					t.Fatal("boundary lacks real owner")
				}
				candidate[index+1] = ""
			}
			targetDB, recorded := hostOpenNative(t, &definition)
			target, err := recorded.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			hostExec(t, p, recorded, target, "CREATE TABLE host_absence_sentinel(v integer)")
			fixture.RunStep(t, p.Ctx, target, recorded.Recorder, fixture.Exec("INSERT INTO host_absence_sentinel VALUES (79)", 1))
			held, err := target.PrepareContext(p.Ctx, "SELECT v FROM host_absence_sentinel")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if err = stuntgres.Restore(p.Ctx, targetDB, strings.NewReader(strings.Join(candidate, "\n"))); err == nil {
				t.Fatal("invalid absence record published target")
			}
			var got int
			if err = held.QueryRowContext(p.Ctx).Scan(&got); err != nil || got != 79 {
				t.Fatalf("failed absence replay changed held target %d/%v", got, err)
			}
		})
	}
}

func hostReplayNamespaceControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	definition := hostReplayDefinition(new(hostNativeRuntime))
	definition.HandlerName.Schema = "host_replay_schema"
	for i := range definition.Conversions {
		definition.Conversions[i].FromName.Schema = "host_replay_directions"
		definition.Conversions[i].ToName.Schema = "host_replay_directions"
	}
	source, b := hostOpenNative(t, &definition)
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE EXTENSION hstore", "CREATE FUNCTION host_namespace_changed(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore AS 'increment-v1'", "ALTER SCHEMA host_replay_schema RENAME TO host_replay_schema_renamed", "ALTER SCHEMA host_replay_directions RENAME TO host_replay_directions_renamed"} {
		hostExec(t, p, b, c, q)
	}
	var stream bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, source, &stream); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		fresh := hostReplayDefinition(new(hostNativeRuntime))
		fresh.HandlerName.Schema = definition.HandlerName.Schema
		for i := range fresh.Conversions {
			fresh.Conversions[i].FromName.Schema = "host_replay_directions"
			fresh.Conversions[i].ToName.Schema = "host_replay_directions"
		}
		destination, recorded := hostOpenNative(t, &fresh)
		if err = stuntgres.Restore(p.Ctx, destination, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatal(err)
		}
		current, err := recorded.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		hostBag(t, p, recorded, current, "host_namespace_changed('a=>7,b=>NULL'::hstore)", [][]any{{"a", "8"}, {"b", nil}, {"c", "3"}})
		hostRows(t, p, recorded, current, "SELECT count(*) AS v FROM pg_namespace WHERE nspname IN ('host_replay_schema','host_replay_directions')", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostRows(t, p, recorded, current, "SELECT count(*) AS v FROM pg_namespace WHERE nspname IN ('host_replay_schema_renamed','host_replay_directions_renamed')", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}})
		current.Close()
		var next bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, destination, &next); err != nil {
			t.Fatal(err)
		}
		stream.Reset()
		stream.Write(next.Bytes())
	}
}
