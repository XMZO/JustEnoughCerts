package files

import (
	"os/user"
	"regexp"
	"strconv"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// owner is who the files of a directory belong to, as chown takes it: a
// user, a group, or both, each by name or by number.
type owner struct {
	name     string // as set, for messages
	uid, gid int    // -1 leaves it as it is
}

var ownerPattern = regexp.MustCompile(`^([A-Za-z0-9._-]*)(?::([A-Za-z0-9._-]+))?$`)

// parseOwner reads an owner as user, user:group or :group; "" is none.
// Names are looked up on this machine, which a container has none of: it
// takes numbers.
func parseOwner(v string) (*owner, error) {
	if v == "" {
		return nil, nil //nolint:nilnil // no owner to set
	}
	m := ownerPattern.FindStringSubmatch(v)
	if m == nil || m[1] == "" && m[2] == "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorOwner, Params: map[string]any{"owner": v}})
	}
	o := &owner{name: v, uid: -1, gid: -1}
	var err error
	if m[1] != "" {
		if o.uid, err = lookup(m[1], func(n string) (string, error) {
			u, err := user.Lookup(n)
			if err != nil {
				return "", err
			}
			return u.Uid, nil
		}); err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorOwnerUnknown, Params: map[string]any{"name": m[1]}, Err: err})
		}
	}
	if m[2] != "" {
		if o.gid, err = lookup(m[2], func(n string) (string, error) {
			g, err := user.LookupGroup(n)
			if err != nil {
				return "", err
			}
			return g.Gid, nil
		}); err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorOwnerUnknown, Params: map[string]any{"name": m[2]}, Err: err})
		}
	}
	return o, nil
}

// lookup returns the number of a user or a group: name itself when it is
// one, or what find has for it.
func lookup(name string, find func(string) (string, error)) (int, error) {
	if n, err := strconv.Atoi(name); err == nil && n >= 0 {
		return n, nil
	}
	id, err := find(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(id)
}
