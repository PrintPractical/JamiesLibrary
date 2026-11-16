package store

import (
	"database/sql/driver"
	"strings"

	"modernc.org/sqlite"
)

// Fold lower-cases s for a case-insensitive comparison of any script, which
// SQLite's own LIKE and NOCASE do for ASCII only. The database has it as the
// SQL function fold(), so a query folds a column and what was typed alike.
func Fold(s string) string {
	return strings.ToLower(strings.ToUpper(s))
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("fold", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case string:
				return Fold(v), nil
			case []byte:
				return Fold(string(v)), nil
			default:
				return v, nil
			}
		})
}
