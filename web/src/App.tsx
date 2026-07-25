import { useCallback, useEffect, useState } from 'react'
import { getBoard, getTask, listWorkspaces, moveTask, triageFinding } from './api'
import type { WorkspaceSummary } from './api'
import { useLiveResource } from './hooks'
import { BoardView } from './components/BoardView'
import { TaskCardPanel } from './components/TaskCardPanel'
import { ReviewCarousel } from './components/ReviewCarousel'

const WS_KEY = 'takos.workspace'

export default function App() {
  const [workspaces, setWorkspaces] = useState<WorkspaceSummary[] | null>(null)
  const [ws, setWs] = useState<string | null>(() => localStorage.getItem(WS_KEY))
  const [openTask, setOpenTask] = useState<string | null>(null)
  const [carousel, setCarousel] = useState(false)
  const [busy, setBusy] = useState(false)
  const [flash, setFlash] = useState<{ kind: 'err' | 'ok'; text: string } | null>(null)

  // The workspace list is daemon-level state — fetched once, then whenever
  // the user reloads. Per-workspace live updates come over SSE below.
  useEffect(() => {
    listWorkspaces().then(
      list => {
        setWorkspaces(list)
        setWs(cur => {
          if (cur && list.some(w => w.name === cur)) return cur
          return list[0]?.name ?? null
        })
      },
      e => setFlash({ kind: 'err', text: String(e.message ?? e) }),
    )
  }, [])

  useEffect(() => {
    if (ws) localStorage.setItem(WS_KEY, ws)
  }, [ws])

  useEffect(() => {
    if (!flash) return
    const t = setTimeout(() => setFlash(null), 6000)
    return () => clearTimeout(t)
  }, [flash])

  const board = useLiveResource(ws, 'board', useCallback(() => getBoard(ws!), [ws]))
  const card = useLiveResource(
    openTask ? ws : null,
    openTask ?? '',
    useCallback(() => getTask(ws!, openTask!), [ws, openTask]),
  )

  const onMove = useCallback(
    async (id: string, to: string) => {
      if (!ws) return
      setBusy(true)
      try {
        const res = await moveTask(ws, id, to)
        setFlash({ kind: 'ok', text: `${res.ID}: ${res.From} → ${res.To}` })
        board.refresh()
        if (openTask === id) card.refresh()
      } catch (e: any) {
        // The validator's refusal, verbatim — the same phrase the TUI shows.
        setFlash({ kind: 'err', text: String(e.message ?? e) })
      } finally {
        setBusy(false)
      }
    },
    [ws, board, card, openTask],
  )

  const onTriage = useCallback(
    async (item: string, status: string) => {
      if (!ws || !openTask) return
      setBusy(true)
      try {
        const res = await triageFinding(ws, openTask, item, status)
        let text = `${res.ItemID} → ${res.Status}`
        if (res.DebtID) text += ` · ${res.DebtID} created in the backlog`
        if (res.MovedTo) text += ` · round triaged, ${openTask} → ${res.MovedTo}`
        setFlash({ kind: 'ok', text })
        if (res.Done) setCarousel(false)
        board.refresh()
        card.refresh()
      } catch (e: any) {
        setFlash({ kind: 'err', text: String(e.message ?? e) })
      } finally {
        setBusy(false)
      }
    },
    [ws, openTask, board, card],
  )

  const current = workspaces?.find(w => w.name === ws)

  return (
    <div className="app">
      <header className="top">
        <span className="brand">takos</span>
        <select value={ws ?? ''} onChange={e => { setWs(e.target.value); setOpenTask(null) }}>
          {(workspaces ?? []).map(w => (
            <option key={w.name} value={w.name}>
              {w.name}
            </option>
          ))}
          {!workspaces?.length && <option value="">no workspaces</option>}
        </select>
        {board.data?.sprint && <span className="sprint">{board.data.sprint}</span>}
        {current && current.state !== 'running' && (
          <span className="badge warn" title={current.reason}>
            {current.state}
          </span>
        )}
        <span className="spread" />
        <span className={board.connected ? 'live on' : 'live off'} title={board.connected ? 'live' : 'reconnecting…'}>
          ● live
        </span>
      </header>

      {flash && <div className={`flash ${flash.kind}`}>{flash.text}</div>}

      <main className={openTask ? 'with-panel' : ''}>
        <div className="boardwrap">
          {board.error && <p className="error">{board.error}</p>}
          {!board.data && !board.error && <p className="empty">Loading…</p>}
          {board.data && <BoardView board={board.data} onOpen={setOpenTask} onMove={onMove} busy={busy} />}
        </div>

        {openTask && card.data && (
          <TaskCardPanel
            card={card.data}
            onClose={() => { setOpenTask(null); setCarousel(false) }}
            onOpenReview={() => setCarousel(true)}
          />
        )}
      </main>

      {carousel && card.data?.review && (
        <ReviewCarousel
          round={card.data.review}
          taskId={card.data.id}
          busy={busy}
          onTriage={onTriage}
          onClose={() => setCarousel(false)}
        />
      )}
    </div>
  )
}
