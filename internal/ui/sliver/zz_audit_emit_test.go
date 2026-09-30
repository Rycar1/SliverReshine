package sliver

import (
	"os"
	"testing"
)

func wr(t *testing.T, name, s string) {
	if err := os.WriteFile(`C:\Users\Rycar\Documents\c2tool\zzout\`+name, []byte(s), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestZZAuditEmit(t *testing.T) {
	// 1. linux-cron install: payload with a double quote -> break out of echo "..."
	argv, err := installCommand(platformLinux, "linux-cron", `/tmp/a"; id; echo "`, "x")
	if err != nil {
		t.Fatal(err)
	}
	wr(t, "cron_dq.sh", argv[2])

	// 1b. same with command substitution
	argv, _ = installCommand(platformLinux, "linux-cron", `/tmp/a$(id)`, "x")
	wr(t, "cron_subst.sh", argv[2])

	// 1c. compare with linux-cron-interval, which has no extra double quotes
	argv, _ = installCommand(platformLinux, "linux-cron-interval", `/tmp/a$(id)`, "x")
	wr(t, "cronint_subst.sh", argv[2])

	// 2. sed path: payload with a newline -> multi-line sed script
	argv, _ = removeCommand(platformLinux, "linux-ssh-authorized-keys", "x\n1e id\n1", "")
	if err != nil {
		t.Fatal(err)
	}
	wr(t, "sed_nl.sh", argv[2])

	// 2b. sed path with GNU sed `e` via newline, bashrc
	argv, _ = removeCommand(platformLinux, "linux-bashrc", "p\n1e touch /tmp/ZZPWNED\n1", "")
	wr(t, "sed_nl2.sh", argv[2])

	t.Logf("cron_dq   = %s", argv[2])
}
