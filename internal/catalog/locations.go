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
	counts, err := countByLocation(ctx, s.st.DB())
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
	var l Location
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		var err error
		l, err = insertLocation(ctx, tx, name, "name")
		return err
	})
	return l, err
}

// insertLocation cleans a location name and adds it in the caller's
// transaction. field names the input in an error.
func insertLocation(ctx context.Context, q DB, name, field string) (Location, error) {
	name, err := cleanText(name, field, "location name", MaxLocation, true, false)
	if err != nil {
		return Location{}, err
	}
	if err := checkNameFree(ctx, q, name, 0, field); err != nil {
		return Location{}, err
	}
	res, err := q.ExecContext(ctx, `INSERT INTO locations (name, name_key) VALUES (?, ?)`, name, strings.ToLower(name))
	if err != nil {
		return Location{}, err
	}
	id, err := res.LastInsertId()
	return Location{ID: id, Name: name}, err
}

// RenameLocation renames a location, for all its copies. Changing only the
// case of its own name is allowed.
func (s *Service) RenameLocation(ctx context.Context, actor, id int64, name string) (Location, error) {
	name, err := cleanText(name, "name", "location name", MaxLocation, true, false)
	if err != nil {
		return Location{}, err
	}
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := locationExists(ctx, tx, id); err != nil {
			return err
		}
		if err := checkNameFree(ctx, tx, name, id, "name"); err != nil {
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
	return s.deleteLocation(ctx, id, 0)
}

// MoveAndDeleteLocation moves every copy in the location to another and then
// deletes it, in one transaction: nothing moves if the delete fails.
func (s *Service) MoveAndDeleteLocation(ctx context.Context, actor, id, to int64) error {
	if id == to {
		return apperr.OnField(apperr.Invalid, "move_to", "Choose a different location to move the copies to.")
	}
	return s.deleteLocation(ctx, id, to)
}

// deleteLocation deletes a location; with to set, its copies move there first.
func (s *Service) deleteLocation(ctx context.Context, id, to int64) error {
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := locationExists(ctx, tx, id); err != nil {
			return err
		}
		if to != 0 {
			if err := mustHaveLocation(ctx, tx, to); err != nil {
				return apperr.OnField(apperr.Invalid, "move_to", "The location to move the copies to does not exist.")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE copies SET location_id = ? WHERE location_id = ? AND removed_at IS NULL`, to, id); err != nil {
				return err
			}
		}
		counts, err := countByLocation(ctx, tx)
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

func checkNameFree(ctx context.Context, q DB, name string, except int64, field string) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM locations WHERE name_key = ? AND id <> ?`, strings.ToLower(name), except).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return apperr.OnField(apperr.Conflict, field, "A location with this name already exists.")
}
