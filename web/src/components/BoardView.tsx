import type { Board, BoardTask } from '../api'

// The staircase, the same projection the TUI draws: stages across the top,
// every task on its own row sitting in its stage's column, so the board
// reads as a cascade from refinement down to done.

function sessionMark(t: BoardTask): { glyph: string; title: string; cls: string } | null {
  if (!t.session) return null
  const s = t.session
  if (s.running) return { glyph: '▶', title: `${s.name} · running`, cls: 'run' }
  switch (s.outcome) {
    case 'finished':
      return { glyph: '✔', title: `${s.name} · finished`, cls: 'ok' }
    case 'limit':
      return { glyph: '⚠', title: `${s.name} · hit its usage limit`, cls: 'warn' }
    case 'interrupted':
      return { glyph: '⏸', title: `${s.name} · interrupted`, cls: 'warn' }
    default:
      return { glyph: '·', title: s.name, cls: 'dim' }
  }
}

function TaskChip({
  task,
  stages,
  activeSprint,
  onOpen,
  onMove,
  busy,
}: {
  task: BoardTask
  stages: string[]
  activeSprint?: string
  onOpen: (id: string) => void
  onMove: (id: string, to: string) => void
  busy: boolean
}) {
  const idx = stages.indexOf(task.stage)
  const prev = idx > 0 ? stages[idx - 1] : null
  const next = idx >= 0 && idx < stages.length - 1 ? stages[idx + 1] : null
  const mark = sessionMark(task)
  // A task sitting in a non-active sprint (e.g. "next") isn't on the active
  // staircase — the server refuses moves for it, so don't offer them either.
  const canMove = task.area === 'sprint' && (!task.sprint || task.sprint === activeSprint)

  return (
    <div className="chip" onClick={() => onOpen(task.id)}>
      <div className="chip-head">
        <span className={`prio p-${task.priority}`}>{task.priority}</span>
        <span className="tid">{task.id}</span>
        {mark && (
          <span className={`mark ${mark.cls}`} title={mark.title}>
            {mark.glyph}
          </span>
        )}
        {task.paused && (
          <span className="badge paused" title="automation paused">
            paused
          </span>
        )}
        {!!task.openReviews && (
          <span className="badge open" title={`${task.openReviews} open review findings`}>
            {task.openReviews} open
          </span>
        )}
      </div>
      <div className="chip-title">{task.title}</div>
      {canMove && (
        <div className="chip-moves" onClick={e => e.stopPropagation()}>
          <button disabled={!prev || busy} onClick={() => prev && onMove(task.id, prev)} title={prev ? `move to ${prev}` : ''}>
            ←
          </button>
          <button disabled={!next || busy} onClick={() => next && onMove(task.id, next)} title={next ? `move to ${next}` : ''}>
            →
          </button>
        </div>
      )}
    </div>
  )
}

export function BoardView({
  board,
  onOpen,
  onMove,
  busy,
}: {
  board: Board
  onOpen: (id: string) => void
  onMove: (id: string, to: string) => void
  busy: boolean
}) {
  const counts = new Map<string, number>()
  for (const t of board.tasks) counts.set(t.stage, (counts.get(t.stage) ?? 0) + 1)

  return (
    <>
      <div
        className="board"
        style={{ gridTemplateColumns: `repeat(${board.stages.length}, minmax(190px, 1fr))` }}
      >
        {board.stages.map((s, i) => (
          <div className="stage-head" key={s} style={{ gridColumn: i + 1, gridRow: 1 }}>
            {s}
            <span className="count">{counts.get(s) ?? 0}</span>
          </div>
        ))}
        {board.tasks.map((t, r) => {
          const col = board.stages.indexOf(t.stage)
          return (
            <div key={t.id} style={{ gridColumn: (col < 0 ? 0 : col) + 1, gridRow: r + 2 }}>
              <TaskChip task={t} stages={board.stages} activeSprint={board.sprint} onOpen={onOpen} onMove={onMove} busy={busy} />
            </div>
          )
        })}
      </div>
      {board.tasks.length === 0 && <p className="empty">No tasks in this sprint yet.</p>}

      {board.backlog.length > 0 && (
        <section className="backlog">
          <h2>Backlog <span className="count">{board.backlog.length}</span></h2>
          <div className="backlog-grid">
            {board.backlog.map(t => (
              <TaskChip key={t.id} task={t} stages={board.stages} activeSprint={board.sprint} onOpen={onOpen} onMove={onMove} busy={busy} />
            ))}
          </div>
        </section>
      )}
    </>
  )
}
