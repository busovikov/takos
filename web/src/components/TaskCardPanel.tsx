import type { TaskCard } from '../api'

// The card: the same sections the TUI shows — Objective and Context from
// task.md, the plan and hand-off artifacts, the handover sections, the
// session log and the latest review round.

function Section({ title, body }: { title: string; body?: string }) {
  if (!body) return null
  return (
    <section className="sec">
      <h3>{title}</h3>
      <pre>{body}</pre>
    </section>
  )
}

function when(ts?: string) {
  if (!ts) return ''
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}

export function TaskCardPanel({
  card,
  onClose,
  onOpenReview,
}: {
  card: TaskCard
  onClose: () => void
  onOpenReview: () => void
}) {
  const open = card.review?.items.filter(i => i.status === 'open').length ?? 0

  return (
    <aside className="panel">
      <header className="panel-head">
        <div>
          <span className={`prio p-${card.priority}`}>{card.priority}</span>
          <span className="tid">{card.id}</span>
          <span className="stage-pill">{card.stage}</span>
          {card.paused && <span className="badge paused">paused</span>}
        </div>
        <button className="close" onClick={onClose} title="close">
          ✕
        </button>
      </header>

      <h2 className="panel-title">{card.title}</h2>
      <div className="meta">
        {card.repo && <span title="repository">{card.repo}</span>}
        {card.sprint && <span title="sprint">{card.sprint}</span>}
        {card.labels?.map(l => (
          <span className="label" key={l}>
            {l}
          </span>
        ))}
      </div>

      <Section title="Objective" body={card.objective} />
      <Section title="Context" body={card.context} />
      <Section title="Plan" body={card.plan} />
      <Section title="Hand-off" body={card.handOff} />
      <Section title="What is done" body={card.whatIsDone} />
      <Section title="Docs" body={card.docs} />
      <Section title="Testing" body={card.testing} />

      {card.review && (
        <section className="sec">
          <h3>
            Review · {card.review.file}
            <button className="link" onClick={onOpenReview}>
              open carousel
            </button>
          </h3>
          <ul className="findings">
            {card.review.items.map(it => (
              <li key={it.id} className={`st-${it.status}`}>
                <span className="fid">{it.id}</span> {it.title}
                <span className="fstatus">{it.status}</span>
              </li>
            ))}
          </ul>
          <p className="verdict">
            {card.review.done
              ? `triaged · verdict: ${card.review.verdict}`
              : `${open} finding${open === 1 ? '' : 's'} still open`}
          </p>
        </section>
      )}

      {!!card.sessions?.length && (
        <section className="sec">
          <h3>Sessions</h3>
          <ul className="sessions">
            {card.sessions.map(s => (
              <li key={s.name}>
                <span className={s.running ? 'mark run' : `mark ${s.outcome === 'finished' ? 'ok' : 'warn'}`}>
                  {s.running ? '▶' : s.outcome === 'finished' ? '✔' : s.outcome === 'limit' ? '⚠' : '⏸'}
                </span>
                <code>{s.name}</code>
                <span className="dim">{s.running ? 'running' : s.outcome}</span>
                <span className="dim when">{when(s.endedAt || s.startedAt)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {!!card.history?.length && (
        <section className="sec">
          <h3>Journal</h3>
          <ul className="history">
            {card.history
              .slice()
              .reverse()
              .map((h, i) => (
                <li key={i}>
                  <span className="dim when">{when(h.at)}</span>
                  <span className="actor">{h.actor}</span>
                  <span className="verb">{h.verb}</span>
                  <span className="args">{h.args}</span>
                </li>
              ))}
          </ul>
        </section>
      )}
    </aside>
  )
}
