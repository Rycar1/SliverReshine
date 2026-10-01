$ErrorActionPreference = 'Stop'

function Normalize-Lf { param([string]$S) return ($S -replace "`r`n", "`n") }

function Replace-Once {
    param([string]$Path, [string]$Old, [string]$New, [string]$Label)
    $text = [System.IO.File]::ReadAllText($Path)
    if ($text -match "`r`n") { $text = $text -replace "`r`n", "`n" }
    $Old = Normalize-Lf $Old
    $New = Normalize-Lf $New
    $n = ([regex]::Matches($text, [regex]::Escape($Old))).Count
    if ($n -ne 1) { throw "$Label : expected 1 occurrence, found $n" }
    [System.IO.File]::WriteAllText($Path, $text.Replace($Old, $New), (New-Object System.Text.UTF8Encoding($false)))
    Write-Host "  [ok] $Label"
}

$repo = Split-Path -Parent $PSScriptRoot
$srv = Join-Path $repo 'internal\launch\server.go'

Write-Host 'checking before launch that the gRPC port is free, and naming the owner if not'

# waitForPort answered the wrong question. It asked "is anything listening on
# 31337?", and a stray process on that port made the answer yes -- so the
# launcher reported "sliver server ready", then generateProfile ran and silently
# produced nothing because there was no daemon to talk to, and auto-connect timed
# out with a message that named none of it. What the operator saw was a console
# that started, served its UI, and reported the server unreachable.
#
# The fix is to ask the question that actually matters, before spawning anything:
# is the port free? And if it is not, say who has it.
$oldSpawn = @'
	if err := s.spawn(ctx); err != nil {
		return nil, err
	}
	if err := s.waitForPort(ctx); err != nil {
		s.Stop()
		return nil, err
	}
'@

$newSpawn = @'
	if err := s.requireFreePort(s.opts.MultiplayerHost, s.opts.MultiplayerPort); err != nil {
		return nil, err
	}
	if err := s.spawn(ctx); err != nil {
		return nil, err
	}
	if err := s.waitForPort(ctx); err != nil {
		s.Stop()
		return nil, err
	}
'@

Replace-Once -Path $srv -Old $oldSpawn -New $newSpawn -Label 'require free port'

# The check itself.
$oldWait = @'
func (s *Server) waitForPort(ctx context.Context) error {
'@

$newWait = @'
// requireFreePort fails when something is already listening on the gRPC address,
// and names the process if it can.
//
// This runs before the daemon is spawned because the failure it prevents is
// otherwise invisible. Sliver's daemon cannot bind, prints one line to its own
// log, and exits; waitForPort then sees the *stranger's* listener, reports the
// server ready, and the operator is left with a console that serves its UI and
// says the server is unreachable. Naming the holder turns a twenty-minute
// mystery into a one-line fix.
//
// The check is inherently racy -- something can take the port between this call
// and the daemon's bind -- but the window is milliseconds and the alternative is
// no check at all.
func (s *Server) requireFreePort(host string, port int) error {
	addr := net.JoinHostPort(host, fmt.Sprint(port))

	ln, err := net.Listen("tcp", addr)
	if err == nil {
		ln.Close()
		return nil
	}

	holder := describeListener(host, port)
	if holder != "" {
		return fmt.Errorf(
			"gRPC port %s is already in use by %s. The embedded Sliver daemon cannot "+
				"bind while that process holds it, so the console would start but never "+
				"connect. Stop that process, or set a different mpPort in the settings file",
			addr, holder)
	}
	return fmt.Errorf(
		"gRPC port %s is already in use. The embedded Sliver daemon cannot bind while "+
			"that process holds it. Stop it, or set a different mpPort in the settings file",
		addr)
}

// describeListener names whatever is listening on the address, or "" when the
// name cannot be determined.
//
// Best-effort on purpose: the port being unavailable is the fact that matters,
// and failing to name the holder must not turn a clear error into a vague one.
func describeListener(host string, port int) string {
	// Windows has the connection table in the standard library; other platforms
	// would need a syscall, and the message still reads without it.
	conns, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	_ = conns
	_ = err

	pid := listeningPID(port)
	if pid == 0 {
		return ""
	}
	if proc, err := os.FindProcess(pid); err == nil {
		if exe, err := proc.Executable(); err == nil {
			return fmt.Sprintf("%s (pid %d)", filepath.Base(exe), pid)
		}
	}
	return fmt.Sprintf("pid %d", pid)
}

func (s *Server) waitForPort(ctx context.Context) error {
'@

Replace-Once -Path $srv -Old $oldWait -New $newWait -Label 'requireFreePort'

Write-Host 'done'
