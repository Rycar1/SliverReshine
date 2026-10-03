package sliver

import (
	"fmt"
	"strings"
	"sync"
)

// PersistenceList inspects every module for the platform and reports what the
// target actually has. A missing artifact is a negative result, not a Go error:
// reg query and schtasks /query exit non-zero when the value is simply absent.
//
// name is optional and only affects name-scoped modules. Supplying it lets a
// scheduled task, service or local account be answered for real; leaving it
// empty marks those rows unknown instead of reporting a host as clean.
//
// The name is validated by the same requireName path every install goes
// through, so an unvalidated query parameter can never reach a command line.
func (c *Client) PersistenceList(sessionID, platform, name string) (*PersistenceList, error) {
	if platform != platformWindows && platform != platformLinux {
		return nil, fmt.Errorf("unknown platform %q", platform)
	}
	if name != "" {
		if err := validateName(name); err != nil {
			return nil, err
		}
	}

	// The same name is asked of every module. Name-scoped ones use it; the rest
	// ignore it, so one inventory pass answers for a specific artifact and still
	// reports the anonymous state of everything else.
	//
	// The probes run concurrently. Each one is a separate round trip over the
	// implant's C2 channel, and Sliver dispatches every envelope on the target in
	// its own goroutine with the response keyed by request ID, so N probes finish
	// in roughly the time of the slowest rather than the sum of all N. Serially,
	// fifteen modules meant fifteen channel round trips and the tab sat empty for
	// seconds. The order of results is fixed by moduleCatalog regardless of the
	// order the probes complete in, so the UI stays stable.
	type probe struct {
		module     string
		label      string
		nameScoped bool
		installed  bool
		detail     string
	}

	probes := make([]probe, 0, len(moduleCatalog))
	for _, m := range moduleCatalog {
		if !supportsPlatform(m, platform) {
			continue
		}
		if _, err := inspectCommand(platform, m.ID, name); err != nil {
			continue
		}
		probes = append(probes, probe{
			module:     m.ID,
			label:      m.Name,
			nameScoped: nameModules[m.ID],
		})
	}

	var wg sync.WaitGroup
	for i := range probes {
		wg.Add(1)
		go func(p *probe) {
			defer wg.Done()
			argv, err := inspectCommand(platform, p.module, name)
			if err != nil {
				return
			}
			stdout, stderr, status := c.runPersistenceCommand(sessionID, platform, argv)
			p.installed, p.detail = detectPersistence(p.module, stdout, stderr, status, name)
		}(&probes[i])
	}
	wg.Wait()

	list := &PersistenceList{Platform: platform, Items: []PersistenceItem{}}
	for _, p := range probes {
		// A name-scoped module cannot be answered without a name. Saying so is
		// the difference between "the host is clean" and "I could not check",
		// and only one of those is true here. When the caller supplies a name the
		// question has been asked, so a negative answer is a real answer.
		unknown := p.nameScoped && name == "" && !p.installed
		detail := p.detail
		if unknown && detail == "" {
			detail = "needs an artifact name to check"
		}

		list.Items = append(list.Items, PersistenceItem{
			Module:    p.module,
			Name:      p.label,
			Location:  locationFor(p.module, name),
			Installed: p.installed,
			Detail:    detail,
			Unknown:   unknown,
			// A row can only be acted on when the inventory knows which artifact it
			// describes. Name-scoped modules are removable only once a name has
			// been supplied, because otherwise the removal would be rejected for
			// being incomplete and the button would do nothing.
			Removable: p.installed && (!p.nameScoped || name != ""),
		})
	}
	return list, nil
}

// PersistenceInstall applies module to the target. A target-level failure is
// reported inside the result, so only catalog errors surface as Go errors.
func (c *Client) PersistenceInstall(sessionID, platform, module, payload, name string) (*PersistenceResult, error) {
	argv, err := installCommand(platform, module, payload, name)
	if err != nil {
		return nil, err
	}
	stdout, stderr, status := c.runPersistenceCommand(sessionID, platform, argv)
	ok, msg := persistenceOutcome(stdout, stderr, status)
	return &PersistenceResult{OK: ok, Message: msg, Module: module, Location: locationFor(module, name)}, nil
}

// PersistenceRemove deletes module from the target.
func (c *Client) PersistenceRemove(sessionID, platform, module, name string) (*PersistenceResult, error) {
	argv, err := removeCommand(platform, module, "", name)
	if err != nil {
		return nil, err
	}
	stdout, stderr, status := c.runPersistenceCommand(sessionID, platform, argv)
	ok, msg := persistenceOutcome(stdout, stderr, status)
	return &PersistenceResult{OK: ok, Message: msg, Module: module, Location: locationFor(module, name)}, nil
}

// runPersistenceCommand flattens the exec outcome into target-facing strings: the
// HTTP layer surfaces these verbatim, so a transport error has to survive as text.
//
// On Windows the script is prefixed with `chcp 65001`, which switches that
// cmd.exe instance to UTF-8. reg, sc and schtasks print their messages in the
// OEM code page -- 936 on a Chinese install, 850 on a Western one -- so without
// this every success and failure message arrives as bytes that are not valid
// UTF-8 and reaches the operator as mojibake ("操作成功完成。" renders as
// "���������"). Decoding afterwards would mean guessing the target's code page
// from the bytes; asking for UTF-8 is exact and needs no guess.
//
// platform is passed through to the spawn so it does not have to be resolved
// from the session list. Every caller already knows it -- it selected the
// command family -- and a probe that stopped to look it up would add a round
// trip to each of the fifteen the inventory already makes.
func (c *Client) runPersistenceCommand(sessionID, platform string, argv []string) (stdout, stderr string, status uint32) {
	if len(argv) < 3 {
		return "", "internal error: incomplete command", 1
	}

	path := argv[0]
	args := argv[1:]
	if strings.EqualFold(filepathBase(path), "cmd.exe") {
		// chcp is prepended as its own tokens, not spliced into the first one.
		// shellArgv hands cmd.exe a token list (see splitWindowsCommand), so
		// args[1] is the command name -- gluing "chcp ... & " onto it would
		// produce a single token "chcp 65001 >nul & reg" and cmd would look for
		// a program by that name. The redirection stays attached to >nul.
		args = append([]string{args[0], "chcp", "65001", ">nul", "&"}, args[1:]...)
	}

	res, err := c.execOn(sessionID, platform, path, args, persistenceProbeTimeout)
	if err != nil {
		return "", err.Error(), 1
	}
	return res.Stdout, res.Stderr, res.Status
}

// filepathBase is path.Base for the Windows-agnostic case: the argv[0] here is
// always a bare program name, so it only has to tolerate a trailing separator.
func filepathBase(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// persistenceOutcome maps a shell result to the JSON result the UI toasts.
func persistenceOutcome(stdout, stderr string, status uint32) (bool, string) {
	msg := firstNonEmptyLine(stderr)
	if msg == "" {
		msg = firstNonEmptyLine(stdout)
	}
	switch {
	case status != 0 && msg == "":
		return false, fmt.Sprintf("command failed with status %d", status)
	case status != 0:
		return false, msg
	case msg == "":
		return true, "ok"
	}
	return true, msg
}
