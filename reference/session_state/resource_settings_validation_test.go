package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type resourceMutation struct { name, input, show, raw, invalid, low, high string }

var resourceMutations = []resourceMutation{
	{"backend_flush_after", "256", "2MB", "256", "257", "-1", "257"},
	{"effective_io_concurrency", "0", "0", "0", "1001", "-1", "1001"},
	{"file_copy_method", "COPY", "copy", "copy", "bad", "", ""},
	{"hash_mem_multiplier", "1.25", "1.25", "1.25", "0", "0", "1001"},
	{"io_combine_limit", "128", "1MB", "128", "0", "0", "129"},
	{"logical_decoding_work_mem", "1024kB", "1MB", "1024", "63", "63", "2147483648"},
	{"maintenance_io_concurrency", "32", "32", "32", "1001", "-1", "1001"},
	{"maintenance_work_mem", "1024kB", "1MB", "1024", "63", "63", "2147483648"},
	{"max_parallel_maintenance_workers", "0", "0", "0", "1025", "-1", "1025"},
	{"max_parallel_workers", "0", "0", "0", "1025", "-1", "1025"},
	{"max_parallel_workers_per_gather", "3", "3", "3", "1025", "-1", "1025"},
	{"max_stack_depth", "1024", "1MB", "1024", "99", "99", "2147483648"},
	{"parallel_leader_participation", "off", "off", "off", "o", "", ""},
	{"temp_buffers", "2048", "16MB", "2048", "99", "99", "1073741824"},
	{"temp_file_limit", "1024", "1MB", "1024", "-2", "-2", "2147483648"},
	{"vacuum_buffer_usage_limit", "0", "0", "0", "127", "-1", "16777217"},
	{"work_mem", "1024kB", "1MB", "1024", "1", "1", "2147483648"},
}

func resourceDefinition(name string) resourceSetting {
	for _, d := range resourceSettings { if d.name == name { return d } }
	panic("missing independent resource expectation: "+name)
}

func resourceValidationError(name, input string) fixture.Diagnostic {
	d := resourceDefinition(name)
	if d.kind == "bool" { return physicalDiagnostic("22023", fmt.Sprintf("parameter %q requires a Boolean value",name)) }
	e := resourceInvalid(name,input)
	if d.kind == "enum" { e.Hint = "Available values: copy, clone."; return e }
	if input == "bogus" { return e }
	if input == "2147483648" { e.Hint = "Value exceeds integer range."; return e }
	if name == "vacuum_buffer_usage_limit" && input == "127" {
		e.Message = `invalid value for parameter "vacuum_buffer_usage_limit": 127`
		e.Detail = `"vacuum_buffer_usage_limit" must be 0 or between 128 kB and 16777216 kB.`
		return e
	}
	unit := ""
	if d.unit != "" { unit = " "+d.unit }
	return physicalDiagnostic("22023",fmt.Sprintf("%s%s is outside the valid range for parameter %q (%s%s .. %s%s)",input,unit,name,d.min,unit,d.max,unit))
}

func TestReferenceResourceSettingsValidation(t *testing.T) {
	for _, m := range resourceMutations {
		t.Run(m.name, func(t *testing.T) {
			physicalCases(t,nil,func(s *physicalCase) {
				peer := s.lockPeer()
				for _, prepared := range []bool{false,true} {
					for _, rows := range []bool{false,true} {
						step := fixture.Exec("SET "+m.name+"="+physicalLiteral(m.input),0)
						step.Prepared, step.SelectRows = prepared, rows
						s.run(step)
						s.resourceValue(m.name,m.show,m.raw,"session",prepared)
					}
					step := fixture.Select("SELECT set_config($1,$2,false)",[]string{"set_config"},[]string{"TEXT"},[][]any{{m.show}},m.name,m.input)
					step.Prepared = prepared
					s.run(step)
					for _, input := range []string{m.invalid,m.low,m.high,"bogus"} {
						if input == "" { continue }
						diagnostic := resourceValidationError(m.name,input)
						for _, rows := range []bool{false,true} { s.run(physicalError("SET "+m.name+"="+physicalLiteral(input),diagnostic,prepared,rows)); s.resourceValue(m.name,m.show,m.raw,"session",prepared) }
						s.run(physicalError("SELECT set_config($1,$2,false)",diagnostic,prepared,true,m.name,input))
						s.resourceValue(m.name,m.show,m.raw,"session",prepared)
					}
					d := resourceDefinition(m.name)
					raw,show,source := s.resourceInitial(d)
					peer.resourceValue(m.name,show,raw,source,prepared)
					s.run(fixture.Exec("RESET "+m.name,0))
					s.resourceValue(m.name,show,raw,source,prepared)
				}
			})
		})
	}
	t.Run("numeric-units-and-hooks",func(t *testing.T) {
		physicalCases(t,nil,func(s *physicalCase) {
			for _, prepared := range []bool{false,true} {
				for _, tc := range []struct{ input,show,raw string }{{"1024kB","1MB","1024"},{"1 MB","1MB","1024"},{"1025kB","1025kB","1025"},{"30.1GB","30822MB","31561728"},{"64.5","64kB","64"},{"65.5","66kB","66"}} {
					step := fixture.Exec("SET work_mem="+physicalLiteral(tc.input),0); step.Prepared=prepared; s.run(step)
					s.resourceValue("work_mem",tc.show,tc.raw,"session",prepared)
				}
				s.run(fixture.Exec("SET work_mem='30.1GB'",0))
				for _, tc := range []struct{input,message,hint string}{{"1kB",`1 kB is outside the valid range for parameter "work_mem" (64 kB .. 2147483647 kB)`,""},{"1 mb",`invalid value for parameter "work_mem": "1 mb"`,`Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`}} {
					d := physicalDiagnostic("22023",tc.message); d.Hint=tc.hint
					s.run(physicalError("SET work_mem="+physicalLiteral(tc.input),d,prepared,false))
					s.run(physicalError("SELECT set_config('work_mem',$1,false)",d,prepared,true,tc.input))
					s.resourceValue("work_mem","30822MB","31561728","session",prepared)
				}
				for _, input := range []string{"127kB","0x7f"} {
					d := resourceValidationError("vacuum_buffer_usage_limit","127")
					s.run(physicalError("SET vacuum_buffer_usage_limit="+physicalLiteral(input),d,prepared,false))
					s.run(physicalError("SELECT set_config('vacuum_buffer_usage_limit',$1,false)",d,prepared,true,input))
					s.resourceValue("vacuum_buffer_usage_limit","2MB","2048","default",prepared)
				}
				for _, tc := range []struct{name,input,show,raw string}{{"hash_mem_multiplier","2.0","2","2"},{"vacuum_buffer_usage_limit","128","128kB","128"},{"temp_file_limit","-1","-1","-1"},{"parallel_leader_participation","yes","on","on"},{"parallel_leader_participation","0","off","off"},{"max_stack_depth","7680","7680kB","7680"}} {
					step:=fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.input),0); step.Prepared=prepared; s.run(step); s.resourceValue(tc.name,tc.show,tc.raw,"session",prepared)
				}
				for _, input := range []string{"7681","2147483647"} {
					d := physicalDiagnostic("22023",fmt.Sprintf(`invalid value for parameter "max_stack_depth": %s`,input))
					d.Detail=`"max_stack_depth" must not exceed 7680kB.`
					d.Hint=`Increase the platform's stack depth limit via "ulimit -s" or local equivalent.`
					s.run(physicalError("SET max_stack_depth="+physicalLiteral(input),d,prepared,false))
					s.run(physicalError("SELECT set_config('max_stack_depth',$1,false)",d,prepared,true,input))
					s.resourceValue("max_stack_depth","7680kB","7680","session",prepared)
				}
				s.run(fixture.Exec("RESET ALL",0))
			}
		})
	})
}
