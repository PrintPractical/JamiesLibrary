package httpapi

import (
	"fmt"
	"strings"
	"testing"

	"jamieslibrary/internal/perm"
)

func (c *catSite) review(cookie string, bookID int64, method, payload string) int {
	c.t.Helper()
	return c.req(method, fmt.Sprintf("/api/books/%d/review", bookID), cookie, payload).Code
}

func (c *catSite) page(cookie string, bookID int64) bookDetail {
	c.t.Helper()
	return decodeAs[bookDetail](c.t, c.req("GET", fmt.Sprintf("/api/books/%d", bookID), cookie, ""), 200)
}

// CAT-6 "Rate": a patron rates a book 4 stars with a review, and signed-in users see it with the patron's display name and the average.
func TestCAT6_Rate(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.addUser("sam", goodPassword, perm.Patron)
	sam := c.signIn("sam", goodPassword)

	if code := c.review(c.pat, b.ID, "PUT", `{"rating":4,"text":"A fine read."}`); code != 200 {
		t.Fatalf("rate: %d", code)
	}
	if code := c.review(sam, b.ID, "PUT", `{"rating":5}`); code != 200 {
		t.Fatalf("rate: %d", code)
	}
	for _, who := range []string{c.pat, sam, c.lib} {
		d := c.page(who, b.ID)
		if d.RatingCount != 2 || d.RatingAverage != 4.5 {
			t.Errorf("average %v of %d", d.RatingAverage, d.RatingCount)
		}
		if len(d.Reviews) != 1 || d.Reviews[0].DisplayName != "pat" || d.Reviews[0].Rating != 4 || d.Reviews[0].Text != "A fine read." || d.ReviewsTotal != 1 {
			t.Errorf("reviews %+v", d.Reviews)
		}
	}
	if d := c.page(c.pat, b.ID); d.MyReview == nil || d.MyReview.Rating != 4 {
		t.Errorf("my review %+v", d.MyReview)
	}
	if d := c.page(c.lib, b.ID); d.MyReview != nil {
		t.Errorf("a librarian who wrote none has %+v", d.MyReview)
	}
	rec := c.req("GET", fmt.Sprintf("/api/books/%d/reviews?per_page=1", b.ID), c.pat, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
}

// Editing keeps one review per user per book; deleting removes it and its rating.
func TestReviewEditKeepsOneAndDeleteRemoves(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.review(c.pat, b.ID, "PUT", `{"rating":2,"text":"Meh."}`)
	c.review(c.pat, b.ID, "PUT", `{"rating":5,"text":"Better on reread."}`)
	if n := c.count("reviews"); n != 1 {
		t.Fatalf("%d rows after an edit", n)
	}
	d := c.page(c.pat, b.ID)
	if d.RatingCount != 1 || d.RatingAverage != 5 || d.Reviews[0].Text != "Better on reread." {
		t.Fatalf("after edit %+v", d)
	}
	// A rating alone replaces the text: it is the user's whole submission.
	c.review(c.pat, b.ID, "PUT", `{"rating":3}`)
	if d := c.page(c.pat, b.ID); len(d.Reviews) != 0 || d.RatingAverage != 3 || d.MyReview == nil || d.MyReview.Text != "" {
		t.Fatalf("rating only %+v", d)
	}
	if code := c.review(c.pat, b.ID, "DELETE", ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if d := c.page(c.pat, b.ID); d.RatingCount != 0 || d.MyReview != nil || c.count("reviews") != 0 {
		t.Fatalf("after delete %+v", d)
	}
	if code := c.review(c.pat, b.ID, "DELETE", ""); code != 404 {
		t.Errorf("deleting twice: %d", code)
	}
}

// CAT-6 "Too long": over 2000 characters is refused, says the limit, and nothing is stored.
func TestCAT6_TooLong(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	long := strings.Repeat("é", 2001) // code points, not bytes
	rec := c.req("PUT", fmt.Sprintf("/api/books/%d/review", b.ID), c.pat, body(map[string]any{"rating": 4, "text": long}))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "2000") || !strings.Contains(rec.Body.String(), `"field":"text"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if c.count("reviews") != 0 {
		t.Error("a refused review was stored")
	}
	exact := strings.Repeat("é", 2000)
	if rec := c.req("PUT", fmt.Sprintf("/api/books/%d/review", b.ID), c.pat, body(map[string]any{"text": exact})); rec.Code != 200 {
		t.Errorf("2000 characters: %d %s", rec.Code, rec.Body)
	}
}

func TestReviewRefusesBadInput(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	for name, payload := range map[string]string{
		"zero stars": `{"rating":0}`, "six stars": `{"rating":6}`, "negative": `{"rating":-1}`, "empty": `{}`,
		"empty text": `{"text":""}`, "null": `{"rating":null,"text":null}`, "a user": `{"rating":3,"user_id":1}`,
		"string rating": `{"rating":"4"}`, "not json": `nope`,
	} {
		if code := c.review(c.pat, b.ID, "PUT", payload); code < 400 || code > 422 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := c.review(c.pat, 9999, "PUT", `{"rating":3}`); code != 404 {
		t.Errorf("unknown book: %d", code)
	}
	if code := c.review(c.pat, 0, "PUT", `{"rating":3}`); code != 404 {
		t.Errorf("bad book: %d", code)
	}
	if c.count("reviews") != 0 {
		t.Error("bad input was stored")
	}
}

// CAT-6 "Visitor": signed out under the public catalog, the average and count show, with no review text or names.
func TestCAT6_Visitor(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.review(c.pat, b.ID, "PUT", `{"rating":4,"text":"SECRETREVIEWTEXT"}`)
	c.publicOn(true)
	for _, p := range []string{fmt.Sprintf("/api/public/books/%d", b.ID), "/api/public/books?q=hobbit"} {
		rec := c.req("GET", p, "", "")
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "SECRETREVIEWTEXT") || strings.Contains(rec.Body.String(), "pat") && strings.Contains(rec.Body.String(), "display_name") {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	d := decodeAs[publicBook](t, c.req("GET", fmt.Sprintf("/api/public/books/%d", b.ID), "", ""), 200)
	if d.RatingAverage != 4 || d.RatingCount != 1 {
		t.Errorf("public rating %v of %d", d.RatingAverage, d.RatingCount)
	}
	// The review routes are not public.
	for _, r := range [][2]string{{"GET", fmt.Sprintf("/api/books/%d/reviews", b.ID)}, {"PUT", fmt.Sprintf("/api/books/%d/review", b.ID)}, {"DELETE", "/api/reviews/1"}} {
		if rec := c.req(r[0], r[1], "", `{"rating":3}`); rec.Code != 401 {
			t.Errorf("%v signed out: %d", r, rec.Code)
		}
	}
}

// CAT-6 "Moderate": a librarian removes a review; it no longer shows or counts, and the removal is logged.
func TestCAT6_Moderate(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.review(c.pat, b.ID, "PUT", `{"rating":1,"text":"rude words"}`)
	id := c.page(c.lib, b.ID).Reviews[0].ID

	if rec := c.req("DELETE", fmt.Sprintf("/api/reviews/%d", id), c.lib, ""); rec.Code != 204 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	d := c.page(c.pat, b.ID)
	if len(d.Reviews) != 0 || d.RatingCount != 0 || d.MyReview != nil {
		t.Errorf("still shows: %+v", d)
	}
	if n := c.count("activity"); n != 1 {
		t.Fatalf("%d activity records", n)
	}
	var action, target, details string
	var actor int64
	if err := c.st.DB().QueryRow(`SELECT action, target, details, actor_id FROM activity`).Scan(&action, &target, &details, &actor); err != nil {
		t.Fatal(err)
	}
	if action != "review.removed" || target != fmt.Sprintf("review:%d", id) || actor != c.libUser.ID || strings.Contains(details, "rude") {
		t.Errorf("activity %s %s %s %d", action, target, details, actor)
	}
	if rec := c.req("DELETE", fmt.Sprintf("/api/reviews/%d", id), c.lib, ""); rec.Code != 404 {
		t.Errorf("removing again: %d", rec.Code)
	}
	// The author cannot post it again, or delete what was removed.
	if code := c.review(c.pat, b.ID, "PUT", `{"rating":5,"text":"rude words"}`); code != 403 {
		t.Errorf("reposting: %d", code)
	}
	if code := c.review(c.pat, b.ID, "DELETE", ""); code != 404 {
		t.Errorf("deleting a removed review: %d", code)
	}
}

// A patron cannot reach another's review: not to remove it, edit it or delete it.
func TestPatronCannotTouchAnotherReview(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.addUser("sam", goodPassword, perm.Patron)
	sam := c.signIn("sam", goodPassword)
	c.review(c.pat, b.ID, "PUT", `{"rating":4,"text":"mine"}`)
	id := c.page(c.pat, b.ID).MyReview.ID

	if rec := c.req("DELETE", fmt.Sprintf("/api/reviews/%d", id), sam, ""); rec.Code != 403 {
		t.Errorf("a patron removing: %d", rec.Code)
	}
	// Own routes act on the caller's own review only: sam has none, so 404, and pat's stays.
	if code := c.review(sam, b.ID, "DELETE", ""); code != 404 {
		t.Errorf("deleting another's by book: %d", code)
	}
	c.review(sam, b.ID, "PUT", `{"rating":1,"text":"sam's"}`)
	d := c.page(c.pat, b.ID)
	if d.MyReview.Text != "mine" || d.MyReview.Rating != 4 || c.count("reviews") != 2 {
		t.Errorf("pat's review changed: %+v", d.MyReview)
	}
	// Another user's id in the body is refused, not obeyed.
	if code := c.review(sam, b.ID, "PUT", fmt.Sprintf(`{"rating":2,"id":%d,"user_id":%d}`, id, c.libUser.ID)); code != 400 {
		t.Errorf("naming another user: %d", code)
	}
}

// A user without the permission is refused and nothing changes.
func TestReviewRoutesNeedTheirPermission(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.review(c.pat, b.ID, "PUT", `{"rating":4,"text":"hi"}`)
	if _, err := c.st.DB().Exec(`DELETE FROM user_permissions WHERE permission = 'reviews.write'`); err != nil {
		t.Fatal(err)
	}
	if code := c.review(c.pat, b.ID, "PUT", `{"rating":1}`); code != 403 {
		t.Errorf("put: %d", code)
	}
	if code := c.review(c.pat, b.ID, "DELETE", ""); code != 403 {
		t.Errorf("delete: %d", code)
	}
	if _, err := c.st.DB().Exec(`DELETE FROM user_permissions WHERE permission = 'reviews.remove'`); err != nil {
		t.Fatal(err)
	}
	if rec := c.req("DELETE", "/api/reviews/1", c.lib, ""); rec.Code != 403 {
		t.Errorf("remove: %d", rec.Code)
	}
	if n := c.count("reviews"); n != 1 || c.count("activity") != 0 {
		t.Errorf("something changed: %d reviews", n)
	}
}

// Text with markup comes back as the same plain string.
func TestReviewMarkupIsPlainText(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	text := `<script>alert(1)</script> & "quotes" <b>bold</b>`
	if code := c.review(c.pat, b.ID, "PUT", body(map[string]any{"text": text})); code != 200 {
		t.Fatal(code)
	}
	if got := c.page(c.lib, b.ID).Reviews[0].Text; got != text {
		t.Errorf("got %q", got)
	}
}

// A removed book's reviews are unreachable: its page is gone, and so is rating it.
func TestReviewOfRemovedBook(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 0)
	if rec := c.req("DELETE", fmt.Sprintf("/api/books/%d", b.ID), c.lib, ""); rec.Code != 204 && rec.Code != 200 {
		t.Fatalf("remove book: %d %s", rec.Code, rec.Body)
	}
	if code := c.review(c.pat, b.ID, "PUT", `{"rating":3}`); code != 404 {
		t.Errorf("rating a removed book: %d", code)
	}
}
