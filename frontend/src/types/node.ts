// A managed node: another 3s-ui panel this one monitors over its apiv2.
// Connection config lives in the DB; live status (NodeStatus) is an in-memory
// snapshot on the server, delivered as `nodesStatus` in api/load responses.

export interface Node {
  id: number
  enable: boolean
  name: string
  baseUrl: string
  webPath: string
  // Write-only: the server never echoes the token back, it reports tokenSet.
  token?: string
  insecure: boolean
  certPin?: string
  desc?: string
  lastSeen: number
  tokenSet?: boolean
  // Phase 2 sync state
  dirty?: boolean
  lastSync?: number
  // one_way (default) | bidirectional — never auto-enabled
  syncMode?: string
  // Multi-node management
  group?: string
  location?: string
  // SSRF policy: loopback / private / CGNAT addresses need an explicit opt-in.
  allowPrivate: boolean
  // Leave this node's servers out of every subscription (links stay stored).
  subExclude: boolean
  // When the capabilities were last read from the node (unix seconds).
  capsAt?: number
}

export type NodeState = 'online' | 'offline' | 'core-stopped'

// Derived on the master: precedence disabled > offline > incompatible >
// syncing > degraded > online.
export type NodeHealth = 'online' | 'offline' | 'degraded' | 'incompatible' | 'syncing' | 'disabled'

export interface NodeSyncState {
  running: boolean
  lastAttempt: number
  lastOk: number
  lastError?: string
  failures: number
  nextRetryAt?: number
}

export interface NodeStatus {
  state: NodeState
  latency: number
  cpu: number
  mem: { current: number; total: number }
  appVersion: string
  coreVersion: string
  error?: string
  checkedAt: number
  lastOnline: number
  swap?: { current: number; total: number }
  disk?: { current: number; total: number }
  net?: { up: number; down: number; sent: number; recv: number }
  coreUptime?: number
  hostUptime?: number
  maintenance?: boolean
  stale?: boolean
  failures?: number
  nextProbeAt?: number
  health?: NodeHealth
  reasons?: string[]
  apiVersion?: number
  sync?: NodeSyncState
}

export interface NodeCapabilities {
  apiVersion: number
  minMasterApi: number
  appVersion: string
  coreVersion: string
  os: string
  arch: string
  goVersion: string
  buildTags: string[]
  inbounds: string[]
  outbounds: string[]
  endpoints: string[]
  services: string[]
  dnsServers: string[]
  panelInbounds: string[]
  transports: string[]
  tls: { tls: boolean; utls: boolean; reality: boolean; ech: boolean; acme: boolean }
  components: Record<string, boolean>
  management: string[]
  features: string[]
}

export interface NodeOperation {
  id: number
  nodeId: number
  requestId: string
  op: string
  object?: string
  action?: string
  actor: string
  status: 'ok' | 'failed' | 'refused'
  error?: string
  attempts: number
  startedAt: number
  durationMs: number
}

export interface NodeDetail {
  node: Node
  status: NodeStatus
  capabilities: NodeCapabilities | null
  legacy: boolean
  capsKnown: boolean
  capsError?: string
  adopted: string[] | null
  operations: NodeOperation[] | null
  remoteOps: string[]
  masterApiVersion: number
}

export const defaultNode: Node = {
  id: 0,
  enable: true,
  name: '',
  baseUrl: '',
  webPath: '/app/',
  token: '',
  insecure: false,
  certPin: '',
  desc: '',
  lastSeen: 0,
  syncMode: 'one_way',
  group: '',
  location: '',
  allowPrivate: false,
  subExclude: false,
}
