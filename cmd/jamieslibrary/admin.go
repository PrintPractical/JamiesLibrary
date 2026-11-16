package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"jamieslibrary/internal/recovery"
)

const adminUsage = "usage: jamieslibrary admin reset-password [--data DIR] [--make-admin] USERNAME"

// runAdmin is the commands the owner runs at the server itself.
func runAdmin(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprintln(stderr, adminUsage)
		return 2
	}
	dataDefault := "./data"
	if v := strings.TrimSpace(getenv("JL_DATA_DIR")); v != "" {
		dataDefault = v
	}
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", dataDefault, "data directory (JL_DATA_DIR)")
	makeAdmin := fs.Bool("make-admin", false, "also give the user the administrator role")
	// flags may come before or after the username
	var names []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			fmt.Fprintln(stderr, "jamieslibrary:", err)
			fmt.Fprintln(stderr, adminUsage)
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		names = append(names, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(names) != 1 || strings.TrimSpace(*dataDir) == "" {
		fmt.Fprintln(stderr, adminUsage)
		return 2
	}
	res, err := recovery.ResetPassword(context.Background(), strings.TrimSpace(*dataDir), names[0], *makeAdmin)
	if err != nil {
		fmt.Fprintln(stderr, "jamieslibrary:", err)
		return 1
	}
	if !res.WasAdministrator && !res.MadeAdministrator {
		fmt.Fprintf(stderr, "Note: %s is not an administrator. Their password was reset all the same; add --make-admin to make them one.\n", res.Username)
	}
	if res.Inactive {
		fmt.Fprintf(stderr, "Note: %s is deactivated and can't sign in until an administrator reactivates them.\n", res.Username)
	}
	if res.MadeAdministrator && !res.WasAdministrator {
		fmt.Fprintf(stdout, "%s is now an administrator.\n", res.Username)
	}
	fmt.Fprintf(stdout, "Temporary password for %s: %s\n", res.Username, res.TemporaryPassword)
	fmt.Fprintln(stdout, "They must choose a new password at their first sign-in. All their sessions have ended.")
	return 0
}
