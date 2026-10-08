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
		s.aiRoutes(),
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
		{"GET", "/api/overview", s.withClient(s.handleOverview)},
		{"POST", "/api/connect", s.handleConnect},
		{"POST", "/api/disconnect", s.handleDisconnect},
		{"GET", "/api/profiles", s.handleListProfiles},
		{"POST", "/api/profiles/{name}", s.handleUseProfile},
	}
}

// sessionRoutes covers session lifecycle and its filesystem.
func (s *Server) sessionRoutes() []route {
	return []route{
		{"GET", "/api/sessions", s.withClient(s.handleSessions)},
		{"POST", "/api/sessions/{id}/kill", s.withClient(s.handleKillSession)},
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
		{"GET", "/api/pivots/graph", s.withClient(s.handlePivotGraph)},
		// Topology is the aggregate view: sessions, beacons, pivots and the
		// console-side proxies flattened into one node/edge list. It supersedes the
		// raw pivot tree for rendering, which is why both endpoints exist.
		{"GET", "/api/topology", s.withClient(s.handleTopology)},
		// WebDelivery: publish a stage and hand back the one-liner that fetches it.
		{"GET", "/api/webdelivery/formats", s.handleWebDeliveryFormats},
		{"POST", "/api/webdelivery", s.withClient(s.handleWebDelivery)},
		// One-liner: turn a running listener into a command that gets a session.
		{"GET", "/api/oneliner/targets", s.withClient(s.handleOneLinerTargets)},
		{"POST", "/api/oneliner", s.withClient(s.handleOneLiner)},
		// Builds for several platforms at once. Separate from /api/oneliner
		// because it is materially more expensive -- one implant build per
		// platform -- and a caller should have to ask for that.
		{"POST", "/api/oneliner/all", s.withClient(s.handleOneLinerAll)},
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
		{"POST", "/api/msf/stage", s.withClient(s.handleMsfStage)},
		{"POST", "/api/sessions/{id}/backdoor", s.handleBackdoor},
		{"POST", "/api/sessions/{id}/dll-hijack", s.handleHijackDLL},
		{"POST", "/api/shellcode/rdi", s.withClient(s.handleShellcodeRDI)},
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
		{"POST", "/api/mimikatz/parse", s.withClient(s.handleMimikatzParse)},
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
		{"POST", "/api/monitor/start", s.withClient(s.handleMonitorStart)},
		{"POST", "/api/monitor/stop", s.withClient(s.handleMonitorStop)},
		{"POST", "/api/beacons/{id}/open-session", s.withClient(s.handleOpenSession)},
	}
}

// beaconRoutes covers port forwards, prune, aliases, beacons.
func (s *Server) beaconRoutes() []route {
	return []route{
		{"GET", "/api/portfwd", s.withClient(s.handlePortfwdList)},
		{"POST", "/api/portfwd", s.withClient(s.handlePortfwdStart)},
		{"DELETE", "/api/portfwd/{port}", s.withClient(s.handlePortfwdStop)},

		{"POST", "/api/beacons/prune", s.withClient(s.handlePruneBeacons)},
		{"POST", "/api/sessions/prune", s.withClient(s.handlePruneSessions)},
		{"GET", "/api/aliases", s.withClient(s.handleAliases)},
		{"POST", "/api/aliases", s.withClient(s.handleAliasInstall)},
		{"DELETE", "/api/aliases/{name}", s.withClient(s.handleAliasRemove)},
		{"POST", "/api/sessions/{id}/aliases/{name}/run", s.handleAliasRun},

		{"GET", "/api/beacons", s.withClient(s.handleBeacons)},
		{"GET", "/api/beacons/{id}", s.withClient(s.handleBeacon)},
		{"POST", "/api/beacons/{id}/rename", s.withClient(s.handleRenameBeacon)},
		{"DELETE", "/api/beacons/{id}", s.withClient(s.handleRmBeacon)},
		{"GET", "/api/beacons/{id}/tasks", s.withClient(s.handleBeaconTasks)},
		{"GET", "/api/beacons/{id}/tasks/{taskID}", s.withClient(s.handleBeaconTaskContent)},
		{"POST", "/api/sessions/{id}/rename", s.withClient(s.handleRenameSession)},
	}
}

// implantRoutes covers implant profiles/builds, operators, hosts.
func (s *Server) implantRoutes() []route {
	return []route{
		{"GET", "/api/implant-profiles", s.withClient(s.handleImplantProfiles)},
		{"POST", "/api/implant-profiles", s.withClient(s.handleSaveImplantProfile)},
		{"DELETE", "/api/implant-profiles/{name}", s.withClient(s.handleDeleteImplantProfile)},
		{"DELETE", "/api/implant-builds/{name}", s.withClient(s.handleDeleteImplantBuild)},
		{"POST", "/api/regenerate", s.withClient(s.handleRegenerate)},
		{"GET", "/api/operators", s.withClient(s.handleGetOperators)},
		{"GET", "/api/compiler", s.withClient(s.handleCompiler)},
		{"GET", "/api/hosts", s.withClient(s.handleHosts)},
		{"GET", "/api/hosts/{uuid}", s.withClient(s.handleHost)},
		{"DELETE", "/api/hosts/{uuid}", s.withClient(s.handleHostRm)},
		{"DELETE", "/api/hosts/{uuid}/iocs/{iocID}", s.withClient(s.handleHostIOCRm)},
	}
}

// websiteRoutes covers websites, canaries, wireguard.
func (s *Server) websiteRoutes() []route {
	return []route{
		{"GET", "/api/websites", s.withClient(s.handleWebsites)},
		{"GET", "/api/websites/{name}", s.withClient(s.handleWebsite)},
		{"POST", "/api/websites/{name}/content", s.withClient(s.handleWebsiteAddContent)},
		{"PUT", "/api/websites/{name}/content", s.withClient(s.handleWebsiteUpdateContent)},
		{"DELETE", "/api/websites/{name}/content", s.withClient(s.handleWebsiteRemoveContent)},
		{"DELETE", "/api/websites/{name}", s.withClient(s.handleWebsiteRemove)},
		{"GET", "/api/canaries", s.withClient(s.handleCanaries)},

		{"GET", "/api/wg/config", s.withClient(s.handleWGClientConfig)},
		{"GET", "/api/wg/ip", s.withClient(s.handleWGUniqueIP)},
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
		{"GET", "/api/socks", s.withClient(s.handleSocksList)},
		{"POST", "/api/socks", s.withClient(s.handleSocksStart)},
		{"DELETE", "/api/socks/{id}", s.withClient(s.handleSocksStop)},

		{"GET", "/api/loot", s.withClient(s.handleLootAll)},
		{"POST", "/api/loot", s.withClient(s.handleLootAdd)},
		{"POST", "/api/loot/{id}/rename", s.withClient(s.handleLootRename)},
		{"GET", "/api/loot/{id}", s.withClient(s.handleLootContent)},
		{"DELETE", "/api/loot/{id}", s.withClient(s.handleLootRemove)},
		{"GET", "/api/jobs", s.withClient(s.handleJobs)},
		{"GET", "/api/events", s.withClient(s.handleEvents)},
		{"GET", "/api/builders", s.withClient(s.handleBuilders)},
		{"POST", "/api/generate", s.withClient(s.handleGenerate)},
		{"POST", "/api/listeners", s.withClient(s.handleListeners)},
		{"DELETE", "/api/listeners/{id}", s.withClient(s.handleStopListener)},

		// Forward (bind) listeners. Kept on their own paths rather than folded
		// into /api/listeners because they are the opposite operation: nothing
		// binds locally, and the server dials out. Mixing them into one
		// collection would put a "port" on an entry that has none and invite
		// exactly the confusion this split avoids.
		{"GET", "/api/listeners/bind", s.withClient(s.handleBindList)},
		{"POST", "/api/listeners/bind", s.withClient(s.handleBindStart)},
		{"DELETE", "/api/listeners/bind/{id}", s.withClient(s.handleBindStop)},
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
		{"POST", "/api/rpc/call", s.withClient(s.handleRPCCall)},
	}
}

// advancedRoutes covers creds, memfiles, encoders, certs, tunnels, reg hive.
func (s *Server) advancedRoutes() []route {
	return []route{
		// --- Post-exploitation surface normally only reachable from the TUI ---

		// Credential vault: server-side store of harvested hashes and plaintexts.
		{"GET", "/api/creds", s.withClient(s.handleCreds)},
		{"POST", "/api/creds", s.withClient(s.handleCredsAdd)},
		{"PUT", "/api/creds", s.withClient(s.handleCredsUpdate)},
		{"DELETE", "/api/creds", s.withClient(s.handleCredsRemove)},
		{"GET", "/api/creds/filter", s.withClient(s.handleCredsByHashType)},
		{"POST", "/api/creds/sniff", s.withClient(s.handleCredsSniff)},
		{"GET", "/api/creds/{id}", s.withClient(s.handleCredByID)},

		// Memfiles: anonymous in-memory files on the target (no disk artefact).
		{"GET", "/api/sessions/{id}/memfiles", s.handleMemfilesList},
		{"POST", "/api/sessions/{id}/memfiles", s.handleMemfilesAdd},
		{"DELETE", "/api/sessions/{id}/memfiles", s.handleMemfilesRemove},

		// File attributes and content search.
		{"POST", "/api/sessions/{id}/fs/chmod", s.handleChmod},
		{"POST", "/api/sessions/{id}/fs/chown", s.handleChown},
		// Windows has no chmod: permissions there are ACL entries, so the same
		// intent is expressed with an account and a permission level.
		{"POST", "/api/sessions/{id}/fs/acl", s.handleGrantACL},
		{"POST", "/api/sessions/{id}/fs/chtimes", s.handleChtimes},
		{"POST", "/api/sessions/{id}/fs/grep", s.handleGrep},

		// Keylogger telemetry sinks.
		{"GET", "/api/monitor/providers", s.withClient(s.handleMonitorProviders)},
		{"POST", "/api/monitor/providers", s.withClient(s.handleMonitorAdd)},
		{"DELETE", "/api/monitor/providers", s.withClient(s.handleMonitorRemove)},

		// C2 profiles: reshape implant HTTP traffic.
		{"GET", "/api/c2profiles", s.withClient(s.handleC2Profiles)},
		{"POST", "/api/c2profiles", s.withClient(s.handleC2ProfileSave)},
		{"GET", "/api/c2profiles/{name}", s.withClient(s.handleC2Profile)},

		// Traffic and shellcode encoders.
		{"GET", "/api/traffic-encoders", s.withClient(s.handleTrafficEncoders)},
		{"POST", "/api/traffic-encoders", s.withClient(s.handleTrafficEncoderAdd)},
		{"DELETE", "/api/traffic-encoders/{name}", s.withClient(s.handleTrafficEncoderRemove)},
		{"GET", "/api/shellcode-encoders", s.withClient(s.handleShellcodeEncoders)},
		{"POST", "/api/shellcode-encoders", s.withClient(s.handleShellcodeEncode)},

		// WASM extensions (the sibling of the existing BOF support).
		{"GET", "/api/sessions/{id}/wasm", s.handleWasmExtensions},
		{"POST", "/api/sessions/{id}/wasm/register", s.handleWasmRegister},
		{"POST", "/api/sessions/{id}/wasm/exec", s.handleWasmExec},

		// Reverse port forwards.
		{"GET", "/api/sessions/{id}/rportfwd", s.handleRportFwdList},
		{"POST", "/api/sessions/{id}/rportfwd", s.handleRportFwdStart},
		{"DELETE", "/api/sessions/{id}/rportfwd/{fwdID}", s.handleRportFwdStop},

		// Certificates.
		{"GET", "/api/certificates/ca", s.withClient(s.handleCACertificates)},
		{"GET", "/api/certificates", s.withClient(s.handleCertificates)},

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

// aiRoutes covers the model-backed assistant.
//
// It is a group of its own because the set of features that use a model is
// expected to grow -- summarising a session, drafting a report, suggesting the
// next step -- and everything that talks to internal/ai should register here
// rather than being scattered through the domain groups, where the shared
// configuration that makes them work is not visible.
func (s *Server) aiRoutes() []route {
	return []route{
		// Whether the assistant is available. It never returns the API key.
		{"GET", "/api/ai/status", s.handleAIStatus},
		// Check one command against the read-only policy without running it, so
		// a refusal is reproducible without spending a model call.
		{"POST", "/api/ai/read-only/check", s.handleAIReadOnlyCheck},
		// Runtime model configuration: read and write the endpoint, model and
		// key without editing the settings file by hand. The write is applied
		// to the running service, so no restart is needed.
		// List the models the endpoint advertises, so the panel can offer a
		// choice instead of a free-text model name. It accepts an unsaved
		// endpoint and key and falls back to the stored ones.
		{"POST", "/api/ai/models", s.handleAIModels},
		{"GET", "/api/settings/ai", s.handleAISettingsGet},
		{"PUT", "/api/settings/ai", s.handleAISettingsPut},
		// Run the collector against one session. It is a session route because
		// the assistant is scoped to a single live target.
		{"POST", "/api/sessions/{id}/ai-collect", s.handleAICollect},
		// Attempt privilege escalation on one session: enumerate, let the model
		// choose a route, run it, and verify the result. Like the collector it
		// is a session route, and it streams its progress.
		{"POST", "/api/sessions/{id}/ai-privesc", s.handleAIPrivesc},
	}
}
