package stuntgres

import (
	"reflect"
	"sync"
	"testing"
)

func TestHBARulesAPI(t *testing.T) {
	for _, construct := range []struct {
		name string
		new  func() *Driver
	}{{"zero", func() *Driver { return new(Driver) }}, {"new", NewDriver}} {
		t.Run(construct.name, func(t *testing.T) {
			for _, early := range []bool{false, true} {
				d := construct.new()
				label, number := "diagnostic label", int32(0)
				input := []HBARule{{RuleNumber: &number, FileName: &label, Database: []string{}, UserName: []string{""}, Options: []string{"secret"}}}
				if early {
					d.SetHBARules(input)
				}
				first, err := d.OpenConnector("exact dsn")
				if err != nil {
					t.Fatal(err)
				}
				holder := d.hbaRules
				if holder == nil || first.(*Connector).engine.HBARules != holder {
					t.Fatal("first engine did not retain stable holder")
				}
				if !early {
					if len(holder.Capture().Rules()) != 0 {
						t.Fatal("unconfigured holder is not empty")
					}
					d.SetHBARules(input)
				}
				captured := holder.Capture()
				want := []HBARule{{RuleNumber: new(int32(0)), FileName: new("diagnostic label"), Database: []string{}, UserName: []string{""}, Options: []string{"secret"}}}
				label, number = "caller changed", 9
				input[0].UserName[0], input[0].Options[0] = "changed", "changed"
				input[0] = HBARule{}
				if !reflect.DeepEqual(captured.Rules(), want) || holder.Capture() != captured {
					t.Fatal("caller mutation changed published facts or capture copied snapshot")
				}
				again, err := d.OpenConnector("exact dsn")
				if err != nil {
					t.Fatal(err)
				}
				other, err := d.OpenConnector("exact dsn ")
				if err != nil {
					t.Fatal(err)
				}
				if again.(*Connector).engine != first.(*Connector).engine || other.(*Connector).engine == first.(*Connector).engine || other.(*Connector).engine.HBARules != holder {
					t.Fatal("exact-DSN engines did not share only their Driver holder")
				}
				independent := construct.new()
				independentConnector, err := independent.OpenConnector("exact dsn")
				if err != nil {
					t.Fatal(err)
				}
				if independentConnector.(*Connector).engine.HBARules == holder || len(independent.hbaRules.Capture().Rules()) != 0 {
					t.Fatal("independent Driver adopted rules")
				}
				d.SetHBARules([]HBARule{{Error: new("replacement")}})
				if d.hbaRules != holder || holder.Capture() == captured || *holder.Capture().Rules()[0].Error != "replacement" {
					t.Fatal("replacement changed holder or froze facts")
				}
				for _, clear := range [][]HBARule{nil, {}} {
					d.SetHBARules(clear)
					if len(holder.Capture().Rules()) != 0 || !reflect.DeepEqual(captured.Rules(), want) {
						t.Fatal("clear failed or mutated retained snapshot")
					}
				}
			}
		})
	}
}

func TestHBARulesAPIConcurrentConnectors(t *testing.T) {
	d := new(Driver)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, name := range []string{"a", "b", "a"} {
		workers.Go(func() {
			<-start
			if _, err := d.OpenConnector(name); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Go(func() {
		<-start
		for range 100 {
			d.SetHBARules([]HBARule{{FileName: new("late")}})
		}
	})
	close(start)
	workers.Wait()
	if len(d.engines) != 2 || d.hbaRules == nil {
		t.Fatal("missing engines/holder")
	}
	for _, e := range d.engines {
		if e.HBARules != d.hbaRules || *e.HBARules.Capture().Rules()[0].FileName != "late" {
			t.Fatal("concurrent engine creation missed publication")
		}
	}
}
