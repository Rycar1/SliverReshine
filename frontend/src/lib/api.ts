import type {
  Session,
  Beacon,
  Event,
  ImplantBuild,
  ImplantConfig,
  GenerateResult,
  Job,
  BindListener,
  DirView,
  NetInterface,
  ProcessInfo,
  SockEntry,
  EnvVar,
  ExecResult,
  PortForward,
  BeaconTask,
  SocksProxy,
  ImplantProfile,
  ServerInfo,
  OverviewData,
  LootEntry,
  CompilerInfo,
  Host,
  Website,
  WGClientConfig,
  WGTCPForwarder,
  WGSocksServer,
  WindowsPrivilege,
  PivotListener,
  PivotGraphEntry,
  TopologyGraph,
  WebDeliveryFormatInfo,
  WebDeliveryRequest,
  WebDeliveryResult,
  SSHCommandResult,
  CallExtensionResult,
  MsfStager,
  Canary,
  Alias,
  RpcMethod,
  RpcCallResult,
  VaultCredential,
  GrepOut,
  MonitorProvider,
  C2Profile,
  ShellcodeEncoder,
  TrafficEncoderReport,
  CertificateInfo,
  RportFwdListener,
  ServiceDetail,
  AVProcess,
  AVScanResult,
  PersistenceModule,
  PersistenceList,
  PersistenceResult,
  MimikatzMode,
  MimikatzResult,
  AuthSettings,
} from './types'

const BASE = '/api'

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    cache: 'no-store',
    ...options,
  })
  const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }))
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`)
  if (body.error) throw new Error(body.error)
  return body as T
}

/**
 * Pull the filename out of a Content-Disposition header.
 *
 * Only the quoted form is handled, which is the only one this console sends.
 * An unparseable header returns '' so the caller falls back rather than
 * producing a download named "undefined".
 */
function filenameFromDisposition(header: string | null): string {
  if (!header) return ''
  const m = /filename="([^"]*)"/.exec(header)
  return m ? m[1] : ''
}

export const api = {
  info: () => request<ServerInfo>('/info'),

  overview: () => request<OverviewData>('/overview'),

  connect: (config: { content: string }) =>
    request<{ success: boolean }>('/connect', { method: 'POST', body: JSON.stringify(config) }),

  disconnect: () => request<{ success: boolean }>('/disconnect', { method: 'POST' }),

  listProfiles: () => request<{ profiles: string[] }>('/profiles'),

  useProfile: (name: string) => request<{ success: boolean }>(`/profiles/${name}`, { method: 'POST' }),

  sessions: () => request<{ sessions: Session[] }>('/sessions'),

  beacons: () => request<{ beacons: Beacon[] }>('/beacons'),

  beacon: (id: string) => request<Beacon>(`/beacons/${encodeURIComponent(id)}`),

  openSessionFromBeacon: (beaconId: string) =>
    request<{ success: boolean; async?: boolean }>(`/beacons/${encodeURIComponent(beaconId)}/open-session`, {
      method: 'POST',
    }),

  closeSession: (sessionId: string) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/close`, { method: 'POST' }),

  monitorStart: () => request<{ success: boolean }>('/monitor/start', { method: 'POST' }),

  monitorStop: () => request<{ success: boolean }>('/monitor/stop', { method: 'POST' }),

  jobs: () => request<{ jobs: Job[] }>('/jobs'),

  events: () => request<{ events: Event[] }>('/events'),

  builders: () => request<{ builders: ImplantBuild[] }>('/builders'),

  generate: (config: Partial<ImplantConfig>) =>
    request<GenerateResult>(`/generate`, { method: 'POST', body: JSON.stringify(config) }),

  startListener: (job: { type: string; addr: string; port: number; tls: boolean }) =>
    request<{ success: boolean }>('/listeners', { method: 'POST', body: JSON.stringify(job) }),

  stopListener: (jobId: number) =>
    request<{ success: boolean }>(`/listeners/${jobId}`, { method: 'DELETE' }),

  // Forward (bind) listeners. These are the mirror image of startListener: no
  // local port is bound and the server dials a port the implant is listening
  // on, which is what makes a session possible when the target cannot reach
  // out. A successful dialBind means the dialer is running, not that a session
  // exists yet — the implant may still be starting, and the server retries.
  bindListeners: () => request<{ listeners: BindListener[] }>('/listeners/bind'),

  dialBind: (host: string, port: number) =>
    request<BindListener>('/listeners/bind', {
      method: 'POST',
      body: JSON.stringify({ host, port }),
    }),

  stopBindListener: (jobId: number) =>
    request<{ success: boolean }>(`/listeners/bind/${jobId}`, { method: 'DELETE' }),

  killSession: (sessionId: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/kill`, { method: 'POST' }),

  renameSession: (sessionId: string, name: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/rename`, {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  renameBeacon: (beaconId: string, name: string) =>
    request<{ success: boolean }>(`/beacons/${beaconId}/rename`, {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  rmBeacon: (beaconId: string) =>
    request<{ success: boolean }>(`/beacons/${beaconId}`, { method: 'DELETE' }),

  beaconTasks: (beaconId: string) => request<{ tasks: BeaconTask[] }>(`/beacons/${beaconId}/tasks`),

  beaconTaskContent: (beaconId: string, taskId: string) =>
    request<BeaconTask>(`/beacons/${beaconId}/tasks/${taskId}`),

  lootList: (type?: string) => {
    const q = type ? `?type=${encodeURIComponent(type)}` : ''
    return request<{ loot: LootEntry[] }>(`/loot${q}`)
  },

  lootContent: (id: string) => request<LootEntry>(`/loot/${encodeURIComponent(id)}`),

  lootRemove: (id: string) => request<{ success: boolean }>(`/loot/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  lootAdd: (body: {
    type: 'file' | 'credential'
    name: string
    file_name?: string
    file_type?: 'text' | 'binary'
    file_data_b64?: string
    cred_user?: string
    cred_password?: string
    cred_api_key?: string
  }) => request<{ success: boolean; id?: string }>('/loot', { method: 'POST', body: JSON.stringify(body) }),

  lootRename: (id: string, name: string) =>
    request<{ success: boolean }>(`/loot/${encodeURIComponent(id)}/rename`, {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  implantProfiles: () => request<{ profiles: ImplantProfile[] }>('/implant-profiles'),

  saveImplantProfile: (body: {
    name: string
    is_beacon: boolean
    config: Partial<ImplantConfig>
  }) =>
    request<{ success: boolean }>('/implant-profiles', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  deleteImplantProfile: (name: string) =>
    request<{ success: boolean }>(`/implant-profiles/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    }),

  socksList: () => request<{ proxies: SocksProxy[] }>('/socks'),

  socksStart: (req: {
    session_id: string
    bind_addr?: string
    bind_port?: number
    username?: string
    password?: string
  }) =>
    request<{ success: boolean; id?: number; bindAddr?: string; bindPort?: number }>('/socks', {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  socksStop: (id: number) => request<{ success: boolean }>(`/socks/${id}`, { method: 'DELETE' }),

  // --- Filesystem ---
  fsList: (sessionId: string, path?: string) => {
    const q = path ? `?path=${encodeURIComponent(path)}` : ''
    return request<DirView>(`/sessions/${sessionId}/fs${q}`)
  },
  fsPwd: (sessionId: string) => request<{ Path: string }>(`/sessions/${sessionId}/fs/pwd`),
  fsCd: (sessionId: string, path: string) =>
    request<{ Path: string }>(`/sessions/${sessionId}/fs/cd`, { method: 'POST', body: JSON.stringify({ path }) }),
  fsCat: (sessionId: string, path: string) =>
    request<{ Data: string; Name: string }>(
      `/sessions/${sessionId}/fs/cat?path=${encodeURIComponent(path)}`,
    ),
  /**
   * Fetch a file from the target as raw bytes.
   *
   * This returns a Blob rather than going through request<T>(): the endpoint
   * streams the file itself, not JSON, because base64 through JSON inflated the
   * payload by a third and forced the whole thing to be buffered as a string on
   * both ends before anything could be saved.
   */
  fsDownload: async (
    sessionId: string,
    path: string,
  ): Promise<{ blob: Blob; name: string }> => {
    const res = await fetch(
      `${BASE}/sessions/${sessionId}/fs/download?path=${encodeURIComponent(path)}`,
      { cache: 'no-store' },
    )
    if (!res.ok) {
      // Error responses are still JSON, so the message survives.
      const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }))
      throw new Error(body.error || `HTTP ${res.status}`)
    }
    // The server names the file in Content-Disposition; fall back to the last
    // path element so the operator never sees an unnamed download.
    const name = filenameFromDisposition(res.headers.get('Content-Disposition')) || path.split(/[/\\]/).pop() || 'download'
    return { blob: await res.blob(), name }
  },
  fsUpload: (sessionId: string, path: string, data: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/fs/upload`, {
      method: 'POST',
      body: JSON.stringify({ path, data }),
    }),
  fsMkdir: (sessionId: string, path: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/fs/mkdir`, {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),
  fsRm: (sessionId: string, path: string, recursive = false) =>
    request<{ success: boolean }>(
      `/sessions/${sessionId}/fs?path=${encodeURIComponent(path)}&recursive=${recursive}`,
      { method: 'DELETE' },
    ),
  fsMv: (sessionId: string, src: string, dst: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/fs/mv`, {
      method: 'POST',
      body: JSON.stringify({ src, dst }),
    }),

  // --- Recon ---
  ifconfig: (sessionId: string) => request<{ interfaces: NetInterface[] }>(`/sessions/${sessionId}/ifconfig`),
  ps: (sessionId: string) => request<{ processes: ProcessInfo[] }>(`/sessions/${sessionId}/ps`),
  killProcess: (sessionId: string, pid: number, force = false) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/ps/kill`, {
      method: 'POST',
      body: JSON.stringify({ pid, force }),
    }),
  netstat: (sessionId: string) => request<{ entries: SockEntry[] }>(`/sessions/${sessionId}/netstat`),
  env: (sessionId: string) => request<{ env: EnvVar[] }>(`/sessions/${sessionId}/env`),
  setEnv: (sessionId: string, key: string, value: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/env`, {
      method: 'POST',
      body: JSON.stringify({ key, value }),
    }),
  unsetEnv: (sessionId: string, key: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/env/${key}`, { method: 'DELETE' }),
  exec: (sessionId: string, path: string, args: string[]) =>
    request<ExecResult>(`/sessions/${sessionId}/exec`, {
      method: 'POST',
      body: JSON.stringify({ path, args }),
    }),
  screenshot: (sessionId: string) => request<{ Data: string }>(`/sessions/${sessionId}/screenshot`),

  // --- Registry ---
  regSubKeys: (sessionId: string, hive: string, path: string) =>
    request<{ keys: string[] }>(
      `/sessions/${sessionId}/reg/subkeys?hive=${encodeURIComponent(hive)}&path=${encodeURIComponent(path)}`,
    ),
  regValues: (sessionId: string, hive: string, path: string) =>
    request<{ values: string[] }>(
      `/sessions/${sessionId}/reg/values?hive=${encodeURIComponent(hive)}&path=${encodeURIComponent(path)}`,
    ),
  regRead: (sessionId: string, hive: string, path: string, key: string) =>
    request<{ Value: string }>(
      `/sessions/${sessionId}/reg/read?hive=${encodeURIComponent(hive)}&path=${encodeURIComponent(path)}&key=${encodeURIComponent(key)}`,
    ),
  regWrite: (sessionId: string, hive: string, path: string, key: string, value: string, type?: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/reg/write`, {
      method: 'POST',
      body: JSON.stringify({ hive, path, key, value, type: type || 'string' }),
    }),
  regDeleteKey: (sessionId: string, hive: string, path: string, key: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/reg/delete-key`, {
      method: 'POST',
      body: JSON.stringify({ hive, path, key }),
    }),

  reconfigureSession: (sessionId: string, reconnectInterval: number) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/reconfigure`, {
      method: 'POST',
      body: JSON.stringify({ reconnect_interval: reconnectInterval }),
    }),

  // --- Port forwarding ---
  portfwdList: () => request<{ forwards: PortForward[] }>('/portfwd'),
  portfwdStart: (req: {
    session_id: string
    bind_addr?: string
    bind_port?: number
    remote_host?: string
    remote_port: number
  }) => request<{ success: boolean; localAddr?: string; localPort?: number }>('/portfwd', {
    method: 'POST',
    body: JSON.stringify(req),
  }),
  portfwdStop: (port: number) => request<{ success: boolean }>(`/portfwd/${port}`, { method: 'DELETE' }),

  // --- Extended operations (P1) ---
  execAssembly: (sessionId: string, assembly: string, assemblyArgs: string, process: string) =>
    request<{ output: string }>(`/sessions/${sessionId}/exec-assembly`, {
      method: 'POST',
      body: JSON.stringify({ assembly, arguments: assemblyArgs, process }),
    }),

  sideload: (sessionId: string, data: string, processName: string, args: string, entryPoint: string) =>
    request<{ result: string }>(`/sessions/${sessionId}/sideload`, {
      method: 'POST',
      body: JSON.stringify({ data, processName, args, entryPoint }),
    }),

  spawnDll: (sessionId: string, data: string, processName: string, args: string, entryPoint: string) =>
    request<{ result: string }>(`/sessions/${sessionId}/spawn-dll`, {
      method: 'POST',
      body: JSON.stringify({ data, processName, args, entryPoint }),
    }),

  // Migrate injects a freshly generated implant into another process. The
  // server rebuilds the C2 config from the session, so only the target is
  // needed here. Pass either a pid or a process name.
  migrate: (sessionId: string, pid: number, procName = '') =>
    request<{ success: boolean }>(`/sessions/${sessionId}/migrate`, {
      method: 'POST',
      body: JSON.stringify({ pid, procName }),
    }),

  // processDump streams a minidump back as a file download rather than JSON:
  // base64 through JSON inflated the payload by a third and forced the whole
  // dump to be buffered as a string before it could be saved.
  processDump: async (sessionId: string, pid: number): Promise<Blob> => {
    const res = await fetch(`${BASE}/sessions/${sessionId}/process-dump`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      cache: 'no-store',
      body: JSON.stringify({ pid }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }))
      throw new Error(body.error || `HTTP ${res.status}`)
    }
    return res.blob()
  },

  // avScan asks the server to identify the target's processes against an
  // external fingerprint database. It runs server-side so the browser never
  // talks to a third party and no CORS headers are needed.
  avScan: (sessionId: string, filter = '', database = '', url = '') =>
    request<AVScanResult>(`/sessions/${sessionId}/av-scan`, {
      method: 'POST',
      body: JSON.stringify({ filter, database, url }),
    }),

  avTest: (database = '', url = '') =>
    request<{ success: boolean; database: string; resolved: number; sample: AVProcess[] }>(
      '/av/test',
      { method: 'POST', body: JSON.stringify({ database, url }) },
    ),

  impersonate: (sessionId: string, username: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/impersonate`, {
      method: 'POST',
      body: JSON.stringify({ username }),
    }),

  makeToken: (sessionId: string, username: string, password: string, domain: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/make-token`, {
      method: 'POST',
      body: JSON.stringify({ username, password, domain }),
    }),

  revToSelf: (sessionId: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/rev-to-self`, { method: 'POST' }),

  getSystem: (sessionId: string, hostingProcess: string) =>
    request<{ success: boolean }>(`/sessions/${sessionId}/getsystem`, {
      method: 'POST',
      body: JSON.stringify({ hostingProcess }),
    }),

  ping: (sessionId: string) =>
    request<{ nonce: number }>(`/sessions/${sessionId}/ping`, { method: 'POST' }),

  deleteImplantBuild: (name: string) =>
    request<{ success: boolean }>(`/implant-builds/${name}`, { method: 'DELETE' }),

  regenerate: (implantName: string) =>
    request<GenerateResult>('/regenerate', {
      method: 'POST',
      body: JSON.stringify({ implantName }),
    }),

  getOperators: () => request<{ operators: { name: string; online: boolean }[] }>('/operators'),

  compiler: () => request<CompilerInfo>('/compiler'),

  hosts: () => request<{ hosts: Host[] }>('/hosts'),

  host: (uuid: string) => request<Host>(`/hosts/${encodeURIComponent(uuid)}`),

  hostRemove: (uuid: string) => request<{ success: boolean }>(`/hosts/${encodeURIComponent(uuid)}`, { method: 'DELETE' }),

  hostIOCRm: (uuid: string, iocId: string) =>
    request<{ success: boolean }>(`/hosts/${encodeURIComponent(uuid)}/iocs/${encodeURIComponent(iocId)}`, {
      method: 'DELETE',
    }),

  // --- Websites ---
  websites: () => request<{ websites: Website[] }>('/websites'),

  website: (name: string) => request<Website>(`/websites/${encodeURIComponent(name)}`),

  websiteAddContent: (name: string, body: { path: string; content_type?: string; file_data_b64?: string; text?: string }) =>
    request<Website>(`/websites/${encodeURIComponent(name)}/content`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  websiteUpdateContent: (name: string, body: { path: string; content_type?: string; file_data_b64?: string; text?: string }) =>
    request<Website>(`/websites/${encodeURIComponent(name)}/content`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),

  websiteRemoveContent: (name: string, paths: string[]) =>
    request<Website>(`/websites/${encodeURIComponent(name)}/content`, {
      method: 'DELETE',
      body: JSON.stringify({ paths }),
    }),

  websiteRemove: (name: string) =>
    request<{ success: boolean }>(`/websites/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // --- WireGuard ---
  wgClientConfig: () => request<WGClientConfig>('/wg/config'),

  wgUniqueIP: () => request<{ ip: string }>('/wg/ip'),

  wgForwarders: (sessionId: string) =>
    request<{ forwarders: WGTCPForwarder[] }>(`/sessions/${encodeURIComponent(sessionId)}/wg/forwarders`),

  wgStartPortForward: (sessionId: string, localPort: number, remoteAddress: string) =>
    request<{ forwarder: WGTCPForwarder; async: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/wg/forwarders`, {
      method: 'POST',
      body: JSON.stringify({ local_port: localPort, remote_address: remoteAddress }),
    }),

  wgStopPortForward: (sessionId: string, id: number) =>
    request<{ forwarder: WGTCPForwarder; async: boolean }>(
      `/sessions/${encodeURIComponent(sessionId)}/wg/forwarders/${id}`,
      { method: 'DELETE' },
    ),

  wgSocksServers: (sessionId: string) =>
    request<{ servers: WGSocksServer[] }>(`/sessions/${encodeURIComponent(sessionId)}/wg/socks`),

  wgStartSocks: (sessionId: string, port: number) =>
    request<{ server: WGSocksServer; async: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/wg/socks`, {
      method: 'POST',
      body: JSON.stringify({ port }),
    }),

  wgStopSocks: (sessionId: string, id: number) =>
    request<{ server: WGSocksServer; async: boolean }>(
      `/sessions/${encodeURIComponent(sessionId)}/wg/socks/${id}`,
      { method: 'DELETE' },
    ),

  // --- Privilege escalation ---
  getPrivs: (sessionId: string) =>
    request<{ privileges: WindowsPrivilege[] }>(`/sessions/${encodeURIComponent(sessionId)}/privs`),

  currentTokenOwner: (sessionId: string) =>
    request<{ owner: string }>(`/sessions/${encodeURIComponent(sessionId)}/token-owner`),

  executeToken: (sessionId: string, path: string, args: string[], output: boolean) =>
    request<ExecResult>(`/sessions/${encodeURIComponent(sessionId)}/execute-token`, {
      method: 'POST',
      body: JSON.stringify({ path, args, output }),
    }),

  runAs: (sessionId: string, username: string, processName: string, args: string) =>
    request<{ output: string; async: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/runas`, {
      method: 'POST',
      body: JSON.stringify({ username, process_name: processName, args }),
    }),

  // --- Pivots ---
  pivotGraph: () => request<{ Children: PivotGraphEntry[] }>(`/pivots/graph`),

  pivotListeners: (sessionId: string) =>
    request<{ listeners: PivotListener[] }>(`/sessions/${encodeURIComponent(sessionId)}/pivots/listeners`),

  pivotStartListener: (sessionId: string, type: string, bindAddress: string) =>
    request<PivotListener>(`/sessions/${encodeURIComponent(sessionId)}/pivots/listeners`, {
      method: 'POST',
      body: JSON.stringify({ type, bind_address: bindAddress }),
    }),

  pivotStopListener: (sessionId: string, id: number) =>
    request<{ ok: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/pivots/listeners/${id}`, {
      method: 'DELETE',
    }),

  // --- Windows services ---
  startService: (sessionId: string, opts: { service_name: string; description?: string; bin_path?: string; hostname?: string; arguments?: string }) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/services`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  stopService: (sessionId: string, serviceName: string, hostname?: string) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/services/stop`, {
      method: 'POST',
      body: JSON.stringify({ service_name: serviceName, hostname: hostname || '' }),
    }),

  removeService: (sessionId: string, serviceName: string, hostname?: string) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/services/remove`, {
      method: 'POST',
      body: JSON.stringify({ service_name: serviceName, hostname: hostname || '' }),
    }),

  // --- SSH ---
  runSSHCommand: (sessionId: string, opts: { username: string; hostname: string; port?: number; command: string; password?: string; priv_key?: string }) =>
    request<SSHCommandResult>(`/sessions/${encodeURIComponent(sessionId)}/ssh`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  // --- Extensions ---
  listExtensions: (sessionId: string) =>
    request<{ names: string[] }>(`/sessions/${encodeURIComponent(sessionId)}/extensions`),

  registerExtension: (sessionId: string, opts: { name: string; os: string; init: string; data_b64: string }) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/extensions/register`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  callExtension: (sessionId: string, opts: { name: string; export: string; server_store?: boolean; args_b64?: string }) =>
    request<CallExtensionResult>(`/sessions/${encodeURIComponent(sessionId)}/extensions/call`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  // --- Metasploit ---
  msf: (sessionId: string, opts: { payload: string; lhost: string; lport: number; encoder?: string; iterations?: number }) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/msf`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  msfRemote: (sessionId: string, opts: { payload: string; lhost: string; lport: number; encoder?: string; iterations?: number; pid: number }) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/msf/remote`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  msfStage: (opts: { arch: string; format: string; port: number; host: string; os: string; protocol: string; bad_chars: string[] }) =>
    request<MsfStager>(`/msf/stage`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  // --- Implant operations ---
  backdoor: (sessionId: string, opts: { file_path: string; profile_name: string }) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/backdoor`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  hijackDll: (sessionId: string, opts: { reference_dll_path: string; target_location: string; reference_dll_b64: string; target_dll_b64: string; profile_name: string }) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/dll-hijack`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  shellcodeRdi: (opts: { data_b64: string; function_name: string; arguments: string }) =>
    request<{ DataB64: string; Size: number }>(`/shellcode/rdi`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  execShellcode: (sessionId: string, opts: { data_b64: string; pid: number; rwx_pages: boolean }) =>
    request<{ success: boolean }>(`/sessions/${encodeURIComponent(sessionId)}/exec-shellcode`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  psexec: (sessionId: string, opts: { hostname: string; profile_name: string; service_name: string; service_desc: string; bin_path: string }) =>
    request<{ success: boolean; message: string; path: string; service: string; host: string }>(`/sessions/${encodeURIComponent(sessionId)}/psexec`, {
      method: 'POST',
      body: JSON.stringify(opts),
    }),

  // --- DNS canaries ---
  canaries: () => request<{ canaries: Canary[] }>('/canaries'),

  // --- Prune ---
  pruneBeacons: (days: number) =>
    request<{ success: boolean; pruned: number }>('/beacons/prune', {
      method: 'POST',
      body: JSON.stringify({ days }),
    }),

  pruneSessions: () =>
    request<{ success: boolean; pruned: number }>('/sessions/prune', { method: 'POST' }),

  // --- Aliases ---
  aliases: () => request<{ aliases: Alias[] }>('/aliases'),

  aliasInstall: (bundleB64: string) =>
    request<{ success: boolean; alias: Alias }>('/aliases', {
      method: 'POST',
      body: JSON.stringify({ bundle_b64: bundleB64 }),
    }),

  aliasRemove: (name: string) =>
    request<{ success: boolean }>(`/aliases/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  aliasRun: (sessionId: string, name: string, opts: { args?: string; process?: string; arch?: string; method?: string; class?: string }) =>
    request<{ success: boolean; output: string; mode: string; command: string; args: string; process: string; platform: string }>(
      `/sessions/${encodeURIComponent(sessionId)}/aliases/${encodeURIComponent(name)}/run`,
      { method: 'POST', body: JSON.stringify(opts) },
    ),
	regCreateKey: (sessionId: string, hive: string, path: string, key: string) =>
		request<{ success: boolean }>(`/sessions/${sessionId}/reg/create-key`, {
			method: 'POST',
			body: JSON.stringify({ hive, path, key }),
		}),

	/** Enumerate the full SliverRPC surface. */
	rpcMethods: () => request<{ methods: RpcMethod[]; count: number }>('/rpc/methods'),

	/** Invoke any SliverRPC method by name with a protojson request body. */
	rpcCall: (method: string, req: unknown, timeout?: number) =>
		request<RpcCallResult>('/rpc/call', {
			method: 'POST',
			body: JSON.stringify({ method, request: req ?? {}, timeout }),
		}),

	// --- Post-exploitation surface ---

	/** Everything in the server-side credential vault. */
	creds: () => request<{ credentials: VaultCredential[] }>('/creds'),

	/** Store a credential. The server accepts either the flat single-entry form
	 *  used by the manual-entry panel or a { credentials: [...] } batch. */
	credsAdd: (cred: Record<string, unknown> | { credentials: Partial<VaultCredential>[] }) =>
		request<{ ok: boolean; added: number }>('/creds', {
			method: 'POST',
			body: JSON.stringify(cred as Record<string, unknown>),
		}),

	credsUpdate: (credentials: Partial<VaultCredential>[]) =>
		request<{ ok: boolean }>('/creds', {
			method: 'PUT',
			body: JSON.stringify({ credentials }),
		}),

	credsRemove: (ids: string[]) =>
		request<{ ok: boolean }>('/creds', {
			method: 'DELETE',
			body: JSON.stringify({ ids }),
		}),

	/** Filter the vault; plaintext=1 narrows to already-recovered secrets. */
	credsByHashType: (hashType: number, plaintextOnly = false) =>
		request<{ credentials: VaultCredential[] }>(
			`/creds/filter?type=${hashType}${plaintextOnly ? '&plaintext=1' : ''}`,
		),

	/** Ask the server to identify an unknown hash's type. */
	credsSniff: (hash: string) =>
		request<VaultCredential>('/creds/sniff', { method: 'POST', body: JSON.stringify({ hash }) }),

	cred: (id: string) => request<VaultCredential>(`/creds/${encodeURIComponent(id)}`),

	// --- Memfiles: in-memory files on the target (no disk artefact) ---

	memfiles: (sessionId: string) =>
		request<DirView>(`/sessions/${sessionId}/memfiles`),

	memfilesAdd: (sessionId: string) =>
		request<{ fd: number }>(`/sessions/${sessionId}/memfiles`, { method: 'POST' }),

	memfilesRemove: (sessionId: string, fd: number) =>
		request<{ ok: boolean }>(`/sessions/${sessionId}/memfiles`, {
			method: 'DELETE',
			body: JSON.stringify({ fd }),
		}),

	// --- File attributes and content search ---

	chmod: (sessionId: string, path: string, mode: string, recursive = false) =>
		request<{ ok: boolean }>(`/sessions/${sessionId}/fs/chmod`, {
			method: 'POST',
			body: JSON.stringify({ path, mode, recursive }),
		}),

	chown: (sessionId: string, path: string, uid: string, gid: string, recursive = false) =>
		request<{ ok: boolean }>(`/sessions/${sessionId}/fs/chown`, {
			method: 'POST',
			body: JSON.stringify({ path, uid, gid, recursive }),
		}),

	/** Rewrite timestamps (timestomping). */
	chtimes: (sessionId: string, path: string, atime: number, mtime: number) =>
		request<{ ok: boolean }>(`/sessions/${sessionId}/fs/chtimes`, {
			method: 'POST',
			body: JSON.stringify({ path, atime, mtime }),
		}),

	/** Search file contents across the target 鈥?the find-secrets-on-disk primitive. */
	grep: (
		sessionId: string,
		pattern: string,
		path: string,
		recursive = true,
		before = 0,
		after = 0,
	) =>
		request<GrepOut>(`/sessions/${sessionId}/fs/grep`, {
			method: 'POST',
			body: JSON.stringify({ pattern, path, recursive, before, after }),
		}),

	// --- Keylogger telemetry sinks ---

	monitorProviders: () =>
		request<{ providers: MonitorProvider[] }>('/monitor/providers'),

	monitorAddProvider: (p: Partial<MonitorProvider>) =>
		request<{ ok: boolean }>('/monitor/providers', {
			method: 'POST',
			body: JSON.stringify(p),
		}),

	monitorRemoveProvider: (p: Partial<MonitorProvider>) =>
		request<{ ok: boolean }>('/monitor/providers', {
			method: 'DELETE',
			body: JSON.stringify(p),
		}),

	// --- C2 profiles ---

	c2Profiles: () => request<{ profiles: C2Profile[] }>('/c2profiles'),

	c2Profile: (name: string) =>
		request<Record<string, unknown>>(`/c2profiles/${encodeURIComponent(name)}`),

	saveC2Profile: (profile: unknown, overwrite = false) =>
		request<{ ok: boolean }>('/c2profiles', {
			method: 'POST',
			body: JSON.stringify({ profile, overwrite }),
		}),

	// --- Encoders ---

	trafficEncoders: () => request<{ encoders: string[] }>('/traffic-encoders'),

	/** Upload a WASM encoder and run its conformance suite. */
	trafficEncoderAdd: (name: string, wasmB64: string, skipTests = false) =>
		request<TrafficEncoderReport>('/traffic-encoders', {
			method: 'POST',
			body: JSON.stringify({ name, wasm: wasmB64, skipTests }),
		}),

	trafficEncoderRemove: (name: string) =>
		request<{ ok: boolean }>(`/traffic-encoders/${encodeURIComponent(name)}`, {
			method: 'DELETE',
		}),

	shellcodeEncoders: () =>
		request<{ encoders: ShellcodeEncoder[] }>('/shellcode-encoders'),

	shellcodeEncode: (encoder: string, arch: string, dataB64: string, iterations = 1, badChars = '') =>
		request<{ data: string; length: number }>('/shellcode-encoders', {
			method: 'POST',
			body: JSON.stringify({ encoder, arch, data: dataB64, iterations, badChars }),
		}),

	// --- WASM extensions ---

	wasmExtensions: (sessionId: string) =>
		request<{ extensions: string[] }>(`/sessions/${sessionId}/wasm`),

	wasmRegister: (sessionId: string, name: string, wasmB64: string) =>
		request<{ ok: boolean }>(`/sessions/${sessionId}/wasm/register`, {
			method: 'POST',
			body: JSON.stringify({ name, wasm: wasmB64 }),
		}),

	wasmExec: (sessionId: string, name: string, args: string[]) =>
		request<{ stdout: string; stderr: string; exitCode: number }>(`/sessions/${sessionId}/wasm/exec`, {
			method: 'POST',
			body: JSON.stringify({ name, args }),
		}),

	// --- Reverse port forwards ---

	rportfwd: (sessionId: string) =>
		request<{ listeners: RportFwdListener[] }>(`/sessions/${sessionId}/rportfwd`),

	rportfwdStart: (
		sessionId: string,
		bindAddress: string,
		bindPort: number,
		forwardAddress: string,
		forwardPort: number,
	) =>
		request<RportFwdListener>(`/sessions/${sessionId}/rportfwd`, {
			method: 'POST',
			body: JSON.stringify({ bindAddress, bindPort, forwardAddress, forwardPort }),
		}),

	rportfwdStop: (sessionId: string, fwdID: number) =>
		request<{ ok: boolean }>(`/sessions/${sessionId}/rportfwd/${fwdID}`, { method: 'DELETE' }),

	// --- Certificates ---

	caCertificates: () =>
		request<{ certificates: CertificateInfo[] }>('/certificates/ca'),

	certificates: (category = 0, cn = '') =>
		request<{ certificates: CertificateInfo[] }>(
			`/certificates?category=${category}&cn=${encodeURIComponent(cn)}`,
		),

	// --- Tunnels ---

	tunnelCreate: (sessionId: string) =>
		request<{ tunnelID: number }>(`/sessions/${sessionId}/tunnel`, { method: 'POST' }),

	tunnelClose: (tunnelID: number, sessionId: string) =>
		request<{ ok: boolean }>('/tunnels', {
			method: 'DELETE',
			body: JSON.stringify({ tunnelID, sessionID: sessionId }),
		}),

	// --- Windows services ---

	serviceDetail: (sessionId: string, name: string, hostname = '') =>
		request<ServiceDetail>(`/sessions/${sessionId}/services/detail`, {
			method: 'POST',
			body: JSON.stringify({ name, hostname }),
		}),

	serviceStartByName: (sessionId: string, name: string, hostname = '') =>
		request<{ ok: boolean }>(`/sessions/${sessionId}/services/start-by-name`, {
			method: 'POST',
			body: JSON.stringify({ name, hostname }),
		}),

	// --- Registry hive extraction ---

	hiveUrl: (sessionId: string) => `/api/sessions/${sessionId}/reg/hive`,

	// --- Persistence ---

	/** Catalog of the persistence mechanisms the server can install. */
	persistenceModules: () => request<{ modules: PersistenceModule[] }>('/persistence/modules'),

	/**
	 * The inventory of what is already on the host.
	 *
	 * `name` is optional and only matters for name-scoped mechanisms: a
	 * scheduled task, a service or a local account can only be looked up by
	 * name, so without one those rows come back marked unknown rather than
	 * claiming the host is clean.
	 */
	persistenceList: (sessionId: string, name?: string) =>
		request<PersistenceList>(
			`/sessions/${encodeURIComponent(sessionId)}/persistence` +
				(name ? `?name=${encodeURIComponent(name)}` : ''),
		),

	persistenceInstall: (sessionId: string, module: string, payload: string, name: string) =>
		request<PersistenceResult>(`/sessions/${encodeURIComponent(sessionId)}/persistence/install`, {
			method: 'POST',
			body: JSON.stringify({ module, payload, name }),
		}),

	persistenceRemove: (sessionId: string, module: string, name: string) =>
		request<PersistenceResult>(`/sessions/${encodeURIComponent(sessionId)}/persistence/remove`, {
			method: 'POST',
			body: JSON.stringify({ module, name }),
		}),

	// --- Credential harvesting ---

	/**
	 * Run a mimikatz command inside the session. These runs can take minutes.
	 *
	 * `elevate` escalates to a SYSTEM session first when the token is not already
	 * elevated, which is what makes the LSA-reading modules work at all.
	 */
	mimikatz: (
		sessionId: string,
		command: string,
		autoAdd: boolean,
		elevate?: boolean,
		hostingProcess?: string,
		mode?: MimikatzMode,
		process?: string,
	) =>
		request<MimikatzResult>(`/sessions/${encodeURIComponent(sessionId)}/mimikatz`, {
			method: 'POST',
			// No path: the console carries its own mimikatz. `mode` decides whether it
			// is written to the target's temp directory or injected into a host
			// process, and `process` names that host when the operator picked one.
			body: JSON.stringify({ command, autoAdd, elevate, hostingProcess, mode, process }),
		}),

	/** Parse output captured elsewhere; autoAdd stores what it recovers. */
	mimikatzParse: (text: string, autoAdd: boolean) =>
		request<MimikatzResult>('/mimikatz/parse', {
			method: 'POST',
			body: JSON.stringify({ text, autoAdd }),
		}),

	// --- Network topology ---

	/**
	 * Aggregate node/edge view: sessions, beacons, pivot hops and the
	 * console-side SOCKS/port-forward links flattened into one graph. Unlike
	 * pivotGraph this is assembled by the console, not by Sliver.
	 */
	topology: () => request<TopologyGraph>('/topology'),

	// --- WebDelivery ---

	/** The fetch-and-run templates the backend renders a one-liner for. */
	webDeliveryFormats: () =>
		request<{ formats: WebDeliveryFormatInfo[] }>('/webdelivery/formats'),

	/**
	 * Build a stage, publish it, and return the one-liner. Has side effects:
	 * builds an implant if the profile has none, publishes content, and starts a
	 * listener.
	 */
	webDelivery: (req: WebDeliveryRequest) =>
		request<WebDeliveryResult>('/webdelivery', {
			method: 'POST',
			body: JSON.stringify(req),
		}),
	// --- Console authentication ---

	authGet: () => request<AuthSettings>('/settings/auth'),

	authPut: (username: string, password: string, currentPassword: string) =>
		request<{ ok: boolean; message: string }>('/settings/auth', {
			method: 'PUT',
			body: JSON.stringify({ username, password, currentPassword }),
		}),
}

export function wsUrl(path: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${window.location.host}${path}`
}
