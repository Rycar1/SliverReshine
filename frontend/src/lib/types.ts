export interface Session {
  ID: string
  Name: string
  UUID: string
  Hostname: string
  Username: string
  UID: string
  GID: string
  PID: number
  OS: string
  Arch: string
  Transport: string
  RemoteAddress: string
  LastCheckin: string
  ActiveC2: string
  Locale: string
  AgentVersion: string
  IsDead: boolean
  IsInteractive: boolean
}

export interface Beacon {
  ID: string
  Name: string
  Hostname: string
  Username: string
  OS: string
  Arch: string
  Transport: string
  RemoteAddress: string
  LastCheckin: string
  NextCheckin: string
  Interval: number
  Jitter: number
  ActiveC2: string
}

export interface Listener {
  ID: number
  Name: string
  JobID: number
  Protocol: string
  Addr: string
  TLS: boolean
}

export interface Event {
  Type: string
  Err: string
  Session?: Session
  Beacon?: Beacon
  Job?: Job
  Data: Record<string, unknown>
}

export interface ServerInfo {
  version: string
  connected: boolean
  error?: string
  /**
   * Set when the console has a connection to the server but could not read a
   * fact about it. Without this the two cases were indistinguishable: both
   * arrived as connected:false with a version, and the UI reported "not
   * connected" for a server that was answering.
   */
  degraded?: boolean
}

/** One callable method on the SliverRPC surface. */
export interface RpcMethod {
  name: string
  streaming: boolean
  inputType: string
  outputType: string
  group: string
}

/** Result of a raw RPC invocation. Unary calls fill `result`; streaming calls
 *  fill `messages` and report whether the drain window was exhausted. */
export interface RpcCallResult {
  ok: boolean
  result?: unknown
  messages?: unknown[]
  count?: number
  truncated?: boolean
}

export interface OverviewCounts {
  sessions: number
  beacons: number
  jobs: number
  builders: number
  socks: number
}

export interface OverviewData {
  counts: OverviewCounts
}

export interface ImplantConfig {
  name: string
  os: string
  arch: string
  format: string
  target: string
  c2: { address: string; protocol: string }[]
  mtls: boolean
  http: boolean
  dns: boolean
  wireguard: boolean
  interval: number
  jitter: number
  maxConnectionErrors: number
  debug: boolean
  evasion: boolean
  obfuscate: boolean
  limitDomainJoined: boolean
  transport: string
}

export interface ImplantBuild {
  Name: string
  ImplantConfig: ImplantConfigView
  ImplantBuildID: string
  Arch: string
  OS: string
}

export interface Job {
  ID: number
  Name: string
  Protocol: string
  Port: number
  Domains: string[]
  JobControl: string
  // The server's human-readable line for the job. A forward (bind) listener
  // puts its target in here, because it has no local port to identify it.
  Description?: string
  // CanStage is the server's verdict on whether this listener can host a
  // stage. Only the HTTP family can; the flag is authoritative.
  CanStage?: boolean
}

// BindListener is a forward listener: the server dials a port the implant is
// listening on instead of waiting for the implant to call home. The session it
// produces is the same mutual-TLS session a reverse listener produces, with the
// TLS roles swapped — so it is encrypted and mutually authenticated the same
// way, and "no sessions yet" means the dial has not landed rather than that the
// implant is silent.
export interface BindListener {
  jobId: number
  address: string
  host: string
  port: number
  direction: 'forward'
}

export interface LootEntry {
  ID: string
  Name: string
  LootType: string
  FileType: string
  File: string
  Size: number
  DataB64?: string
  CredUser?: string
  CredPassword?: string
  CredAPIKey?: string
}

export interface CompilerTarget {
  GOOS: string
  GOARCH: string
  Format: string
}

export interface CrossCompiler {
  TargetGOOS: string
  TargetGOARCH: string
  CCPath: string
  CXXPath: string
}

export interface CompilerInfo {
  GOOS: string
  GOARCH: string
  Targets: CompilerTarget[]
  CrossCompilers: CrossCompiler[]
}

export interface IOC {
  ID: string
  Path: string
  FileHash: string
}

export interface Host {
  Hostname: string
  HostUUID: string
  OSVersion: string
  IOCs: IOC[]
}

export interface WebContent {
  Path: string
  ContentType: string
  Size: number
  DataB64?: string
}

export interface Website {
  Name: string
  Contents: Record<string, WebContent>
  Size: number
}

export interface WGClientConfig {
  ServerPubKey: string
  ClientPrivateKey: string
  ClientPubKey: string
  ClientIP: string
}

export interface WGTCPForwarder {
  ID: number
  LocalAddr: string
  RemoteAddr: string
}

export interface WGSocksServer {
  ID: number
  LocalAddr: string
}

export interface Operator {
  Name: string
  FirstContact: string
  Sessions: number
  CanCleanup: boolean
}

export interface Canary {
  ImplantName: string
  Domain: string
  Triggered: boolean
  FirstTriggered?: string
  LatestTrigger?: string
  Count: number
}

export interface Credential {
  ID: number
  Username: string
  Plaintext: string
  Hash: string
  HashType: string
  Realm: string
  Collection: string
}

export interface Task {
  ID: number
  CreatedAt: string
  CompletedAt: string
  Description: string
  State: string
  Type: string
  SessionID: string
}

export interface GenerateResult {
  success: boolean
  message: string
  path?: string
  name?: string
  data?: string
  // Set by the server when a build of this name already existed and was
  // removed to make room for this one. Without it a rebuild is
  // indistinguishable from a fresh build, which is how a replacement -- and,
  // when the build failed, the loss of the build it replaced -- went
  // unnoticed.
  replaced?: boolean
}

export interface FileInfo {
  name: string
  path: string
  size: number
  isDir: boolean
}

export interface FileEntry {
  Name: string
  IsDir: boolean
  Size: number
  ModTime: number
  Mode: string
}

export interface DirView {
  Path: string
  Exists: boolean
  Files: FileEntry[]
}

export interface NetInterface {
  Index: number
  Name: string
  MAC: string
  IPAddresses: string[]
}

export interface ProcessInfo {
  PID: number
  PPID: number
  Executable: string
  Owner: string
  SessionID: number
  CmdLine: string[]
}

// --- Process identification (AV / EDR fingerprinting) ---

// One process the lookup service recognised, merged with the local process
// table so the console can show owner and command line alongside the verdict.
export interface AVProcess {
  pid: string
  key: string
  // The service's label, with its <font> markup already stripped server-side.
  value: string
  // 'security' | 'software' | 'system'
  category: string
  identified: boolean
  process?: ProcessInfo
}

export interface AVScanResult {
  success: boolean
  database: string
  timestamp: string
  stats: {
    // Number of processes sent for identification.
    sent: number
    // Number the service reports as recognised (may exceed `matches` when the
    // same executable appears at several pids).
    total: number
    identified: number
  }
  matches: AVProcess[]
  groups: Record<string, AVProcess[]>
  // Executables the service had no entry for. Custom tooling and implants
  // show up here, which makes it a useful differ against a baseline.
  unmatched: string[]
}

export interface SockEntry {
  Protocol: string
  LocalAddr: string
  LocalPort: number
  RemoteAddr: string
  RemotePort: number
  State: string
  UID: number
  ProcessName: string
}

export interface EnvVar {
  Key: string
  Value: string
}

export interface ExecResult {
  Status: number
  Stdout: string
  Stderr: string
  PID: number
}

export interface WindowsPrivilege {
  Name: string
  Description: string
  Enabled: boolean
  EnabledByDefault: boolean
  Removed: boolean
  UsedForAccess: boolean
}

export interface NetConnPivot {
  PeerID: number
  RemoteAddress: string
}

export interface PivotListener {
  ID: number
  Type: string
  BindAddress: string
  Pivots: NetConnPivot[]
}

export interface PivotGraphEntry {
  PeerID: number
  Name: string
  SessionID: string
  Hostname: string
  Username: string
  OS: string
  Transport: string
  RemoteAddress: string
  Children: PivotGraphEntry[]
}

export interface SSHCommandResult {
  StdOut: string
  StdErr: string
}

export interface CallExtensionResult {
  Output: string
  ServerStore: boolean
}

export interface MsfStager {
  FileName: string
  DataB64: string
  Size: number
}

export interface PortForward {
  LocalAddr: string
  LocalPort: number
  Host: string
  Port: number
  SessionID: string
  /**
   * The most recent per-connection failure, absent when there has not been one.
   *
   * A forward can be listed while every connection through it fails. Without
   * this field the table showed a healthy row and the only evidence of the
   * problem was a browser tab that never loaded.
   */
  LastConnErr?: string
}

export interface BeaconTask {
  ID: string
  BeaconID: string
  CreatedAt: number
  State: string
  SentAt: number
  CompletedAt: number
  Description: string
  ResponseB64?: string
}

export interface SocksProxy {
  ID: number
  SessionID: string
  BindAddr: string
  BindPort: number
  Username: string
  Password: string
}

export interface ImplantProfile {
  Name: string
  Config?: ImplantConfigView
}

export interface ImplantConfigView {
  Name: string
  OS: string
  Arch: string
  Format: string
  Interval: number
  Jitter: number
  Obfuscate: boolean
  Debug: boolean
  Evasion: boolean
  MaxConnectionErrors: number
  IsBeacon: boolean
  BeaconInterval: number
  BeaconJitter: number
  C2: { URL: string }[]
}

export interface Alias {
  Name: string
  Version: string
  CommandName: string
  OriginalAuthor: string
  RepoURL: string
  Help: string
  Entrypoint: string
  AllowArgs: boolean
  DefaultArgs: string
  Platforms: string[]
  IsAssembly: boolean
  IsReflective: boolean
}

// --- Post-exploitation additions ---

/** A credential in the server-side vault. Hashes and plaintexts harvested
 *  across every session land here and survive session loss. */
export interface VaultCredential {
  ID: string
  Username: string
  Plaintext: string
  Hash: string
  HashType: number
  IsCracked: boolean
  OriginHostUUID: string
  Collection: string
}

/** One content match inside a file. */
export interface GrepMatch {
  LineNumber: number
  Line: string
  LinesBefore?: string[]
  LinesAfter?: string[]
}

/** Grep results for a single file. */
export interface GrepFileResult {
  Path: string
  IsBinary: boolean
  Matches: GrepMatch[]
}

export interface GrepOut {
  SearchPath: string
  Results: GrepFileResult[]
}

/** A keylogger telemetry sink. */
export interface MonitorProvider {
  ID: string
  Type: string
  APIKey: string
  APIPassword: string
}

/** A stored HTTP C2 profile, summarised for the list view. */
export interface C2Profile {
  ID: string
  Name: string
  Created: number
  ServerURI?: string[]
  UserAgent?: string
}

/** An available shellcode encoder chain for one architecture. */
export interface ShellcodeEncoder {
  Arch: string
  Encoders: string[]
}

/** Result of running an encoder's conformance suite. */
export interface TrafficEncoderTest {
  Name: string
  Completed: boolean
  Success: boolean
  Duration: number
  Err?: string
}

export interface TrafficEncoderReport {
  EncoderID: number
  TotalTests: number
  TotalDuration: number
  Tests: TrafficEncoderTest[]
}

/** A certificate descriptor as reported by the server. */
export interface CertificateInfo {
  CN: string
  Expiry: string
  KeyType: string
  IsCA: boolean
  Certificate: string
}

/** A reverse port forward listener bound on the server. */
export interface RportFwdListener {
  ID: number
  BindAddress: string
  BindPort: number
  ForwardAddress: string
  ForwardPort: number
}

/** Full configuration of a Windows service. */
export interface ServiceDetail {
  Name: string
  DisplayName: string
  Description: string
  Status: number
  StartupType: number
  BinPath: string
  Account: string
}

/**
 * A persistence mechanism the server knows how to install. `technique` is the
 * MITRE ATT&CK id (for example T1547.001) shown as a badge in the catalog.
 */
export interface PersistenceModule {
  id: string
  name: string
  technique: string
  platforms: string[]
  requiresAdmin: boolean
  description: string
  /**
   * Optional i18n keys naming what the two install fields mean for this module.
   * Most modules install a file, so the defaults (a path and an artifact id) are
   * right; an account-creation module instead takes a password and a username,
   * and labelling those as a path and an id invites them to be filled in wrong.
   */
  payloadLabel?: string
  nameLabel?: string
  /**
   * Chinese catalog text. Absent for a module the backend has no translation
   * for, in which case `name` and `description` are used unchanged — so the
   * fallback is the identity, never a blank card.
   */
  nameZh?: string
  descriptionZh?: string
}

/** One persistence mechanism as it exists on the target right now. */
export interface PersistenceItem {
  module: string
  name: string
  location: string
  installed: boolean
  detail: string
  removable: boolean
  /**
   * True when the unnamed inventory pass could not answer for this module. A
   * name-scoped artifact (a task, a service, an account) can only be looked up
   * by name, and the inventory has none. Rendering this as "absent" would tell
   * the operator a host is clean right after they installed something.
   */
  unknown?: boolean
}

export interface PersistenceList {
  platform: string
  items: PersistenceItem[]
}

export interface PersistenceResult {
  ok: boolean
  message: string
  module: string
  location: string
}

/** One credential recovered from a mimikatz-style dump. */
export interface ParsedCredential {
  username: string
  domain: string
  secret: string
  kind: string
  source: string
}

export interface MimikatzResult {
  ok: boolean
  command: string
  raw: string
  exitCode: number
  parsed: ParsedCredential[]
  added: number
  message: string
  /**
   * The run was escalated to a SYSTEM session first. Absent on the parse path,
   * which never touches a target.
   */
  elevated?: boolean
  /** The session the command actually ran on; differs when escalated. */
  sessionId?: string
  /** Token integrity before the run: Untrusted | Low | Medium | High. */
  integrity?: string
  /**
   * How the payload reached the target: "upload" writes the embedded binary to
   * the target's temp directory and executes it; "memory" injects it into a
   * host process and never writes a file. The backend picks one when the caller
   * leaves it out. Absent on the parse path, which never touches a target.
   */
  mode?: MimikatzMode
  /** The path written on the target. Empty on an in-memory run. */
  targetPath?: string
  /**
   * Human-readable account of what the run did before the payload started —
   * which host process was used, or why no file was written. Shown next to the
   * credentials so a run is auditable without reading the raw output.
   */
  execution?: string
}

/**
 * How the credential payload is executed on the target.
 *
 * "memory" needs a host process on the target and is refused when the session
 * has none, rather than silently falling back to a disk write: an operator who
 * asked for no file to be written must not get one.
 */
export type MimikatzMode = 'auto' | 'memory' | 'upload'
/** Console credentials - the same pair the browser login prompt asks for. */
export interface AuthSettings {
  username: string
  enabled: boolean
  source: string
}

/** One vertex in the aggregate network view. */
export interface TopologyNode {
  ID: string
  /** c2 | session | beacon */
  Kind: string
  Label: string
  Hostname: string
  Username: string
  OS: string
  Arch: string
  Address: string
  Transport: string
  /** Pivot hops from the console; drives the column layout. */
  Depth: number
  Dead: boolean
}

/** One link between two vertices, labelled with what carries it. */
export interface TopologyEdge {
  From: string
  To: string
  /** transport | pivot | socks | portfwd */
  Kind: string
  Label: string
}

/** One fetch-and-run template the backend can render a one-liner for. */
/** One listener that can serve a stage, with whether it is eligible. */
export interface OneLinerTarget {
  job_id: number
  name: string
  port: number
  domains: string[]
  can_stage: boolean
  /**
   * The address a command built for this listener dials when the host field is
   * left blank, or "" when none is available. The panel shows it instead of an
   * empty field, which is what invited a wildcard to be typed in.
   */
  callback_host?: string
}

/** One command template for a stage that has already been published. */
export interface OneLinerAlternative {
  delivery: string
  label: string
  /** The platform the template is written for; "" when it works on several. */
  platform: string
  command: string
}

/** What the operator asked for. */
export interface OneLinerRequest {
  job_id: number
  platform: string
  /** The address the target can reach. Empty falls back to the listener's own. */
  host?: string
  name?: string
  obfuscate?: boolean
  evasion?: boolean
  delivery?: string
  /**
   * Rebuild even when an identical stage was already built. Omitted means the
   * backend answers from the stage it built last time instead of building
   * another pair of implants.
   */
  force?: boolean
}

/** The command that gets a session, plus what it points at. */
export interface OneLinerResult {
  command: string
  url: string
  platform: string
  delivery: string
  c2_url: string
  job_id: number
  staged_as: string
  warning: string
  /** True when the command came from an earlier build rather than a new one. */
  reused?: boolean
  alternatives: OneLinerAlternative[]
}

/**
 * One platform's result from a multi-platform build.
 *
 * Unlike OneLinerResult this carries a per-platform error, because building for
 * several platforms is not all-or-nothing: a Windows build failing says nothing
 * about the Linux one, and the operator should keep whichever succeeded.
 */
export interface MultiOneLinerResult {
  command: string
  platform: string
  url: string
  delivery: string
  staged_as: string
  /** Where the stage was published; differs per platform by design. */
  path: string
  /**
   * The address the implant dials. Shown in the dialog because a command that
   * calls back somewhere unreachable looks identical to one that works until it
   * is run on the target.
   */
  c2_url: string
  alternatives: OneLinerAlternative[]
  /** True when this platform's stage already existed and was not rebuilt. */
  reused?: boolean
  /** Set when this platform could not be built. Empty on success. */
  error?: string
}
export interface WebDeliveryFormatInfo {
  id: string
  platform: string
  label: string
}

/** Parameters for one delivery. */
export interface WebDeliveryRequest {
  profile_name: string
  host: string
  port: number
  path?: string
  format: string
  website?: string
}

export interface WebDeliveryResult {
  /** The one-liner to run on the target. */
  command: string
  /** What the command fetches, so reachability can be tested first. */
  url: string
  /** Listener job id, or 0 when an existing listener already covers the port. */
  job_id: number
  /** Non-fatal note, e.g. that no listener was started. */
  warning: string
  /** Website the stage was actually published on (the console may rewrite it). */
  website?: string
}

export interface TopologyGraph {
  nodes: TopologyNode[]
  edges: TopologyEdge[]
}

// --- AI assistant ---

/**
 * One command the AI collector proposed and what happened to it.
 *
 * A refused step is a normal, expected outcome: the model proposed something
 * outside the read-only allowlist and the console stopped it before it ran.
 */
export interface AICollectStep {
  command: string
  /**
   * The model's own reasoning for this step, shown to the operator. Empty
   * when the endpoint returns no reasoning or thinking is off.
   */
  thinking?: string
  reason?: string
  refused?: boolean
  refusal?: string
  output?: string
  error?: string
  status?: number
}

/** A credential, API key or loot candidate the model extracted. */
export interface AICollectFinding {
  kind: string
  name?: string
  username?: string
  secret?: string
  content?: string
  source?: string
}

/** One artefact written into the console vaults. */
export interface AICollectStored {
  kind: string
  name: string
  id?: string
}

/** Parameters for one read-only collection run. */
export interface AICollectRequest {
  objective?: string
  no_store?: boolean
  /**
   * Run a second model pass over the extracted findings before filing them,
   * dropping duplicates and entries with no value. The pass answers with
   * indices, so it can remove a finding but never rewrite one.
   */
  filter_findings?: boolean
}

/** One finding the second pass removed, with the model's reason. */
export interface AICollectFiltered {
  /** The finding's position in the extraction phase's output. */
  index: number
  kind: string
  name: string
  reason: string
}

export interface AICollectResult {
  steps: AICollectStep[]
  findings: AICollectFinding[]
  stored: AICollectStored[]
  skipped: string[]
  /** Findings the second pass removed; empty unless filtering was on. */
  filtered?: AICollectFiltered[]
  summary?: string
  stopped: string
  model: string
  dry_run: boolean
}

/**
 * One frame of a streaming run.
 *
 * A collection or escalation run is published as it happens rather than as a
 * final blob, so the operator can watch the model reason and act instead of a
 * spinner. `status` and `summary` carry free text, `step` carries a finished
 * command, and `done` carries the run's result (or an error message in
 * `text` when the run failed after the stream had already started).
 */
export type AIEventType =
  | 'status'
  | 'thinking'
  | 'thinking_delta'
  | 'answer_delta'
  | 'command'
  | 'step'
  | 'summary'
  | 'done'

export interface AIEvent {
  type: AIEventType
  /** Position of the step this frame belongs to, when it names one. */
  index?: number
  /**
   * Free text for status, thinking, command and summary frames, and the
   * increment carried by a thinking_delta or answer_delta frame.
   */
  text?: string
  /** The finished step, on `step` frames. */
  step?: AICollectStep
  /** The run's final value, on the `done` frame. */
  result?: unknown
}

/**
 * Parameters for one escalation attempt.
 *
 * Unlike collection, escalation defaults to the full shell: a run that cannot
 * change state cannot escalate. `read_only` opts back into the allowlist.
 */
export interface AIPrivescRequest {
  objective?: string
  max_steps?: number
  read_only?: boolean
  dry_run?: boolean
  /** Skip the automatic linpeas/winPEAS enumeration and go straight to the model. */
  skip_enum?: boolean
  /** Re-download the enumeration tool instead of using the cache. */
  refresh_tool?: boolean
  /** Leave the uploaded tool on the target instead of deleting it afterwards. */
  keep_tool?: boolean
}

/** The enumeration helper that was staged on the target for this run. */
export interface AIPrivescTool {
  name?: string
  /** The release asset that was fetched, e.g. linpeas.sh. */
  asset?: string
  url?: string
  remotePath?: string
  bytes?: number
  /** True when the download was served from the local cache. */
  cached?: boolean
  /** True when enumeration was skipped, so nothing was staged. */
  skipped?: boolean
  /** True when the staged tool was removed again at the end of the run. */
  removed?: boolean
  error?: string
}

export interface AIPrivescResult {
  steps: AICollectStep[]
  summary?: string
  stopped: string
  model: string
  /** The target's platform as the console saw it, e.g. linux/amd64. */
  platform: string
  read_only: boolean
  dry_run: boolean
  /**
   * True when the run demonstrated elevated access. This is established
   * against the target, not taken from the model's own claim.
   */
  escalated: boolean
  /**
   * How the outcome was established: "session" when the shell itself holds the
   * elevated identity, "route" when a one-shot escape proved the route without
   * changing the session's identity.
   */
  escalated_via?: 'session' | 'route' | string
  /** The output that established the outcome, when one ran. */
  evidence?: string
  tool: AIPrivescTool
}

/** Whether the assistant is available, and what it is allowed to run. */
export interface AIStatus {
  configured: boolean
  baseURL: string
  model: string
  hasKey: boolean
  readOnly: boolean
  allowlist: string[]
}

/** The read-only policy's answer for one command. */
export interface AIReadOnlyCheck {
  allowed: boolean
  path?: string
  args?: string[]
  reason?: string
}

/** The stored AI configuration, including whether the policy is on. */
export interface AISettings {
  baseURL: string
  model: string
  apiKeyEnv: string
  timeoutSeconds: number
  readOnly: boolean
  /**
   * Whether AI features ask the endpoint for the model's reasoning. It is a
   * console-wide setting, so every AI feature sees the same value.
   */
  thinking: boolean
  hasKey: boolean
  managed: boolean
}

/**
 * A change to the stored AI configuration. Every field is optional: the
 * server applies only the ones that are present, so the read-only toggle
 * can be flipped without restating the endpoint and the model.
 */
/** The model list an OpenAI-compatible endpoint advertises. */
export interface AIModels {
  baseURL: string
  models: string[]
}

/**
 * A model-listing request. Blank fields fall back to the stored configuration,
 * so the panel can list models for an endpoint it has not saved yet.
 */
export interface AIModelsRequest {
  baseURL?: string
  apiKey?: string
}

export interface AISettingsUpdate {
  baseURL?: string
  model?: string
  apiKey?: string
  apiKeyEnv?: string
  timeoutSeconds?: number
  readOnly?: boolean
  thinking?: boolean
}
