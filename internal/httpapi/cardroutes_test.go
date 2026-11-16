package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/barcode/barcodetest"
	"jamieslibrary/internal/perm"
)

func (s *site) cardOf(cookie string) string {
	s.t.Helper()
	rec := s.req("GET", "/api/me/card", cookie, "")
	var c cardBody
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != 200 {
		s.t.Fatalf("card: %d %s", rec.Code, rec.Body)
	}
	return c.Number
}

func (s *site) logTotal() int {
	s.t.Helper()
	page, err := activity.New(s.st).List(context.Background(), 1, 10)
	if err != nil {
		s.t.Fatal(err)
	}
	return page.Total
}

// CARD-1 "View card": a new patron opens their card and sees their name, a barcode and a QR code unique to them.
func TestViewCard(t *testing.T) {
	s := newSite(t)
	s.addUser("pat", goodPassword, perm.Patron)
	s.addUser("quinn", goodPassword, perm.Patron)
	pat, quinn := s.signIn("pat", goodPassword), s.signIn("quinn", goodPassword)

	rec := s.req("GET", "/api/me/card", pat, "")
	var c cardBody
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != 200 || c.Name != "pat" || len(c.Number) != 12 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, kind := range []string{"barcode", "qr"} {
		img := s.req("GET", "/api/me/card/"+kind+".svg", pat, "")
		if img.Code != 200 || img.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(img.Body.String(), "<svg") {
			t.Fatalf("%s: %d %s %.60s", kind, img.Code, img.Header().Get("Content-Type"), img.Body)
		}
		other := s.req("GET", "/api/me/card/"+kind+".svg", quinn, "")
		if other.Body.String() == img.Body.String() {
			t.Errorf("two patrons got the same %s", kind)
		}
	}
	if s.cardOf(quinn) == c.Number {
		t.Fatal("two patrons share a card")
	}
}

// CARD-1: the images a patron is shown say their card number, and only that. A scanner reading the
// barcode or the QR code at the desk must get what /api/me/card names.
func TestServedCardImagesDecodeToTheCardNumber(t *testing.T) {
	s := newSite(t)
	s.addUser("pat", goodPassword, perm.Patron)
	s.addUser("quinn", goodPassword, perm.Patron)
	for _, name := range []string{"pat", "quinn"} {
		cookie := s.signIn(name, goodPassword)
		want := s.cardOf(cookie)
		for kind, symbology := range map[string]string{"barcode": "code128", "qr": "qr"} {
			img := s.req("GET", "/api/me/card/"+kind+".svg", cookie, "")
			if img.Code != 200 {
				t.Fatalf("%s %s: %d", name, kind, img.Code)
			}
			got, err := barcodetest.DecodeSVG(symbology, img.Body.String())
			if err != nil || got != want {
				t.Errorf("%s's %s decodes to %q (%v), want their card number %q", name, kind, got, err, want)
			}
		}
	}
}

// A patron can't read another's card by any id: the route takes no id, and one in the query or path is ignored or unknown.
func TestACardIsReadFromTheSessionOnly(t *testing.T) {
	s := newSite(t)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	other := s.addUser("other", goodPassword, perm.Patron)
	cookie := s.signIn("pat", goodPassword)
	mine := s.cardOf(cookie)
	otherCard, _ := s.users.Card(context.Background(), other.ID)
	for _, path := range []string{fmt.Sprintf("/api/me/card?id=%d", other.ID), fmt.Sprintf("/api/me/card?user_id=%d&user=other", other.ID)} {
		if rec := s.req("GET", path, cookie, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), mine) {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if mine == otherCard {
		t.Fatal("two patrons share a card")
	}
	for _, path := range []string{fmt.Sprintf("/api/users/%d/card", other.ID), fmt.Sprintf("/api/me/%d/card", other.ID), fmt.Sprintf("/api/patrons/%d/card", pat.ID)} {
		if rec := s.req("GET", path, cookie, ""); rec.Code != 404 || strings.Contains(rec.Body.String(), otherCard) {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

// CARD-1 "Re-issue": a librarian re-issues a lost card; the new one identifies the patron and the old one says no such patron.
func TestReissue(t *testing.T) {
	s := newSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	lib, patCookie := s.signIn("lib", goodPassword), s.signIn("pat", goodPassword)
	old := s.cardOf(patCookie)

	if rec := s.req("GET", "/api/patrons/by-card/"+old, lib, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"display_name":"pat"`) {
		t.Fatalf("before: %d %s", rec.Code, rec.Body)
	}
	rec := s.req("POST", fmt.Sprintf("/api/users/%d/card/reissue", pat.ID), lib, "{}")
	var res map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != 200 || len(res["number"]) != 12 || res["number"] == old {
		t.Fatalf("reissue: %d %s", rec.Code, rec.Body)
	}
	if got := s.cardOf(patCookie); got != res["number"] {
		t.Fatalf("patron sees %s, was issued %s", got, res["number"])
	}
	if rec := s.req("GET", "/api/patrons/by-card/"+res["number"], lib, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"id":%d`, pat.ID)) {
		t.Fatalf("new card: %d %s", rec.Code, rec.Body)
	}
	rec = s.req("GET", "/api/patrons/by-card/"+old, lib, "")
	if rec.Code != 404 || errMessage(t, rec) != "No such patron." {
		t.Fatalf("old card: %d %s", rec.Code, rec.Body)
	}

	// the log names who and whom, never a card number
	page, err := activity.New(s.st).List(context.Background(), 1, 10)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Action != activity.CardReissued ||
		page.Entries[0].Target != fmt.Sprintf("user:%d", pat.ID) || string(page.Entries[0].Details) != "{}" {
		t.Fatalf("log %+v %v", page, err)
	}
}

func TestLookupByCardNormalisesAndHidesUnknownCodes(t *testing.T) {
	s := newSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	lib := s.signIn("lib", goodPassword)
	card, _ := s.users.Card(context.Background(), pat.ID)
	if rec := s.req("GET", "/api/patrons/by-card/%20"+card+"%20", lib, ""); rec.Code != 200 {
		t.Fatalf("padded: %d %s", rec.Code, rec.Body)
	}
	for _, code := range []string{"000000000000", "x", "%00", strings.Repeat("9", 300), "C0000001"} {
		if rec := s.req("GET", "/api/patrons/by-card/"+code, lib, ""); rec.Code != 404 || errMessage(t, rec) != "No such patron." {
			t.Errorf("%.20s: %d %s", code, rec.Code, rec.Body)
		}
	}
}

// ROLE-3: each card route refuses a caller without its permission and changes nothing.
func TestCardRoutesRefuseCallersWithoutThePermission(t *testing.T) {
	s := newSite(t)
	s.addUser("pat", goodPassword, perm.Patron)
	target := s.addUser("target", goodPassword, perm.Patron)
	pat := s.signIn("pat", goodPassword)
	before, _ := s.users.Card(context.Background(), target.ID)

	if rec := s.req("POST", fmt.Sprintf("/api/users/%d/card/reissue", target.ID), pat, "{}"); rec.Code != 403 {
		t.Errorf("reissue: %d %s", rec.Code, rec.Body)
	}
	if rec := s.req("GET", "/api/patrons/by-card/"+before, pat, ""); rec.Code != 403 || strings.Contains(rec.Body.String(), "target") {
		t.Errorf("lookup: %d %s", rec.Code, rec.Body)
	}
	if after, _ := s.users.Card(context.Background(), target.ID); after != before {
		t.Error("the card changed")
	}
	if s.logTotal() != 0 {
		t.Error("a refused change was logged")
	}
}

// Security design §4(b): a librarian can't re-issue the card of someone with more permissions (the administrator).
func TestReissueFollowsTheSubsetRule(t *testing.T) {
	s := newSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	admin := s.addUser("ada", goodPassword, perm.Administrator)
	lib := s.signIn("lib", goodPassword)
	before, _ := s.users.Card(context.Background(), admin.ID)
	if rec := s.req("POST", fmt.Sprintf("/api/users/%d/card/reissue", admin.ID), lib, "{}"); rec.Code != 403 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if after, _ := s.users.Card(context.Background(), admin.ID); after != before {
		t.Fatal("the card changed")
	}
	if s.logTotal() != 0 {
		t.Error("a refused change was logged")
	}
	for _, id := range []string{"999", "0", "-1", "abc"} {
		if rec := s.req("POST", "/api/users/"+id+"/card/reissue", lib, "{}"); rec.Code != 404 {
			t.Errorf("%s: %d %s", id, rec.Code, rec.Body)
		}
	}
}
