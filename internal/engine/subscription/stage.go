package subscription

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type Host interface {
	ddl.Host
	AllocateSubscriptionOID() (ids.OID, error)
	SubscriptionInBlock() bool
	ValidateSubscriptionSync(string) error
	ParseSubscriptionLSN(string) (uint64, error)
}

func transaction(h Host, label string) error {
	if h.SubscriptionInBlock() {
		return failure("25001", label+" cannot run inside a transaction block")
	}
	return nil
}
func passwordPrivilege() error {
	e := failure("42501", "password_required=false is superuser-only")
	e.Hint = "Subscriptions with the password_required option set to false may only be created or modified by the superuser."
	return e
}
func validateConnection(text string, passwordRequired, superuser bool) error {
	password, err := parseConnection(text)
	if err != nil {
		return err
	}
	if !superuser && passwordRequired && !password {
		e := failure("2F003", "password is required")
		e.Detail = "Non-superusers must provide a password in the connection string."
		return e
	}
	return nil
}
func publications(s *parser.Subscription, old []string) ([]string, error) {
	requested := make([]string, len(s.Publications))
	for i, name := range s.Publications {
		if slices.Contains(requested[:i], name.Text) {
			return nil, failure("42710", fmt.Sprintf(`publication name "%s" used more than once`, name.Text))
		}
		requested[i] = name.Text
	}
	if s.Action == "create" || s.Action == "set" {
		return requested, nil
	}
	next := slices.Clone(old)
	for _, name := range requested {
		index := slices.Index(next, name)
		if s.Action == "add" {
			if index >= 0 {
				return nil, failure("42710", fmt.Sprintf(`publication "%s" is already in subscription "%s"`, name, s.Name.Text))
			}
			next = append(next, name)
		} else {
			if index < 0 {
				return nil, failure("42P17", fmt.Sprintf(`publication "%s" is not in subscription "%s"`, name, s.Name.Text))
			}
			next = slices.Delete(next, index, index+1)
		}
	}
	if len(next) == 0 {
		return nil, failure("42P17", "cannot drop all the publications from a subscription")
	}
	return next, nil
}
func Stage(h Host, auth ddl.Authority, s *parser.Subscription) error {
	v := h.AccessView(auth)
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return err
	}
	key := catalog.SubscriptionNameKey(v.Owner.DatabaseAddress(), s.Name.Text)
	if s.Action == "create" {
		o, err := parseOptions(s, h)
		if err != nil {
			return err
		}
		if o.createSlot {
			if err = transaction(h, "CREATE SUBSCRIPTION ... WITH (create_slot = true)"); err != nil {
				return err
			}
		}
		capability, ok := v.Roles.Lookup("pg_create_subscription")
		permitted := actor.Superuser
		if ok && !permitted {
			permitted, err = v.Roles.InheritsRole(v.State.CurrentUser, capability.Ref)
			if err != nil {
				return err
			}
		}
		if !permitted {
			e := failure("42501", "permission denied to create subscription")
			e.Detail = `Only roles with privileges of the "pg_create_subscription" role may create subscriptions.`
			return e
		}
		if err = access.OwnershipDatabaseCreate(v, auth); err != nil {
			return err
		}
		if o.supplied["password_required"] && o.values["password_required"] == "false" && !actor.Superuser {
			return passwordPrivilege()
		}
		if h.StageRead().NameAddress(key) != (ids.ObjectAddress{}) {
			return failure("42710", fmt.Sprintf(`subscription "%s" already exists`, s.Name.Text))
		}
		d := &catalog.Subscription{ObjectName: s.Name.Text, Database: v.Owner.DatabaseAddress(), Owner: v.State.CurrentUser, Connection: s.Connection, SlotName: s.Name.Text, HasSlot: true, Enabled: o.enabled, Streaming: 'p', TwoPhase: 'd', PasswordRequired: true, SynchronousCommit: "off", Origin: "any"}
		o.apply(d)
		if err = validateConnection(s.Connection, d.PasswordRequired, actor.Superuser); err != nil {
			return err
		}
		pubs, err := publications(s, nil)
		if err != nil {
			return err
		}
		d.SetPublications(pubs)
		if o.connect {
			return failure("0A000", "subscription publisher connections are not available")
		}
		oid, err := h.AllocateSubscriptionOID()
		if err != nil {
			return err
		}
		d.ObjectAddress = ids.ObjectAddress{ClassID: ids.SubscriptionClassID, ObjectID: oid}
		d.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		if err = h.Stage().Create(d); err != nil {
			return err
		}
		h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "subscription was created, but is not connected", Hint: "To initiate replication, you must manually create the replication slot, enable the subscription, and refresh the subscription."})
		return nil
	}
	d, _ := h.StageRead().Get(h.StageRead().NameAddress(key)).(*catalog.Subscription)
	if d == nil {
		if s.Action == "drop" && s.IfExists {
			h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`subscription "%s" does not exist, skipping`, s.Name.Text)})
			return nil
		}
		return failure("42704", fmt.Sprintf(`subscription "%s" does not exist`, s.Name.Text))
	}
	if s.Action == "owner" {
		target, err := access.SchemaOwner(v, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if target == d.Owner {
			return nil
		}
	}
	if err = v.Check(auth, access.Requirement{Object: v.Ref(d.Address()), OwnerOnly: true, OwnerNoun: "subscription"}); err != nil {
		return err
	}
	if s.Action != "drop" && !d.PasswordRequired && !actor.Superuser {
		return passwordPrivilege()
	}
	next := *d
	if s.Action == "refresh" && !d.Enabled {
		return failure("55000", "ALTER SUBSCRIPTION ... REFRESH is not allowed for disabled subscriptions")
	}
	o, err := parseOptions(s, h)
	if err != nil {
		return err
	}
	switch s.Action {
	case "drop":
		if d.HasSlot {
			if err = transaction(h, "DROP SUBSCRIPTION"); err != nil {
				return err
			}
			return failure("0A000", "subscription replication slot removal is not available")
		}
		h.DropTarget(d.Address())
		return h.RecordDependencyPath(auth)
	case "connection":
		owner, err := v.Roles.Descriptor(d.Owner)
		if err != nil {
			return err
		}
		if err = validateConnection(s.Connection, d.PasswordRequired, owner.Superuser); err != nil {
			return err
		}
		next.Connection = s.Connection
	case "enable":
		if !d.HasSlot {
			return failure("55000", "cannot enable subscription that does not have a slot name")
		}
		next.Enabled = true
	case "disable":
		next.Enabled = false
	case "options":
		if o.supplied["slot_name"] && o.values["slot_name"] == "none" && d.Enabled {
			return failure("55000", "cannot set slot_name = NONE for enabled subscription")
		}
		if o.supplied["password_required"] && o.values["password_required"] == "false" && !actor.Superuser {
			return passwordPrivilege()
		}
		for _, name := range []string{"two_phase", "failover"} {
			if !o.supplied[name] {
				continue
			}
			if d.Enabled {
				return failure("55000", fmt.Sprintf(`cannot set option "%s" for enabled subscription`, name))
			}
			remote := name == "failover" || o.values[name] == "false"
			if remote {
				if !d.HasSlot {
					return failure("55000", fmt.Sprintf(`cannot set option "%s" for a subscription that does not have a slot name`, name))
				}
				if err = transaction(h, `ALTER SUBSCRIPTION ... SET (`+name+`)`); err != nil {
					return err
				}
				if name == "two_phase" && o.supplied["slot_name"] {
					return failure("42601", `"slot_name" and "two_phase" cannot be altered at the same time`)
				}
			}
		}
		if o.supplied["failover"] || o.supplied["two_phase"] && o.values["two_phase"] == "false" {
			return failure("0A000", "subscription replication slot alteration is not available")
		}
		o.apply(&next)
	case "set", "add", "remove", "refresh":
		if s.Action != "refresh" {
			pubs, err := publications(s, d.Publications())
			if err != nil {
				return err
			}
			next.SetPublications(pubs)
		}
		if o.refresh {
			if !d.Enabled {
				e := failure("55000", "ALTER SUBSCRIPTION with refresh is not allowed for disabled subscriptions")
				action := strings.ToUpper(s.Action)
				if action == "REMOVE" {
					action = "DROP"
				}
				e.Hint = "Use ALTER SUBSCRIPTION ... " + action + " PUBLICATION ... WITH (refresh = false)."
				if s.Action != "set" {
					e.Hint = "Use ALTER SUBSCRIPTION ... " + action + " PUBLICATION ... WITH (refresh = false) instead."
				}
				return e
			}
			label := "ALTER SUBSCRIPTION with refresh"
			if s.Action == "refresh" {
				label = "ALTER SUBSCRIPTION ... REFRESH"
			}
			if err = transaction(h, label); err != nil {
				return err
			}
			return failure("0A000", "subscription publisher connections are not available")
		}
	case "skip":
		if !o.supplied["lsn"] {
			return failure("42601", "must specify lsn")
		}
		next.SkipLSN = o.lsn
	case "rename":
		if err = access.OwnershipDatabaseCreate(v, auth); err != nil {
			return err
		}
		if h.StageRead().NameAddress(catalog.SubscriptionNameKey(d.Database, s.Target.Text)) != (ids.ObjectAddress{}) {
			return failure("42710", fmt.Sprintf(`subscription "%s" already exists`, s.Target.Text))
		}
		next.ObjectName = s.Target.Text
	case "owner":
		target, err := access.SchemaOwner(v, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if target == d.Owner {
			return nil
		}
		if err = access.OwnershipSuccessor(v, target); err != nil {
			return err
		}
		if err = access.OwnershipDatabaseCreate(v, auth); err != nil {
			return err
		}
		next.Owner = target
	default:
		return fmt.Errorf("stuntgres: invalid subscription action %q", s.Action)
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Replace(&next)
}
