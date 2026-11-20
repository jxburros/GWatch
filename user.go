package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/dbconfig"
	"github.com/jxburros/GWatch/internal/store"
)

func runUserCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: gwatch user add [flags] <username> | gwatch user reset-password [flags] <username>")
	}
	action := args[0]
	if action != "add" && action != "reset-password" {
		return fmt.Errorf("unknown user command %q (use add or reset-password)", action)
	}
	flags := flag.NewFlagSet("gwatch user "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", envOr("GWATCH_DATA_DIR", defaultDataDir()), "directory containing the GWatch database")
	roleName := flags.String("role", "viewer", "role for a new account: admin or viewer")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: gwatch user %s [flags] <username>", action)
	}
	username := strings.TrimSpace(flags.Arg(0))
	if err := validateCLIUsername(username); err != nil {
		return err
	}
	if action == "add" && *roleName != "admin" && *roleName != "viewer" {
		return fmt.Errorf("unknown role %q (use admin or viewer)", *roleName)
	}
	fmt.Fprint(stderr, "Password (read from standard input): ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read password: %w", err)
	}
	password := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	ctx := context.Background()
	cfg, _, err := dbconfig.Resolve(*dataDir, dbconfig.Overrides{})
	if err != nil {
		return fmt.Errorf("resolve database: %w", err)
	}
	db, err := store.OpenDSN(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	switch action {
	case "add":
		count, err := db.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("count accounts: %w", err)
		}
		role := auth.ParseRole(*roleName)
		if count == 0 {
			role = auth.RoleAdmin
		}
		user, err := db.CreateUser(ctx, username, hash, role)
		if err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return fmt.Errorf("account %q already exists", username)
			}
			return fmt.Errorf("create account: %w", err)
		}
		fmt.Fprintf(stdout, "Created %s account %q (id %d).\n", user.Role, user.Username, user.ID)
	case "reset-password":
		user, _, err := db.GetUserByName(ctx, username)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("account %q does not exist", username)
			}
			return fmt.Errorf("find account: %w", err)
		}
		if err := db.SetUserPassword(ctx, user.ID, hash); err != nil {
			return fmt.Errorf("reset password: %w", err)
		}
		fmt.Fprintf(stdout, "Password reset for %q; all of its browser sessions have been signed out.\n", user.Username)
	}
	return nil
}

func validateCLIUsername(username string) error {
	if len(username) < 2 {
		return errors.New("the user name must be at least 2 characters")
	}
	if len(username) > 64 {
		return errors.New("the user name is too long")
	}
	for _, r := range username {
		if r < 0x20 || r == 0x7f {
			return errors.New("the user name contains a control character")
		}
	}
	return nil
}
