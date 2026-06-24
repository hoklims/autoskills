export type SuggestionStatus = 'pending' | 'accepted' | 'rejected'
export type StatusFilter = SuggestionStatus | 'all'
export type Signal = 'correction' | 'rediscovery' | 'failure_fix' | 'convention' | 'workflow'
export type Scope = 'machine' | 'repo'
export type Placement = 'always_on' | 'path_scoped' | 'skill'
export type DecisionAction = 'accept' | 'reject'

export interface Evidence {
  excerpt: string
  sessionId: string
  tool: 'cursor' | 'claude'
}

export interface Suggestion {
  id: string
  createdAt: string
  status: SuggestionStatus
  title: string
  signal: Signal
  scope: Scope
  placement: Placement
  sensitivity: boolean
  confidence: number
  project: string
  repoRoot: string
  targetPath: string
  body: string
  rationale: string
  evidence: Evidence[]
}

export interface Stats {
  pending: number
  accepted: number
  rejected: number
  sessions: number
  projects: number
}

export interface Project {
  name: string
  repoRoot: string
  pending: number
}

export interface DecisionResponse {
  ok: boolean
  writtenPath?: string
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init)
  if (!res.ok) {
    throw new Error(`${init?.method ?? 'GET'} ${path} failed: ${res.status}`)
  }
  return res.json() as Promise<T>
}

export function getStats(): Promise<Stats> {
  return request<Stats>('/api/stats')
}

export function getSuggestions(status: StatusFilter): Promise<{ suggestions: Suggestion[] }> {
  return request<{ suggestions: Suggestion[] }>(`/api/suggestions?status=${status}`)
}

export function getProjects(): Promise<{ projects: Project[] }> {
  return request<{ projects: Project[] }>('/api/projects')
}

export function postDecision(id: string, action: DecisionAction, body?: string): Promise<DecisionResponse> {
  return request<DecisionResponse>(`/api/suggestions/${id}/decision`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body !== undefined ? { action, body } : { action }),
  })
}
