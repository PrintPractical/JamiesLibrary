package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

// CardDigits is the length of a library card number (security design §15).
const CardDigits = 12

// maxCardTries bounds the redraws when a card number is taken. With 10^12
// numbers a second draw is already vanishingly unlikely.
const maxCardTries = 20

var cardSpace = new(big.Int).Exp(big.NewInt(10), big.NewInt(CardDigits), nil)

var errNoSuchPatron = apperr.New(apperr.NotFound, "No such patron.")

// drawCode is how a card number is drawn; tests replace it to force a collision.
var drawCode = newCardCode

// newCardCode draws a card number from crypto/rand: never derived from an ID or the time.
func newCardCode() (string, error) {
	n, err := rand.Int(rand.Reader, cardSpace)
	if err != nil {
		return "", fmt.Errorf("drawing a card number: %w", err)
	}
	return fmt.Sprintf("%0*d", CardDigits, n), nil
}

// IssueCard gives the user a new card number inside the caller's transaction
// and returns it. A number that any identifier (card or copy) already holds is
// redrawn. It refuses a user who already has a card: use ReissueCard.
func (s *Service) IssueCard(ctx context.Context, tx store.Tx, userID int64) (string, error) {
	for i := 0; i < maxCardTries; i++ {
		code, err := drawCode()
		if err != nil {
			return "", err
		}
		var one int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM identifiers WHERE code = ?`, code).Scan(&one)
		if err == nil {
			continue // taken by a card or a copy: draw again
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO identifiers (code, kind, user_id) VALUES (?, ?, ?)`, code, catalog.KindCard, userID); err != nil {
			return "", fmt.Errorf("issuing a card: %w", err)
		}
		return code, nil
	}
	return "", errors.New("no free card number could be drawn")
}

// ReissueCard replaces the user's card inside the caller's transaction: the old
// number is deleted, so it identifies no one, and a new one is issued. The
// actor must hold every permission of the user (security design §4(b)), read
// in the same transaction. The caller records the activity entry in tx too.
func (s *Service) ReissueCard(ctx context.Context, tx store.Tx, actor perm.Set, userID int64) (string, error) {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errNotFound
		}
		return "", err
	}
	target, err := permissionsTx(ctx, tx, userID)
	if err != nil {
		return "", err
	}
	if !target.SubsetOf(actor) {
		return "", apperr.New(apperr.Forbidden, "You can't change the card of someone who has permissions you don't have.")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM identifiers WHERE user_id = ? AND kind = ?`, userID, catalog.KindCard); err != nil {
		return "", err
	}
	return s.IssueCard(ctx, tx, userID)
}

func permissionsTx(ctx context.Context, tx store.Tx, userID int64) (perm.Set, error) {
	rows, err := tx.QueryContext(ctx, `SELECT permission FROM user_permissions WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := perm.Set{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		// an ID this version doesn't know grants nothing, so it can't block either
		if perm.Valid(perm.Permission(p)) {
			set[perm.Permission(p)] = struct{}{}
		}
	}
	return set, rows.Err()
}

// Card returns the user's card number. A user without one gets a not-found
// error that says what to do.
func (s *Service) Card(ctx context.Context, userID int64) (string, error) {
	var code string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT code FROM identifiers WHERE user_id = ? AND kind = ?`, userID, catalog.KindCard).Scan(&code)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apperr.New(apperr.NotFound, "You have no library card yet. Ask a librarian to issue one.")
	}
	return code, err
}

// Patron is who a card identifies.
type Patron struct {
	ID          int64
	Username    string
	DisplayName string
	Active      bool
}

// PatronByCard finds who the card number identifies. A number that is
// malformed, unknown or old, or that is a copy's code, is the same "no such
// patron". The code is normalised like every scanned code.
func (s *Service) PatronByCard(ctx context.Context, code string) (Patron, error) {
	code, err := catalog.NormaliseCode(code)
	if err != nil {
		return Patron{}, errNoSuchPatron
	}
	var p Patron
	err = s.st.DB().QueryRowContext(ctx, `
		SELECT u.id, u.username, u.display_name, u.active
		FROM identifiers i JOIN users u ON u.id = i.user_id
		WHERE i.code = ? AND i.kind = ?`, code, catalog.KindCard).Scan(&p.ID, &p.Username, &p.DisplayName, &p.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return Patron{}, errNoSuchPatron
	}
	return p, err
}
