package circ

import (
	"fmt"
	"strings"
	"testing"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/users"
)

// named adds a user with a username and a display name of their own.
func (r *rig) named(username, display string, role perm.Role) users.User {
	r.t.Helper()
	u, err := r.us.Create(ctx, users.NewUser{Username: username, DisplayName: display, PasswordHash: "x", Role: role})
	if err != nil {
		r.t.Fatal(err)
	}
	return u
}

func names(ms []PatronMatch) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.DisplayName)
	}
	return strings.Join(out, ",")
}

// CIRC-8 "A card selects at once": a card number returns its patron even when a name also contains it.
func TestFindAtDeskCardWinsOverAName(t *testing.T) {
	r := newRig(t)
	pat, card := r.user("pat", perm.Patron)
	r.named("decoy", "Decoy "+card, perm.Patron)
	got, err := r.svc.FindAtDesk(ctx, " "+strings.ToLower(card)+" ")
	if err != nil || got.Patron == nil || got.Patron.ID != pat.ID || got.Patron.Card != card || got.Matches != nil || got.More {
		t.Fatalf("got %+v %v", got, err)
	}
}

// CIRC-8 "Search by part of a name or username": case-insensitive, never by part of a card number.
func TestFindAtDeskByNameOrUsername(t *testing.T) {
	r := newRig(t)
	r.named("ann", "Ann Lee", perm.Patron)
	_, card := r.user("zed", perm.Patron)
	r.named("bob", "Robert Fox", perm.Patron)
	for q, want := range map[string]string{"nn l": "Ann Lee", "ANN": "Ann Lee", "obe": "Robert Fox", "bo": "Robert Fox", "zEd": "Zed"} {
		got, err := r.svc.FindAtDesk(ctx, q)
		if err != nil || got.Patron != nil || names(got.Matches) != want || got.More {
			t.Errorf("%q: %+v %v", q, got, err)
		}
	}
	got, err := r.svc.FindAtDesk(ctx, card[:len(card)-3])
	if err != nil || got.Patron != nil || len(got.Matches) != 0 {
		t.Errorf("part of a card matched: %+v %v", got, err)
	}
}

// CIRC-8: % and _ are taken literally, and a lookup that matches nothing is an empty list.
func TestFindAtDeskWildcardsAndNoMatch(t *testing.T) {
	r := newRig(t)
	r.named("a1", "100% Sure", perm.Patron)
	r.named("a2", "Under_score", perm.Patron)
	r.named("a3", "Other One", perm.Patron)
	for q, want := range map[string]string{"%": "100% Sure", "_": "Under_score", "0% S": "100% Sure", "und_r": ""} {
		got, err := r.svc.FindAtDesk(ctx, q)
		if err != nil || names(got.Matches) != want {
			t.Errorf("%q: %+v %v", q, got, err)
		}
	}
	got, err := r.svc.FindAtDesk(ctx, "zzz")
	if err != nil || got.Patron != nil || got.Matches == nil || len(got.Matches) != 0 || got.More {
		t.Errorf("no match: %+v %v", got, err)
	}
}

// CIRC-8 "Staff and deactivated are listed": everyone is a candidate; Active and Card tell them apart. Same name: both, by id.
func TestFindAtDeskEveryoneIsACandidate(t *testing.T) {
	r := newRig(t)
	a := r.named("sam1", "Sam Same", perm.Patron)
	b := r.named("sam2", "Sam Same", perm.Patron)
	lib := r.named("samlib", "Sam Librarian", perm.Librarian)
	adm := r.named("samadm", "Sam Admin", perm.Administrator)
	off := r.named("samoff", "Sam Off", perm.Patron)
	nocard := r.named("samnone", "Sam Nocard", perm.Patron)
	if _, err := r.st.DB().ExecContext(ctx, `UPDATE users SET active = 0 WHERE id = ?`, off.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.DB().ExecContext(ctx, `DELETE FROM identifiers WHERE user_id = ?`, nocard.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.svc.FindAtDesk(ctx, "sam")
	if err != nil || got.Patron != nil || got.More || len(got.Matches) != 6 {
		t.Fatalf("%+v %v", got, err)
	}
	by := map[int64]PatronMatch{}
	var ids []int64
	for _, m := range got.Matches {
		by[m.ID] = m
		ids = append(ids, m.ID)
	}
	if names(got.Matches) != "Sam Admin,Sam Librarian,Sam Nocard,Sam Off,Sam Same,Sam Same" || ids[4] != a.ID || ids[5] != b.ID {
		t.Errorf("order: %s %v", names(got.Matches), ids)
	}
	if !by[lib.ID].Active || by[lib.ID].Card == "" || !by[adm.ID].Active || by[adm.ID].Card == "" {
		t.Errorf("staff: %+v %+v", by[lib.ID], by[adm.ID])
	}
	if by[off.ID].Active || by[off.ID].Card == "" {
		t.Errorf("deactivated: %+v", by[off.ID])
	}
	if !by[nocard.ID].Active || by[nocard.ID].Card != "" {
		t.Errorf("no card: %+v", by[nocard.ID])
	}
}

// CIRC-8 "At most 50": 50 matches say nothing more; 51 give 50 and more.
func TestFindAtDeskLimit(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 50; i++ {
		r.named(fmt.Sprintf("lim%02d", i), fmt.Sprintf("Limit %02d", i), perm.Patron)
	}
	got, err := r.svc.FindAtDesk(ctx, "limit")
	if err != nil || len(got.Matches) != 50 || got.More {
		t.Fatalf("50: %d more=%v %v", len(got.Matches), got.More, err)
	}
	r.named("lim50", "Limit 50", perm.Patron)
	got, err = r.svc.FindAtDesk(ctx, "limit")
	if err != nil || len(got.Matches) != 50 || !got.More || got.Matches[49].DisplayName != "Limit 49" {
		t.Fatalf("51: %d more=%v %v", len(got.Matches), got.More, err)
	}
}

func TestFindAtDeskRefusesBadQueries(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"", "   ", "a\tb", "a\x00", strings.Repeat("x", 201)} {
		_, err := r.svc.FindAtDesk(ctx, q)
		e, ok := apperr.As(err)
		if !ok || e.Code != apperr.Invalid || e.Field != "q" {
			t.Errorf("%q: %v", q, err)
		}
	}
}

// A card number that nobody holds falls back to names (a name may look like a code).
func TestFindAtDeskUnknownCodeSearchesNames(t *testing.T) {
	r := newRig(t)
	r.named("x1", "Ann", perm.Patron)
	got, err := r.svc.FindAtDesk(ctx, "Ann")
	if err != nil || got.Patron != nil || len(got.Matches) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

// A card matches only whole: a prefix or an interior part of its number finds no one, and the whole number still selects.
func TestFindAtDeskCardOnlyMatchesWhole(t *testing.T) {
	r := newRig(t)
	pat, card := r.user("pat", perm.Patron)
	for _, q := range []string{card[:len(card)-1], card[:6], card[3:9], card[1 : len(card)-1], card + "0"} {
		got, err := r.svc.FindAtDesk(ctx, q)
		if err != nil || got.Patron != nil || len(got.Matches) != 0 {
			t.Errorf("%q: %+v %v", q, got, err)
		}
	}
	if got, err := r.svc.FindAtDesk(ctx, card); err != nil || got.Patron == nil || got.Patron.ID != pat.ID {
		t.Errorf("whole card: %+v %v", got, err)
	}
}

// Hostile input: a backslash is literal; 200 code points are accepted (multi-byte ones count once) and 201 refused;
// an emoji above U+FFFF and RTL marks work in the query and in a stored name.
func TestFindAtDeskHostileInput(t *testing.T) {
	r := newRig(t)
	r.named("bs", `Back\slash`, perm.Patron)
	r.named("plain", "Backslash", perm.Patron)
	r.named("emo", "Pat 😀 Smith", perm.Patron)
	rtl := r.named("rtl", "Dana Lee", perm.Patron)
	// creation refuses such a name, but one may be stored by older data or another path
	if _, err := r.st.DB().ExecContext(ctx, `UPDATE users SET display_name = ? WHERE id = ?`, "Dana ‏‮Lee", rtl.ID); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]string{`\`: `Back\slash`, `k\s`: `Back\slash`, "😀": "Pat 😀 Smith", "t 😀 S": "Pat 😀 Smith", "‏": "Dana ‏‮Lee", "‮Lee": "Dana ‏‮Lee"} {
		got, err := r.svc.FindAtDesk(ctx, q)
		if err != nil || names(got.Matches) != want {
			t.Errorf("%q: %+v %v", q, got, err)
		}
	}
	for _, unit := range []string{"x", "é", "😀"} {
		if got, err := r.svc.FindAtDesk(ctx, strings.Repeat(unit, 200)); err != nil || len(got.Matches) != 0 {
			t.Errorf("200 × %q: %+v %v", unit, got, err)
		}
		_, err := r.svc.FindAtDesk(ctx, strings.Repeat(unit, 201))
		if e, ok := apperr.As(err); !ok || e.Code != apperr.Invalid || e.Field != "q" {
			t.Errorf("201 × %q: %v", unit, err)
		}
	}
	r.named("long", strings.Repeat("😀", 30), perm.Patron)
	if got, err := r.svc.FindAtDesk(ctx, strings.Repeat("😀", 30)); err != nil || len(got.Matches) != 1 {
		t.Errorf("a long emoji name: %+v %v", got, err)
	}
}

// 60 matches give the first 50 and more.
func TestFindAtDeskSixtyMatches(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 60; i++ {
		r.named(fmt.Sprintf("six%02d", i), fmt.Sprintf("Sixty %02d", i), perm.Patron)
	}
	got, err := r.svc.FindAtDesk(ctx, "sixty")
	if err != nil || len(got.Matches) != 50 || !got.More || got.Matches[0].DisplayName != "Sixty 00" || got.Matches[49].DisplayName != "Sixty 49" {
		t.Fatalf("%d more=%v %v", len(got.Matches), got.More, err)
	}
}
