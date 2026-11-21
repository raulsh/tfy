package store

import (
	"context"
	"path/filepath"
	"testing"
)

// A new database starts with this machine's Claude login as its one account,
// used by default.
func TestOpenSeedsTheMachineAccount(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tfy.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	accounts, err := st.Q.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ID != "machine" || accounts[0].Cli != "claude" || accounts[0].Kind != "machine" {
		t.Fatalf("accounts = %+v", accounts)
	}
	if accounts[0].CreatedAt.IsZero() {
		t.Error("the seeded account's timestamp did not parse")
	}
	runs, err := st.Q.GetAppSetting(ctx, "runs")
	if err != nil || runs != `{"accounts":["machine"],"stages":{}}` {
		t.Errorf("runs setting = %q (%v)", runs, err)
	}
}
