package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ReadAccountSecret returns an account's secret, "" when it has none.
func ReadAccountSecret(p Paths, id string) (string, error) {
	b, err := os.ReadFile(p.AccountSecret(id))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// WriteAccountSecret stores an account's secret where only its owner can
// read it, replacing the old one in one step. An empty secret removes it.
func WriteAccountSecret(p Paths, id, secret string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return fmt.Errorf("%q is not an account id", id)
	}
	if secret = strings.TrimSpace(secret); secret == "" {
		if err := os.Remove(p.AccountSecret(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	dir := p.Accounts()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+id+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.WriteString(secret + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p.AccountSecret(id))
}
