//go:build windows

package sliver

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// The argv builders have a shape test elsewhere. This file goes one step
// further and hands their output to the real icacls.exe on a scratch file: an
// argument list that matches the expected shape but that the tool rejects would
// still be a broken feature, and only the tool can settle that. It also reads
// the ACL back, because a command that exits zero without changing anything is
// the failure mode this code exists to avoid.
func TestRealIcaclsAcceptsTheGeneratedArgv(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Fatalf("cannot determine the current account: %v", err)
	}
	file := filepath.Join(t.TempDir(), "scratch.txt")
	if err := os.WriteFile(file, []byte("scratch"), 0o600); err != nil {
		t.Fatal(err)
	}

	grant := windowsGrantArgs(file, me.Username, "RX", false)
	out, err := exec.Command(icaclsPath, grant...).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %v failed: %v\n%s", grant, err, out)
	}

	acl, err := exec.Command(icaclsPath, file).CombinedOutput()
	if err != nil {
		t.Fatalf("reading the ACL back failed: %v\n%s", err, acl)
	}
	// The account has to appear with the right that was granted. The listing
	// also carries the account's inherited entries -- this one already owns the
	// file -- so the check is for a line that names the account *and* the
	// granted right, not merely for the account.
	granted := false
	for _, l := range strings.Split(string(acl), "\n") {
		if strings.Contains(l, me.Username) && strings.Contains(strings.ToUpper(l), "(RX)") {
			granted = true
		}
	}
	if !granted {
		t.Fatalf("the granted right is not in the ACL:\ngrant=%v\ngrant out=%s\nfull acl:\n%s", grant, out, acl)
	}

	// /setowner on the account that already owns the file needs no privilege
	// beyond being the owner, so this exercises the flag without depending on
	// the test account holding SeRestorePrivilege.
	owner := windowsSetOwnerArgs(file, me.Username, false)
	if out, err := exec.Command(icaclsPath, owner...).CombinedOutput(); err != nil {
		t.Fatalf("icacls %v failed: %v\n%s", owner, err, out)
	}

	// Recursion is a flag icacls has to accept, so it is exercised too.
	rec := windowsGrantArgs(filepath.Dir(file), me.Username, "RX", true)
	if out, err := exec.Command(icaclsPath, rec...).CombinedOutput(); err != nil {
		t.Fatalf("icacls %v failed: %v\n%s", rec, err, out)
	}
}
