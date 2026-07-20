// Types mirror the Go projections in internal/trackerd/board.go and
// http.go. The UI is read-first: every view is a projection the daemon
// re-derives from the filesystem, and mutations go through the same
// validator the CLI and TUI use — a refused move comes back as a 409 with
// the exact phrase the TUI status bar shows.

export interface SessionProj {
  name: string
  running: boolean
  outcome?: string
  startedAt?: string
  endedAt?: string
}

export interface BoardTask {
  id: string
  title: string
  priority: string
  stage: string
  area: string
  repo?: string
  labels?: string[]
  paused?: boolean
  openReviews?: number
  session?: SessionProj
}

export interface Board {
  workspace: string
  sprint?: string
  stages: string[]
  tasks: BoardTask[]
  backlog: BoardTask[]
}

export interface ReviewItem {
  id: string
  title: string
  status: 'open' | 'resolved' | 'rework' | 'tech-debt'
  body: string
}

export interface ReviewRound {
  file: string
  items: ReviewItem[]
  verdict?: string
  done: boolean
}

export interface HistoryLine {
  at: string
  actor: string
  verb: string
  args: string
}

export interface TaskCard {
  id: string
  title: string
  priority: string
  repo?: string
  labels?: string[]
  paused?: boolean
  stage: string
  area: string
  sprint?: string
  objective?: string
  context?: string
  plan?: string
  handOff?: string
  whatIsDone?: string
  docs?: string
  testing?: string
  sessions?: SessionProj[]
  review?: ReviewRound
  history?: HistoryLine[]
}

export interface WorkspaceSummary {
  name: string
  root: string
  state: string
  reason?: string
  activeSprint?: string
  tasks?: Record<string, number>
  lastScan?: string
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, init)
  if (!resp.ok) {
    // The API answers refusals in plain text — surface it verbatim.
    throw new Error((await resp.text()).trim() || `${resp.status} ${resp.statusText}`)
  }
  return resp.json() as Promise<T>
}

export const listWorkspaces = () => request<WorkspaceSummary[]>('/api/workspaces')

export const getBoard = (ws: string) => request<Board>(`/api/ws/${encodeURIComponent(ws)}/board`)

export const getTask = (ws: string, id: string) =>
  request<TaskCard>(`/api/ws/${encodeURIComponent(ws)}/task/${encodeURIComponent(id)}`)

export const moveTask = (ws: string, id: string, to: string) =>
  request<{ ID: string; From: string; To: string }>(
    `/api/ws/${encodeURIComponent(ws)}/task/${encodeURIComponent(id)}/move`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ to }),
    },
  )

export const triageFinding = (ws: string, id: string, item: string, status: string) =>
  request<{ ItemID: string; Status: string; DebtID: string; Verdict: string; Done: boolean; MovedTo: string }>(
    `/api/ws/${encodeURIComponent(ws)}/task/${encodeURIComponent(id)}/review/${encodeURIComponent(item)}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status }),
    },
  )

// eventsURL is the per-workspace SSE stream. The event carries no payload:
// a ping means "something changed on disk, re-fetch".
export const eventsURL = (ws: string) => `/api/ws/${encodeURIComponent(ws)}/events`
