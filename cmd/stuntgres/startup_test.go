package main

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestServerStartupParameters(t *testing.T) {
	t.Run("ExactPairs", func(t *testing.T) {
		body := []byte("user\x00Alice\x00database\x00MixedCase\x00DateStyle\x00ISO, DMY\x00_pq_.one\x00x\x00_pq_.two\x00y\x00\x00")
		pairs, unsupported, err := parseStartupParameters(body)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pairs, map[string]string{"user": "Alice", "database": "MixedCase", "DateStyle": "ISO, DMY", "_pq_.one": "x", "_pq_.two": "y"}) {
			t.Fatalf("startup pairs: %#v", pairs)
		}
		if !reflect.DeepEqual(unsupported, []string{"_pq_.one", "_pq_.two"}) {
			t.Fatalf("unsupported options: %#v", unsupported)
		}
	})
	t.Run("RepeatedPairsLastWins", func(t *testing.T) {
		body := []byte("user\x00Alice\x00user\x00Bob\x00datestyle\x00ISO\x00DateStyle\x00SQL\x00_pq_.one\x00x\x00_pq_.one\x00y\x00\x00")
		pairs, unsupported, err := parseStartupParameters(body)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"user": "Bob", "DateStyle": "SQL", "_pq_.one": "y"}
		if !reflect.DeepEqual(pairs, want) {
			t.Fatalf("pairs: %#v want %#v", pairs, want)
		}
		if !reflect.DeepEqual(unsupported, []string{"_pq_.one", "_pq_.one"}) {
			t.Fatalf("protocol option order: %#v", unsupported)
		}
	})
	t.Run("MalformedPairs", func(t *testing.T) {
		for _, body := range []string{
			"", "user", "user\x00", "user\x00Alice", "user\x00Alice\x00",
			"user\x00Alice\x00\x00extra", "user\x00\xff\x00\x00",
		} {
			_, _, err := parseStartupParameters([]byte(body))
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "08P01" {
				t.Errorf("packet %q: %v", body, err)
			}
		}
	})
	t.Run("BackslashOptions", func(t *testing.T) {
		options := `-c DateStyle=ISO,\ DMY --application_name=a\ b -c timezone=UTC -ctimezone=Europe/Paris --application_name=last\\name`
		defaults, err := startupOptions(options)
		if err != nil {
			t.Fatal(err)
		}
		want := startupDefaults{"datestyle": "ISO, DMY", "application_name": `last\name`, "timezone": "Europe/Paris"}
		if !reflect.DeepEqual(defaults, want) {
			t.Fatalf("defaults: %#v, want %#v", defaults, want)
		}
		defaults, err = startupOptions(" \t\n")
		if err != nil || len(defaults) != 0 {
			t.Fatalf("empty options: %#v %v", defaults, err)
		}
		// Quotes are literal: options are not interpreted as shell source.
		defaults, err = startupOptions(`--application_name='literal'`)
		if err != nil || defaults["application_name"] != "'literal'" {
			t.Fatalf("quoted option: %#v %v", defaults, err)
		}
	})
	t.Run("MalformedOptions", func(t *testing.T) {
		for _, options := range []string{"-c", "--application_name", "-c =x", "-x timezone=UTC", "--=x", "--application_name=x\\"} {
			_, err := startupOptions(options)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "08P01" {
				t.Errorf("options %q: %v", options, err)
			}
		}
	})
	t.Run("ASCIIFold", func(t *testing.T) {
		if got := startupSettingName("DateStyle_Ä"); got != "datestyle_Ä" {
			t.Fatalf("setting name: %q", got)
		}
	})
}
