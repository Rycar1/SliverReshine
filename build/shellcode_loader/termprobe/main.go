// Terminal end-to-end probe.
//
// Speaks the console's terminal WebSocket protocol directly so the whole path
// can be exercised without a browser: upgrade, wait for the shell to say
// something, send a command, and check that the command's output comes back.
//
// It exists because "the terminal opens" and "the terminal works" are different
// claims, and the failure this was written for -- a shell that never becomes
// interactive on an older Windows -- looks identical to a healthy one from the
// outside: the socket opens, the client's own banner prints, and then nothing
// ever arrives.
//
// Usage:
//
//	go run . -url ws://127.0.0.1:17080/ws/sessions/ID/terminal -user op -pass pw
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const (
	msgData   = byte(0x01)
	msgResize = byte(0x02)
	msgClose  = byte(0x03)
)

func frame(t byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = t
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func main() {
	url := flag.String("url", "", "terminal websocket url")
	user := flag.String("user", "", "basic auth user")
	pass := flag.String("pass", "", "basic auth password")
	marker := flag.String("marker", "SLIVER-TERM-PROBE-4417", "string the shell must echo back")
	wait := flag.Duration("wait", 25*time.Second, "how long to wait for the marker")
	flag.Parse()

	if *url == "" {
		fmt.Fprintln(os.Stderr, "usage: probe -url ws://host/ws/sessions/ID/terminal -user U -pass P")
		os.Exit(2)
	}

	cfg, err := websocket.NewConfig(*url, "http://"+strings.TrimPrefix(strings.SplitN(*url, "/", 3)[2], "/"))
	if err != nil {
		fmt.Println("config error:", err)
		os.Exit(1)
	}
	if *user != "" {
		cfg.Header.Set("Authorization", "Basic "+
			base64.StdEncoding.EncodeToString([]byte(*user+":"+*pass)))
	}

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		fmt.Println("DIAL FAILED:", err)
		os.Exit(1)
	}
	defer ws.Close()
	ws.PayloadType = websocket.BinaryFrame
	fmt.Println("dial: ok")

	// Tell the console our size, the way the browser does on open.
	_, _ = ws.Write(frame(msgResize, mustJSON(map[string]int{"cols": 120, "rows": 30})))

	type chunk struct {
		t   byte
		s   string
		len int
	}
	chunks := make(chan chunk, 256)
	go func() {
		buf := make([]byte, 16384)
		for {
			n, err := ws.Read(buf)
			if n > 0 {
				raw := buf[:n]
				for len(raw) >= 5 {
					t := raw[0]
					l := int(binary.BigEndian.Uint32(raw[1:5]))
					if 5+l > len(raw) {
						break
					}
					chunks <- chunk{t: t, s: string(raw[5 : 5+l]), len: l}
					raw = raw[5+l:]
				}
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()

	// Phase 1: does the shell say anything at all on its own?
	start := time.Now()
	sawOutput := false
	deadline := time.After(8 * time.Second)
	var buf bytes.Buffer
collect:
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				fmt.Println("socket closed before any output")
				break collect
			}
			if c.t == msgData {
				if !sawOutput {
					fmt.Printf("first output after %v\n", time.Since(start).Round(time.Millisecond))
					sawOutput = true
				}
				buf.WriteString(c.s)
			}
			if c.t == msgClose {
				fmt.Printf("CLOSE FRAME (%d bytes): %s\n", c.len, strings.TrimSpace(c.s))
				if strings.TrimSpace(c.s) != "" {
					fmt.Println("RESULT: the console reported a failure")
					os.Exit(1)
				}
			}
		case <-deadline:
			break collect
		}
	}

	if !sawOutput {
		fmt.Println("RESULT: NO OUTPUT -- the shell never became interactive")
		os.Exit(1)
	}
	fmt.Printf("prompt sample: %q\n", strings.TrimSpace(buf.String()))

	// Phase 2: can it run something?
	cmd := "echo " + *marker
	if _, err := ws.Write(frame(msgData, []byte(cmd+"\r\n"))); err != nil {
		fmt.Println("write failed:", err)
		os.Exit(1)
	}
	fmt.Printf("sent: %s\n", cmd)

	buf.Reset()
	end := time.After(*wait)
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				fmt.Println("socket closed while waiting")
				os.Exit(1)
			}
			if c.t == msgData {
				buf.WriteString(c.s)
				if strings.Contains(buf.String(), *marker) {
					fmt.Println("RESULT: COMMAND RAN -- the terminal works end to end")
					return
				}
			}
		case <-end:
			fmt.Printf("RESULT: NO ECHO within %v\nreceived so far: %q\n",
				*wait, strings.TrimSpace(buf.String()))
			os.Exit(1)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

var _ = http.StatusOK
