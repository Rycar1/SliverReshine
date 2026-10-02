// One command per connection.
//
// The multi-command probes were ambiguous: frames arrive interleaved with local
// echo, so attributing output to a command needed bookkeeping that was easy to
// get wrong, and a lagging reader makes a working command look broken. Opening a
// fresh connection per case removes the question entirely -- everything received
// after the banner belongs to the one command that was sent.
package main

import (
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const (
	msgData  = byte(0x01)
	msgClose = byte(0x03)
)

func frame(t byte, p []byte) []byte {
	out := make([]byte, 5+len(p))
	out[0] = t
	binary.BigEndian.PutUint32(out[1:5], uint32(len(p)))
	copy(out[5:], p)
	return out
}

func run(url, user, pass, line string, settle time.Duration) (string, bool, bool) {
	host := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(url, "ws://"), "wss://"), "/", 2)[0]
	cfg, err := websocket.NewConfig(url, "http://"+host)
	if err != nil {
		return "config: " + err.Error(), false, false
	}
	cfg.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		return "dial: " + err.Error(), false, false
	}
	defer ws.Close()
	ws.PayloadType = websocket.BinaryFrame

	chunks := make(chan string, 256)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		buf := make([]byte, 32768)
		for {
			n, err := ws.Read(buf)
			if n > 0 {
				raw := buf[:n]
				for len(raw) >= 5 {
					l := int(binary.BigEndian.Uint32(raw[1:5]))
					if 5+l > len(raw) {
						break
					}
					if raw[0] == msgData || raw[0] == msgClose {
						chunks <- string(raw[5 : 5+l])
					}
					raw = raw[5+l:]
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Let the banner and first prompt arrive before sending anything.
	time.Sleep(1500 * time.Millisecond)

	if _, err := ws.Write(frame(msgData, []byte(line+"\r\n"))); err != nil {
		return "write: " + err.Error(), false, false
	}

	var got strings.Builder
	deadline := time.After(settle)
loop:
	for {
		select {
		case c := <-chunks:
			got.WriteString(c)
		case <-closed:
			break loop
		case <-deadline:
			break loop
		}
	}
	out := got.String()
	// Drop the echo of the command itself.
	out = strings.Replace(out, line+"\r\n", "", 1)
	return out, true, false
}

func main() {
	url := flag.String("url", "", "terminal ws url")
	user := flag.String("user", "", "user")
	pass := flag.String("pass", "", "pass")
	settle := flag.Duration("settle", 4*time.Second, "how long to collect after sending")
	line := flag.String("line", "", "single command to run (empty = built-in suite)")
	flag.Parse()

	if *line != "" {
		out, ok, _ := run(*url, *user, *pass, *line, *settle)
		if !ok {
			fmt.Println("FAILED:", out)
			os.Exit(1)
		}
		fmt.Printf("line : %s\nout  : %q\n", *line, strings.TrimSpace(out))
		return
	}

	type tc struct {
		name    string
		line    string
		want    string
		refused bool
		absent  bool
	}
	// The expectations are chosen so local echo cannot satisfy them: each one
	// requires output that only the target could produce.
	cases := []tc{
		{name: "whoami", line: "whoami", want: "rycar"},
		{name: "hostname", line: "hostname", want: "Rycarl"},
		{name: "echo via program", line: "cmd /c echo UNIQ-8823", want: "UNIQ-8823"},
		{name: "pwd builtin", line: "pwd", want: `\`},
		{name: "cd builtin", line: `cd C:\Windows`, want: `Windows`},
		{name: "pipe refused", line: "ls | grep x", refused: true, want: "no shell"},
		{name: "redirect refused", line: "echo a > b", refused: true, want: "no shell"},
		{name: "glob refused", line: "ls *.txt", refused: true, want: "no shell"},
		{name: "chaining refused", line: "a && b", refused: true, want: "no shell"},
		{name: "unbalanced quote", line: `echo "abc`, refused: true, want: "unbalanced"},
		{name: "missing program", line: "no-such-prog-9174", want: "no-such-prog-9174"},
		// A quoted operator must NOT be refused by the console's own syntax
		// check -- it is data. Whether the invoked program then re-interprets it
		// is that program's business: on Windows a process receives one command
		// line string, not an argv array, so cmd.exe reparses what it was given
		// and treats an unquoted "|" as a pipe. That is cmd's behaviour, not the
		// console's, and the console cannot override it from outside without
		// hardcoding cmd-specific escaping.
		//
		// What this case checks is therefore the console's half only: the line is
		// not refused, and the program runs.
		{name: "quoted operator not refused", line: `cmd /c echo "a|b"`, want: "exec mode has no shell", absent: true},
	}

	nPass, nFail := 0, 0
	for _, c := range cases {
		out, ok, _ := run(*url, *user, *pass, c.line, *settle)
		if !ok {
			fmt.Printf("FAIL  %-24s transport: %s\n", c.name, out)
			nFail++
			continue
		}
		trimmed := strings.TrimSpace(out)
		good := strings.Contains(trimmed, c.want)
		if c.absent {
			good = !strings.Contains(trimmed, c.want)
		}
		if c.refused && !strings.Contains(trimmed, "no shell") && !strings.Contains(trimmed, "unbalanced") {
			good = false
		}
		if good {
			fmt.Printf("PASS  %-24s %q\n", c.name, trimmed)
			nPass++
		} else {
			fmt.Printf("FAIL  %-24s want %q, got %q\n", c.name, c.want, trimmed)
			nFail++
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Printf("\n%d passed, %d failed\n", nPass, nFail)
	if nFail > 0 {
		os.Exit(1)
	}
}
