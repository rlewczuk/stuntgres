package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func IdentityValue(h SessionInquiryHost, c *exec.Context, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	identity, err := h.ExpressionIdentity(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if kind != functions.IdentityCurrentSchema {
		return identity.Value(kind)
	}
	cache := h.CurrentSchemaResult()
	if h.InquiryCacheable() && cache.Type() != nil {
		return *cache, nil
	}
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	read, path, err := h.CurrentSchemaPathLocked()
	if err != nil {
		return sqltype.Value{}, err
	}
	resultValue := sqltype.Null(result)
	if len(path) > 0 {
		resultValue = sqltype.String(result, read.Get(path[0]).Name())
	}
	if h.InquiryCacheable() {
		*cache = resultValue
	}
	return resultValue, nil
}

func CurrentSchemas(h SessionInquiryHost, implicit bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	cache := h.SearchPathResults()
	results := *cache
	if results[0].Type() == nil || !h.InquiryCacheable() {
		h.ReacquireEngine()
		defer h.ReleaseEngineLocked()
		read, paths, err := h.SearchPathsLocked()
		if err != nil {
			return sqltype.Value{}, err
		}
		names := make([]string, len(paths[1]))
		for i, path := range paths {
			for j, a := range path {
				names[j] = read.Get(a).Name()
			}
			results[i], err = sqltype.Names(result, names[:len(path)])
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if h.InquiryCacheable() {
			*cache = results
		}
	}
	if implicit {
		return results[1], nil
	}
	return results[0], nil
}

func MyTempNamespace(h SessionInquiryHost) (uint32, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	namespace, err := h.TemporaryNamespaceLocked()
	if err != nil {
		return 0, err
	}
	return uint32(namespace.ObjectID), nil
}

func OtherTempNamespace(h SessionInquiryHost, oid uint32) (bool, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	read, owner, err := h.NamespaceSessionLocked()
	if err != nil {
		return false, err
	}
	ns, _ := read.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(oid)})
	return ns != nil && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != owner.SessionToken, nil
}

func ListeningChannels(h SessionInquiryHost, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	h.ReacquireEngine()
	channels, err := h.ListeningChannelsLocked()
	h.ReleaseEngineLocked()
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, nil
	}
	values := make([]sqltype.Value, len(channels))
	i := 0
	for channel := range channels {
		values[i] = sqltype.String(result, channel)
		i++
	}
	return values, nil
}

func NotificationQueueUsage(h SessionInquiryHost) (float64, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	return h.NotificationQueueUsageLocked()
}
