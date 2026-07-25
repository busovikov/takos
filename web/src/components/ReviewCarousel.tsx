import { useCallback, useEffect, useState } from 'react'
import type { ReviewRound } from '../api'

// One finding per screen, exactly like the TUI carousel: r/w/t triage the
// current finding, Tab and the arrows walk the round in a circle, and a
// decision jumps to the next still-open finding.

const ACTIONS: Array<{ key: string; status: string; label: string }> = [
  { key: 'r', status: 'resolved', label: 'resolve' },
  { key: 'w', status: 'rework', label: 'rework' },
  { key: 't', status: 'tech-debt', label: 'tech debt' },
]

export function ReviewCarousel({
  round,
  taskId,
  busy,
  onTriage,
  onClose,
}: {
  round: ReviewRound
  taskId: string
  busy: boolean
  onTriage: (item: string, status: string) => void
  onClose: () => void
}) {
  const [idx, setIdx] = useState(() => {
    const first = round.items.findIndex(i => i.status === 'open')
    return first >= 0 ? first : 0
  })
  const n = round.items.length
  const item = round.items[Math.min(idx, n - 1)]

  const step = useCallback((delta: number) => setIdx(i => (i + delta + n) % n), [n])

  const triage = useCallback(
    (status: string) => {
      if (busy || !item) return
      onTriage(item.id, status)
      // Jump to the next still-open finding, wrapping around.
      const from = round.items.findIndex(i => i.id === item.id)
      for (let k = 1; k <= n; k++) {
        const j = (from + k) % n
        if (round.items[j].status === 'open' && round.items[j].id !== item.id) {
          setIdx(j)
          return
        }
      }
    },
    [busy, item, n, onTriage, round.items],
  )

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') return onClose()
      if (e.key === 'Tab') {
        e.preventDefault()
        return step(e.shiftKey ? -1 : 1)
      }
      if (e.key === 'ArrowRight') return step(1)
      if (e.key === 'ArrowLeft') return step(-1)
      const action = ACTIONS.find(a => a.key === e.key.toLowerCase())
      if (action) {
        e.preventDefault()
        triage(action.status)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [step, triage, onClose])

  if (!item) return null
  const counts = round.items.reduce<Record<string, number>>((acc, i) => {
    acc[i.status] = (acc[i.status] ?? 0) + 1
    return acc
  }, {})

  return (
    <div className="overlay" onClick={onClose}>
      <div className="carousel" onClick={e => e.stopPropagation()}>
        <header>
          <span className="tid">{taskId}</span>
          <span className="dim">{round.file}</span>
          <span className="dim">
            finding {idx + 1}/{n}
          </span>
          <span className="dim spread">
            {counts['open'] ?? 0} open · {counts['resolved'] ?? 0} resolved · {counts['rework'] ?? 0} rework ·{' '}
            {counts['tech-debt'] ?? 0} tech-debt
          </span>
          <button className="close" onClick={onClose}>
            ✕
          </button>
        </header>

        <h2>
          <span className="fid">{item.id}</span> {item.title}
          <span className={`fstatus st-${item.status}`}>{item.status}</span>
        </h2>

        <pre className="finding-body">{item.body}</pre>

        <footer>
          <div className="nav">
            <button onClick={() => step(-1)} title="previous (⇧Tab)">
              ←
            </button>
            <button onClick={() => step(1)} title="next (Tab)">
              →
            </button>
          </div>
          <div className="actions">
            {ACTIONS.map(a => (
              <button
                key={a.status}
                disabled={busy}
                className={`act act-${a.status}`}
                onClick={() => triage(a.status)}
                title={`${a.label} (${a.key})`}
              >
                <kbd>{a.key}</kbd> {a.label}
              </button>
            ))}
          </div>
        </footer>
      </div>
    </div>
  )
}
