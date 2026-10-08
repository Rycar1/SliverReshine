package sliver

// The two maps in this file remember what Sliver's Job does not.
//
// A Job carries a name, a port, a description and a list of domains. It does not
// carry the website an HTTP listener serves, nor the address it was bound to,
// and both are needed to produce a one-liner that works:
//
//   - the website decides where a stage has to be published. Publishing to a
//     different one leaves the listener answering 404 while the listener, the
//     build and the publish all report success;
//   - the bind address is the only address the console knows for sure the
//     listener is reachable at. Without it a listener started on 192.168.1.9
//     looks identical to one started on 0.0.0.0, and the callback address falls
//     back to the wildcard -- an implant told to dial 0.0.0.0 dials itself.
//
// So both are recorded here, keyed by job id, when this console starts a
// listener. See persistedJobMap for the storage.

// listenerSitesFile is the file the listener-to-website map is persisted to,
// inside the console's profile directory.
//
// Persisting it is what makes the association outlive the process: without it a
// console restart forgets which website a listener serves, the one-liner refuses
// to publish ("the website it serves is unknown"), and the only way to recover
// is to stop and restart the listener.
const listenerSitesFile = "listener-sites.json"

// listenerHostsFile is the file the listener-to-callback-address map is
// persisted to, beside the one above and for the same reason.
const listenerHostsFile = "listener-hosts.json"

// listenerMaps returns the two persisted maps, creating them on first use.
//
// Lazy creation keeps a zero-value Client usable in tests and in any code path
// that never starts a listener. The maps are created once, so every caller
// shares one instance and one lock.
func (c *Client) listenerMaps() (*persistedJobMap, *persistedJobMap) {
	c.mapsMu.Lock()
	defer c.mapsMu.Unlock()
	if c.sites == nil {
		c.sites = newPersistedJobMap(listenerSitesFile, "listener websites")
	}
	if c.hosts == nil {
		c.hosts = newPersistedJobMap(listenerHostsFile, "listener callback addresses")
	}
	return c.sites, c.hosts
}

// rememberListenerSite records which website a listener serves.
func (c *Client) rememberListenerSite(jobID uint32, website string) {
	if c.root != nil {
		c.root.rememberListenerSite(jobID, website)
		return
	}
	sites, _ := c.listenerMaps()
	sites.remember(jobID, website)
}

// listenerSite returns the website a listener serves and whether it is known.
func (c *Client) listenerSite(jobID uint32) (string, bool) {
	if c.root != nil {
		return c.root.listenerSite(jobID)
	}
	sites, _ := c.listenerMaps()
	return sites.get(jobID)
}

// rememberListenerHost records the address a listener this console started is
// reachable at, so a one-liner built for it dials something real.
//
// The value is stored as given, wildcards included: the resolver decides what is
// usable, and keeping the raw value means the console can say "bound to 0.0.0.0"
// rather than "unknown" when it has to explain why it cannot build a command.
func (c *Client) rememberListenerHost(jobID uint32, host string) {
	if c.root != nil {
		c.root.rememberListenerHost(jobID, host)
		return
	}
	_, hosts := c.listenerMaps()
	hosts.remember(jobID, host)
}

// listenerHost returns the address a listener is reachable at and whether it is
// known.
func (c *Client) listenerHost(jobID uint32) (string, bool) {
	if c.root != nil {
		return c.root.listenerHost(jobID)
	}
	_, hosts := c.listenerMaps()
	return hosts.get(jobID)
}
