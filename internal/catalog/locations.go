package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/store"
)

// Location is a named place copies are kept, with how many copies it holds.
type Location struct {
	ID     int64
	Name   string
	Copies int
}

// LocationInUse is the conflict for deleting a location that holds copies.
// It is an *apperr.Error too (errors.As finds it).
type LocationInUse struct {
	Copies int
	err    *apperr.Error
}

func (e *LocationInUse) Error() string { return e.err.Error() }
func (e *LocationInUse) Unwrap() error { return e.err }

var errLocationNotFound = apperr.New(apperr.NotFound, "That location does not exist.")

// Locations lists every location by name, with its copy count.
func (s *Service) Locations(ctx context.Context) ([]Location, error) {
	counts, err := s.copies.CountByLocation(ctx, s.st.DB())
	if err != nil {
		return nil, err
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id, name FROM locations ORDER BY name_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Location{}
	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.ID, &l.Name); err != nil {
			return nil, err
		}
		l.Copies = counts[l.ID]
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateLocation adds a location. A name another location has, ignoring
// case, is refused.
func (s *Service) CreateLocation(ctx context.Context, actor int64, name string) (Location, error) {
	name, err := cleanText(name, "name", "location name", MaxLocation, true)
	if err != nil {
		return Location{}, err
	}
	var id int64
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := checkNameFree(ctx, tx, name, 0); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO locations (name, name_key) VALUES (?, ?)`, name, strings.ToLower(name))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return Location{ID: id, Name: name}, err
}

// RenameLocation renames a location, for all its copies. Changing only the
// case of its own name is allowed.
func (s *Service) RenameLocation(ctx context.Context, actor, id int64, name string) (Location, error) {
	name, err := cleanText(name, "name", "location name", MaxLocation, true)
	if err != nil {
		return Location{}, err
	}
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := locationExists(ctx, tx, id); err != nil {
			return err
		}
		if err := checkNameFree(ctx, tx, name, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE locations SET name = ?, name_key = ? WHERE id = ?`, name, strings.ToLower(name), id)
		return err
	})
	if err != nil {
		return Location{}, err
	}
	return Location{ID: id, Name: name}, nil
}

// DeleteLocation deletes an empty location. One that holds copies is a
// *LocationInUse that says how many.
func (s *Service) DeleteLocation(ctx context.Context, actor, id int64) error {
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := locationExists(ctx, tx, id); err != nil {
			return err
		}
		counts, err := s.copies.CountByLocation(ctx, tx)
		if err != nil {
			return err
		}
		if n := counts[id]; n > 0 {
			return &LocationInUse{Copies: n, err: apperr.New(apperr.Conflict,
				fmt.Sprintf("This location cannot be deleted: %s there. Move %s to another location first.", plural(n, "copy is", "copies are"), plural1(n)))}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM locations WHERE id = ?`, id)
		return err
	})
}

func plural1(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func locationExists(ctx context.Context, q DB, id int64) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM locations WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return errLocationNotFound
	}
	return err
}

func checkNameFree(ctx context.Context, q DB, name string, except int64) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM locations WHERE name_key = ? AND id <> ?`, strings.ToLower(name), except).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return apperr.OnField(apperr.Conflict, "name", "A location with this name already exists.")
}
