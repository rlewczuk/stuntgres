package publication

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func failure(code, message string) *sqlstate.Error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func applyOptions(d *catalog.Publication, options []parser.DictionaryOption) error {
	seen := make(map[string]bool, len(options))
	for _, o := range options {
		name := o.Name.Text
		switch name {
		case "publish", "publish_via_partition_root", "publish_generated_columns":
		default:
			return failure("42601", fmt.Sprintf(`unrecognized publication parameter: "%s"`, name))
		}
		if seen[name] {
			err := failure("42601", "conflicting or redundant options")
			err.Position = fmt.Sprint(o.Name.Pos.Character)
			return err
		}
		seen[name] = true
		switch name {
		case "publish":
			if !o.Supplied {
				return failure("42601", "publish requires a parameter")
			}
			actions, err := settings.ParseIdentifierList("publish", o.Value)
			if err != nil {
				return failure("42601", `invalid list syntax in parameter "publish"`)
			}
			d.Insert, d.Update, d.Delete, d.Truncate = false, false, false, false
			for _, action := range actions {
				switch action {
				case "insert":
					d.Insert = true
				case "update":
					d.Update = true
				case "delete":
					d.Delete = true
				case "truncate":
					d.Truncate = true
				default:
					return failure("42601", fmt.Sprintf(`unrecognized value for publication option "publish": "%s"`, action))
				}
			}
		case "publish_via_partition_root":
			value := strings.ToLower(o.Value)
			if o.Numeric {
				text := strings.ReplaceAll(value, "_", "")
				unsigned := strings.TrimPrefix(strings.TrimPrefix(text, "+"), "-")
				base := 10
				if len(unsigned) > 2 && unsigned[0] == '0' && strings.ContainsRune("bBoOxX", rune(unsigned[1])) {
					base = 0
				}
				if n, err := strconv.ParseInt(text, base, 32); err == nil && (n == 0 || n == 1) {
					value = strconv.FormatInt(n, 10)
				}
			}
			if !o.Supplied {
				d.ViaRoot = true
			} else if value == "true" || value == "on" || o.Numeric && value == "1" {
				d.ViaRoot = true
			} else if value == "false" || value == "off" || o.Numeric && value == "0" {
				d.ViaRoot = false
			} else {
				return failure("42601", "publish_via_partition_root requires a Boolean value")
			}
		case "publish_generated_columns":
			if !o.Supplied {
				return failure("42601", "publish_generated_columns requires a parameter")
			}
			switch strings.ToLower(o.Value) {
			case "none":
				d.GeneratedColumns = 'n'
			case "stored":
				d.GeneratedColumns = 's'
			default:
				err := failure("42601", fmt.Sprintf(`invalid value for publication parameter "publish_generated_columns": "%s"`, o.Value))
				err.Detail = `Valid values are "none" and "stored".`
				return err
			}
		}
	}
	return nil
}
