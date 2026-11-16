// Package perm defines the 16 permissions a user can hold (ROLE-1), the three
// role presets (ROLE-2) and the set operations the rest of the server needs.
// The string IDs are stored in the database and sent to the web app, so they
// never change.
package perm

import "sort"

// Permission is one thing a user may be allowed to do, by its stable ID.
type Permission string

const (
	CatalogBrowse     Permission = "catalog.browse"
	LoansOwnView      Permission = "loans.own.view"
	LoansOwnRenew     Permission = "loans.own.renew"
	ReviewsWrite      Permission = "reviews.write"
	SelfCheckout      Permission = "circ.self_checkout"
	PatronsLookup     Permission = "patrons.lookup"
	CircDesk          Permission = "circ.desk"
	CardsReissue      Permission = "cards.reissue"
	CatalogManage     Permission = "catalog.manage"
	ReviewsRemove     Permission = "reviews.remove"
	ReportsView       Permission = "reports.view"
	ActivityView      Permission = "activity.view"
	SettingsManage    Permission = "settings.manage"
	InvitationsManage Permission = "invitations.manage"
	UsersManage       Permission = "users.manage"
	UsersPermissions  Permission = "users.permissions"
)

// All lists every permission, in the order people see them.
var All = []Permission{
	CatalogBrowse, LoansOwnView, LoansOwnRenew, ReviewsWrite, SelfCheckout,
	PatronsLookup, CircDesk, CardsReissue, CatalogManage, ReviewsRemove,
	ReportsView, ActivityView, SettingsManage, InvitationsManage,
	UsersManage, UsersPermissions,
}

// Valid reports whether p is one of the 16.
func Valid(p Permission) bool {
	for _, q := range All {
		if p == q {
			return true
		}
	}
	return false
}

// Role is a preset: the set of permissions assigned with it.
type Role string

const (
	Patron        Role = "patron"
	Librarian     Role = "librarian"
	Administrator Role = "administrator"
)

// Roles lists the three roles, least to most powerful.
var Roles = []Role{Patron, Librarian, Administrator}

// ValidRole reports whether r is one of the three roles.
func ValidRole(r Role) bool {
	for _, q := range Roles {
		if r == q {
			return true
		}
	}
	return false
}

var patronSet = []Permission{CatalogBrowse, LoansOwnView, LoansOwnRenew, ReviewsWrite, SelfCheckout}

var librarianSet = append(append([]Permission{}, patronSet...),
	PatronsLookup, CircDesk, CardsReissue, CatalogManage, ReviewsRemove)

// Preset returns the permissions a role sets. It is a new set each time. An
// unknown role has none.
func Preset(r Role) Set {
	switch r {
	case Patron:
		return NewSet(patronSet...)
	case Librarian:
		return NewSet(librarianSet...)
	case Administrator:
		return NewSet(All...)
	}
	return Set{}
}

// Set is a set of permissions.
type Set map[Permission]struct{}

// NewSet returns a set of ps.
func NewSet(ps ...Permission) Set {
	s := make(Set, len(ps))
	for _, p := range ps {
		s[p] = struct{}{}
	}
	return s
}

// Has reports whether p is in the set. A nil set is empty.
func (s Set) Has(p Permission) bool {
	_, ok := s[p]
	return ok
}

// Equal reports whether both sets hold exactly the same permissions.
func (s Set) Equal(o Set) bool {
	return len(s) == len(o) && s.SubsetOf(o)
}

// SubsetOf reports whether every permission in s is also in o.
func (s Set) SubsetOf(o Set) bool {
	for p := range s {
		if !o.Has(p) {
			return false
		}
	}
	return true
}

// Minus returns what is in s and not in o.
func (s Set) Minus(o Set) Set {
	d := Set{}
	for p := range s {
		if !o.Has(p) {
			d[p] = struct{}{}
		}
	}
	return d
}

// List returns the members in the order of All.
func (s Set) List() []Permission {
	var out []Permission
	for _, p := range All {
		if s.Has(p) {
			out = append(out, p)
		}
	}
	return out
}

// Diff is how a set differs from a preset (ADMIN-2).
type Diff struct {
	Added   []Permission // held, though the preset does not give them
	Removed []Permission // given by the preset, though not held
}

// Empty reports whether the set is exactly the preset.
func (d Diff) Empty() bool { return len(d.Added) == 0 && len(d.Removed) == 0 }

// DiffFromPreset compares s with role's preset.
func DiffFromPreset(s Set, r Role) Diff {
	p := Preset(r)
	return Diff{Added: s.Minus(p).List(), Removed: p.Minus(s).List()}
}

// Strings returns the IDs of ps, sorted, for storing or logging.
func Strings(ps []Permission) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	sort.Strings(out)
	return out
}
