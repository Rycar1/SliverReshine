package api

// apiRoutes is the single source of truth for the /api handlers: Routes()
// registers them and RoutePatterns() exports them, so the two cannot drift.
//
// The table is split by domain. The groups are concatenated in the order they
// appear below, and RoutePatterns() is compared byte-for-byte against the
// committed frontend fixture (frontend/src/lib/__fixtures__/routes.json), so
// moving a route between groups changes that fixture and must be intentional.
func (s *Server) apiRoutes() []route {
	groups := [][]route{
		s.coreRoutes(),
		s.sessionRoutes(),
		s.sessionOpsRoutes(),
		s.sessionExtRoutes(),
		s.postExRoutes(),
		s.beaconRoutes(),
		s.implantRoutes(),
		s.websiteRoutes(),
		s.infraRoutes(),
		s.rawRPCRoutes(),
		s.advancedRoutes(),
	}
	var routes []route
	for _, group := range groups {
		routes = append(routes, group...)
	}
	return routes
}

// coreRoutes covers info, overview, console connect/disconnect, profiles.
func (s *Server) coreRoutes() []route {
	return []route{
		{"GET", "/api/info", s.handleInfo},
		{"GET", "/api/overview", s.handleOverview},
		{"POST", "/api/connect", s.handleConnect},
		{"POST", "/api/disconnect", s.handleDisconnect},
		{"GET", "/api/profiles", s.handleListProfiles},
		{"POST", "/api/profiles/{name}", s.handleUseProfile},
	}
}

// sessionRoutes covers session lifecycle and its filesystem.
func (s *Server) sessionRoutes() []route {
	return []route{
		{"GET", "/api/sessions", s.handleSessions},
		{"POST", "/api/sessions/{id}/kill", s.handleKillSession},
		{"GET", "/api/sessions/{id}/fs", s.handleFsList},
		{"GET", "/api/sessions/{id}/fs/pwd", s.handleFsPwd},
		{"POST", "/api/sessions/{id}/fs/cd", s.handleFsCd},
		{"GET", "/api/sessions/{id}/fs/cat", s.handleFsCat},
		{"GET", "/api/sessions/{id}/fs/download", s.handleFsDownload},
		{"POST", "/api/sessions/{id}/fs/upload", s.handleFsUpload},
		{"POST", "/api/sessions/{id}/fs/mkdir", s.handleFsMkdir},
		{"DELETE", "/api/sessions/{id}/fs", s.handleFsRm},
		{"POST", "/api/sessions/{id}/fs/mv", s.handleFsMv},
	}
}

// sessionOpsRoutes covers process/network introspection, environment, exec.
func (s *Server) sessionOpsRoutes() []route {
	return []route{
		{"GET", "/api/sessions/{id}/ifconfig", s.handleIfconfig},
		{"GET", "/api/sessions/{id}/ps", s.handlePs},
		{"POST", "/api/sessions/{id}/ps/kill", s.handleKillProcess},
		{"GET", "/api/sessions/{id}/netstat", s.handleNetstat},
		{"GET", "/api/sessions/{id}/env", s.handleGetEnv},
		{"POST", "/api/sessions/{id}/env", s.handleSetEnv},
		{"DELETE", "/api/sessions/{id}/env/{key}", s.handleUnsetEnv},
		{"POST", "/api/sessions/{id}/exec", s.handleExec},
		{"GET", "/api/sessions/{id}/screenshot", s.handleScreenshot},
	}
}

// sessionExtRoutes covers extended session ops, delivery, pivots, services, msf.
func (s *Server) sessionExtRoutes() []route {
	return []route{
		// Extended session operations (P1)
		{"POST", "/api/sessions/{id}/exec-assembly", s.handleExecAssembly},
		{"POST", "/api/sessions/{id}/sideload", s.handleSideload},
		{"POST", "/api/sessions/{id}/spawn-dll", s.handleSpawnDll},
		{"POST", "/api/sessions/{id}/migrate", s.handleMigrate},
		{"POST", "/api/sessions/{id}/process-dump", s.handleProcessDump},
		{"POST", "/api/sessions/{id}/av-scan", s.handleAVScan},
		{"POST", "/api/sessions/{id}/impersonate", s.handleImpersonate},
		{"POST", "/api/sessions/{id}/make-token", s.handleMakeToken},
		{"POST", "/api/sessions/{id}/rev-to-self", s.handleRevToSelf},
		{"POST", "/api/sessions/{id}/getsystem", s.handleGetSystem},
		{"GET", "/api/sessions/{id}/privs", s.handleGetPrivs},
		{"POST", "/api/av/test", s.handleAVTest},
		{"GET", "/api/settings/auth", s.handleAuthGet},
		{"PUT", "/api/settings/auth", s.handleAuthPut},
		{"GET", "/api/sessions/{id}/token-owner", s.handleCurrentTokenOwner},
		{"POST", "/api/sessions/{id}/execute-token", s.handleExecuteToken},
		{"POST", "/api/sessions/{id}/runas", s.handleRunAs},
		{"GET", "/api/pivots/graph", s.handlePivotGraph},
		// Topology is the aggregate view: sessions, beacons, pivots and the
		// console-side proxies flattened into one node/edge list. It supersedes the
		// raw pivot tree for rendering, which is why both endpoints exist.
		{"GET", "/api/topology", s.handleTopology},
		// WebDelivery: publish a stage and hand back the one-liner that fetches it.
		{"GET", "/api/webdelivery/formats", s.handleWebDeliveryFormats},
		{"POST", "/api/webdelivery", s.handleWebDelivery},
		// One-liner: turn a running listener into a command that gets a session.
		{"GET", "/api/oneliner/targets", s.handleOneLinerTargets},
		{"POST", "/api/oneliner", s.handleOneLiner},
		// Builds for several platforms at once. Separate from /api/oneliner
		// because it is materially more expensive -- one implant build per
		// platform -- and a caller should have to ask for that.
		{"POST", "/api/oneliner/all", s.handleOneLinerAll},
		{"GET", "/api/sessions/{id}/pivots/listeners", s.handlePivotListeners},
		{"POST", "/api/sessions/{id}/pivots/listeners", s.handlePivotStartListener},
		{"DELETE", "/api/sessions/{id}/pivots/listeners/{pivotID}", s.handlePivotStopListener},
		{"POST", "/api/sessions/{id}/services", s.handleStartService},
		{"POST", "/api/sessions/{id}/services/stop", s.handleStopService},
		{"POST", "/api/sessions/{id}/services/remove", s.handleRemoveService},
		{"POST", "/api/sessions/{id}/ssh", s.handleRunSSHCommand},
		{"GET", "/api/sessions/{id}/extensions", s.handleListExtensions},
		{"POST", "/api/sessions/{id}/extensions/register", s.handleRegisterExtension},
		{"POST", "/api/sessions/{id}/extensions/call", s.handleCallExtension},
		{"POST", "/api/sessions/{id}/msf", s.handleMsf},
		{"POST", "/api/sessions/{id}/msf/remote", s.handleMsfRemote},
		{"POST", "/api/msf/stage", s.handleMsfStage},
		{"POST", "/api/sessions/{id}/backdoor", s.handleBackdoor},
		{"POST", "/api/sessions/{id}/dll-hijack", s.handleHijackDLL},
		{"POST", "/api/shellcode/rdi", s.handleShellcodeRDI},
	}
}

// postExRoutes covers persistence, mimikatz, registry, monitor.
func (s *Server) postExRoutes() []route {
	return []route{
		// Persistence (T1547/T1053/T1543 family) and credential harvesting.
		//
		// The module catalog is static, so it is served globally rather than per
		// session; everything that touches a host is scoped to a session id.
		{"GET", "/api/persistence/modules", s.handlePersistenceModules},
		{"GET", "/api/sessions/{id}/persistence", s.handlePersistenceList},
		{"POST", "/api/sessions/{id}/persistence/install", s.handlePersistenceInstall},
		{"POST", "/api/sessions/{id}/persistence/remove", s.handlePersistenceRemove},
		{"GET", "/api/mimikatz/modules", s.handleMimikatzModules},
		{"POST", "/api/sessions/{id}/mimikatz", s.handleMimikatzRun},
		{"POST", "/api/mimikatz/parse", s.handleMimikatzParse},
		{"POST", "/api/sessions/{id}/exec-shellcode", s.handleExecuteShellcode},
		{"POST", "/api/sessions/{id}/psexec", s.handlePsExec},
		{"POST", "/api/sessions/{id}/ping", s.handlePing},

		{"GET", "/api/sessions/{id}/reg/subkeys", s.handleRegSubKeys},
		{"GET", "/api/sessions/{id}/reg/values", s.handleRegValues},
		{"GET", "/api/sessions/{id}/reg/read", s.handleRegRead},
		{"POST", "/api/sessions/{id}/reg/write", s.handleRegWrite},
		{"POST", "/api/sessions/{id}/reg/create-key", s.handleRegCreateKey},
		{"POST", "/api/sessions/{id}/reg/delete-key", s.handleRegDeleteKey},
		{"POST", "/api/sessions/{id}/reconfigure", s.handleReconfigure},
		{"POST", "/api/sessions/{id}/close", s.handleCloseSession},
		{"POST", "/api/monitor/start", s.handleMonitorStart},
		{"POST", "/api/monitor/stop", s.handleMonitorStop},
		{"POST", "/api/beacons/{id}/open-session", s.handleOpenSession},
	}
}

// beaconRoutes covers port forwards, prune, aliases, beacons.
func (s *Server) beaconRoutes() []route {
	return []route{
		{"GET", "/api/portfwd", s.handlePortfwdList},
		{"POST", "/api/portfwd", s.handlePortfwdStart},
		{"DELETE", "/api/portfwd/{port}", s.handlePortfwdStop},

		{"POST", "/api/beacons/prune", s.handlePruneBeacons},
		{"POST", "/api/sessions/prune", s.handlePruneSessions},
		{"GET", "/api/aliases", s.handleAliases},
		{"POST", "/api/aliases", s.handleAliasInstall},
		{"DELETE", "/api/aliases/{name}", s.handleAliasRemove},
		{"POST", "/api/sessions/{id}/aliases/{name}/run", s.handleAliasRun},

		{"GET", "/api/beacons", s.handleBeacons},
		{"GET", "/api/beacons/{id}", s.handleBeacon},
		{"POST", "/api/beacons/{id}/rename", s.handleRenameBeacon},
		{"DELETE", "/api/beacons/{id}", s.handleRmBeacon},
		{"GET", "/api/beacons/{id}/tasks", s.handleBeaconTasks},
		{"GET", "/api/beacons/{id}/tasks/{taskID}", s.handleBeaconTaskContent},
		{"POST", "/api/sessions/{id}/rename", s.handleRenameSession},
	}
}

// implantRoutes covers implant profiles/builds, operators, hosts.
func (s *Server) implantRoutes() []route {
	return []route{
		{"GET", "/api/implant-profiles", s.handleImplantProfiles},
		{"POST", "/api/implant-profiles", s.handleSaveImplantProfile},
		{"DELETE", "/api/implant-profiles/{name}", s.handleDeleteImplantProfile},
		{"DELETE", "/api/implant-builds/{name}", s.handleDeleteImplantBuild},
		{"POST", "/api/regenerate", s.handleRegenerate},
		{"GET", "/api/operators", s.handleGetOperators},
		{"GET", "/api/compiler", s.handleCompiler},
		{"GET", "/api/hosts", s.handleHosts},
		{"GET", "/api/hosts/{uuid}", s.handleHost},
		{"DELETE", "/api/hosts/{uuid}", s.handleHostRm},
		{"DELETE", "/api/hosts/{uuid}/iocs/{iocID}", s.handleHostIOCRm},
	}
}

// websiteRoutes covers websites, canaries, wireguard.
func (s *Server) websiteRoutes() []route {
	return []route{
		{"GET", "/api/websites", s.handleWebsites},
		{"GET", "/api/websites/{name}", s.handleWebsite},
		{"POST", "/api/websites/{name}/content", s.handleWebsiteAddContent},
		{"PUT", "/api/websites/{name}/content", s.handleWebsiteUpdateContent},
		{"DELETE", "/api/websites/{name}/content", s.handleWebsiteRemoveContent},
		{"DELETE", "/api/websites/{name}", s.handleWebsiteRemove},
		{"GET", "/api/canaries", s.handleCanaries},

		{"GET", "/api/wg/config", s.handleWGClientConfig},
		{"GET", "/api/wg/ip", s.handleWGUniqueIP},
		{"GET", "/api/sessions/{id}/wg/forwarders", s.handleWGForwarders},
		{"POST", "/api/sessions/{id}/wg/forwarders", s.handleWGStartPortForward},
		{"DELETE", "/api/sessions/{id}/wg/forwarders/{fwdID}", s.handleWGStopPortForward},
		{"GET", "/api/sessions/{id}/wg/socks", s.handleWGSocksServers},
		{"POST", "/api/sessions/{id}/wg/socks", s.handleWGStartSocks},
		{"DELETE", "/api/sessions/{id}/wg/socks/{serverID}", s.handleWGStopSocks},
	}
}

// infraRoutes covers socks, loot, jobs, events, builders, listeners.
func (s *Server) infraRoutes() []route {
	return []route{
		{"GET", "/api/socks", s.handleSocksList},
		{"POST", "/api/socks", s.handleSocksStart},
		{"DELETE", "/api/socks/{id}", s.handleSocksStop},

		{"GET", "/api/loot", s.handleLootAll},
		{"POST", "/api/loot", s.handleLootAdd},
		{"POST", "/api/loot/{id}/rename", s.handleLootRename},
		{"GET", "/api/loot/{id}", s.handleLootContent},
		{"DELETE", "/api/loot/{id}", s.handleLootRemove},
		{"GET", "/api/jobs", s.handleJobs},
		{"GET", "/api/events", s.handleEvents},
		{"GET", "/api/builders", s.handleBuilders},
		{"POST", "/api/generate", s.handleGenerate},
		{"POST", "/api/listeners", s.handleListeners},
		{"DELETE", "/api/listeners/{id}", s.handleStopListener},

		// Forward (bind) listeners. Kept on their own paths rather than folded
		// into /api/listeners because they are the opposite operation: nothing
		// binds locally, and the server dials out. Mixing them into one
		// collection would put a "port" on an entry that has none and invite
		// exactly the confusion this split avoids.
		{"GET", "/api/listeners/bind", s.handleBindList},
		{"POST", "/api/listeners/bind", s.handleBindStart},
		{"DELETE", "/api/listeners/bind/{id}", s.handleBindStop},
	}
}

// rawRPCRoutes covers raw RPC console.
func (s *Server) rawRPCRoutes() []route {
	return []route{
		// Raw RPC console — reaches every method on the SliverRPC surface, not
		// just the ones with a hand-written page. This is what makes the
		// "full Sliver feature set" claim hold for methods the UI never
		// modelled (armory, crackstation, anything added upstream).
		{"GET", "/api/rpc/methods", s.handleRPCMethods},
		{"POST", "/api/rpc/call", s.handleRPCCall},
	}
}

// advancedRoutes covers creds, memfiles, encoders, certs, tunnels, reg hive.
func (s *Server) advancedRoutes() []route {
	return []route{
		// --- Post-exploitation surface normally only reachable from the TUI ---

		// Credential vault: server-side store of harvested hashes and plaintexts.
		{"GET", "/api/creds", s.handleCreds},
		{"POST", "/api/creds", s.handleCredsAdd},
		{"PUT", "/api/creds", s.handleCredsUpdate},
		{"DELETE", "/api/creds", s.handleCredsRemove},
		{"GET", "/api/creds/filter", s.handleCredsByHashType},
		{"POST", "/api/creds/sniff", s.handleCredsSniff},
		{"GET", "/api/creds/{id}", s.handleCredByID},

		// Memfiles: anonymous in-memory files on the target (no disk artefact).
		{"GET", "/api/sessions/{id}/memfiles", s.handleMemfilesList},
		{"POST", "/api/sessions/{id}/memfiles", s.handleMemfilesAdd},
		{"DELETE", "/api/sessions/{id}/memfiles", s.handleMemfilesRemove},

		// File attributes and content search.
		{"POST", "/api/sessions/{id}/fs/chmod", s.handleChmod},
		{"POST", "/api/sessions/{id}/fs/chown", s.handleChown},
		{"POST", "/api/sessions/{id}/fs/chtimes", s.handleChtimes},
		{"POST", "/api/sessions/{id}/fs/grep", s.handleGrep},

		// Keylogger telemetry sinks.
		{"GET", "/api/monitor/providers", s.handleMonitorProviders},
		{"POST", "/api/monitor/providers", s.handleMonitorAdd},
		{"DELETE", "/api/monitor/providers", s.handleMonitorRemove},

		// C2 profiles: reshape implant HTTP traffic.
		{"GET", "/api/c2profiles", s.handleC2Profiles},
		{"POST", "/api/c2profiles", s.handleC2ProfileSave},
		{"GET", "/api/c2profiles/{name}", s.handleC2Profile},

		// Traffic and shellcode encoders.
		{"GET", "/api/traffic-encoders", s.handleTrafficEncoders},
		{"POST", "/api/traffic-encoders", s.handleTrafficEncoderAdd},
		{"DELETE", "/api/traffic-encoders/{name}", s.handleTrafficEncoderRemove},
		{"GET", "/api/shellcode-encoders", s.handleShellcodeEncoders},
		{"POST", "/api/shellcode-encoders", s.handleShellcodeEncode},

		// WASM extensions (the sibling of the existing BOF support).
		{"GET", "/api/sessions/{id}/wasm", s.handleWasmExtensions},
		{"POST", "/api/sessions/{id}/wasm/register", s.handleWasmRegister},
		{"POST", "/api/sessions/{id}/wasm/exec", s.handleWasmExec},

		// Reverse port forwards.
		{"GET", "/api/sessions/{id}/rportfwd", s.handleRportFwdList},
		{"POST", "/api/sessions/{id}/rportfwd", s.handleRportFwdStart},
		{"DELETE", "/api/sessions/{id}/rportfwd/{fwdID}", s.handleRportFwdStop},

		// Certificates.
		{"GET", "/api/certificates/ca", s.handleCACertificates},
		{"GET", "/api/certificates", s.handleCertificates},

		// Tunnels.
		{"POST", "/api/sessions/{id}/tunnel", s.handleTunnelCreate},
		{"DELETE", "/api/tunnels", s.handleTunnelClose},

		// Windows service detail and start-by-name.
		{"POST", "/api/sessions/{id}/services/detail", s.handleServiceDetail},
		{"POST", "/api/sessions/{id}/services/start-by-name", s.handleServiceStartByName},

		// Whole-hive registry extraction (SAM/SECURITY/SYSTEM collection).
		{"POST", "/api/sessions/{id}/reg/hive", s.handleRegistryHive},
	}
}
