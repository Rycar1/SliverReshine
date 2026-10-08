package sliver

import "testing"

// The session-level check answers "is the shell I am holding privileged".
// privescRouteEvidence answers the different question of whether any command
// the model ran proved the route, so a one-shot escape is not reported as a
// failed run. These cases pin the narrow shape that separates the two from an
// enumeration helper that merely mentions root.

func TestPrivescRouteEvidenceLinux(t *testing.T) {
	cases := []struct {
		name   string
		steps  []AICollectStep
		want   bool
		wantEv string
	}{
		{
			name:   "one-shot escape proves the route",
			steps:  []AICollectStep{{Command: "sudo -n /usr/bin/find /tmp -exec /bin/sh -c id \\;", Output: "uid=0(root) gid=0(root) groups=0(root)\n", Status: 0}},
			want:   true,
			wantEv: "uid=0(root) gid=0(root) groups=0(root)",
		},
		{
			name:  "unprivileged id is not evidence",
			steps: []AICollectStep{{Command: "id", Output: "uid=1000(dev) gid=1000(dev) groups=1000(dev)\n", Status: 0}},
			want:  false,
		},
		{
			name:  "an enumeration that merely lists the root account is not evidence",
			steps: []AICollectStep{{Command: "cat /etc/passwd", Output: "root:x:0:0:root:/root:/bin/bash\ndev:x:1000:1000::/home/dev:/bin/bash\n", Status: 0}},
			want:  false,
		},
		{
			name:  "a refused command is never evidence",
			steps: []AICollectStep{{Command: "id", Refused: true, Refusal: "not on the allowlist", Output: "uid=0(root) gid=0(root)\n"}},
			want:  false,
		},
		{
			name:  "a command the target failed to run is never evidence",
			steps: []AICollectStep{{Command: "id", Error: "rpc: connection closed", Output: "uid=0(root) gid=0(root)\n"}},
			want:  false,
		},
		{
			name:  "a non-zero exit is never evidence",
			steps: []AICollectStep{{Command: "id", Output: "uid=0(root) gid=0(root)\n", Status: 1}},
			want:  false,
		},
		{
			name: "the most recent match wins",
			steps: []AICollectStep{
				{Command: "id", Output: "uid=0(root) gid=0(root)\n", Status: 0},
				{Command: "id", Output: "uid=0(root) gid=0(root) groups=0(root)\n", Status: 0},
			},
			want:   true,
			wantEv: "uid=0(root) gid=0(root) groups=0(root)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ev := privescRouteEvidence(tc.steps, "linux")
			if got != tc.want {
				t.Fatalf("privescRouteEvidence() = %v, want %v", got, tc.want)
			}
			if tc.wantEv != "" && ev != tc.wantEv {
				t.Fatalf("evidence = %q, want %q", ev, tc.wantEv)
			}
		})
	}
}

func TestPrivescRouteEvidenceWindows(t *testing.T) {
	cases := []struct {
		name  string
		steps []AICollectStep
		want  bool
	}{
		{
			name:  "SYSTEM token is evidence",
			steps: []AICollectStep{{Command: "whoami /groups", Output: "Mandatory Label\\System Mandatory Level  S-1-16-16384\nBUILTIN\\Administrators S-1-5-32-544\nNT AUTHORITY\\SYSTEM S-1-5-18\n", Status: 0}},
			want:  true,
		},
		{
			name:  "high integrity is evidence",
			steps: []AICollectStep{{Command: "whoami /groups", Output: "Mandatory Label\\High Mandatory Level S-1-16-12288\n", Status: 0}},
			want:  true,
		},
		{
			name:  "medium integrity is not evidence",
			steps: []AICollectStep{{Command: "whoami /groups", Output: "Mandatory Label\\Medium Mandatory Level S-1-16-8192\n", Status: 0}},
			want:  false,
		},
		{
			name:  "a uid line on a Windows target is not read as a token",
			steps: []AICollectStep{{Command: "id", Output: "uid=0(root) gid=0(root)\n", Status: 0}},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := privescRouteEvidence(tc.steps, "windows")
			if got != tc.want {
				t.Fatalf("privescRouteEvidence() = %v, want %v", got, tc.want)
			}
		})
	}
}
