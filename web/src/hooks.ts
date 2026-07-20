import { useCallback, useEffect, useRef, useState } from 'react'
import { eventsURL } from './api'

// useLive subscribes to a workspace's SSE stream and calls onChange on
// every ping. The callback lives in a ref so re-renders never tear the
// stream down and rebuild it.
export function useLive(ws: string | null, onChange: () => void) {
  const cb = useRef(onChange)
  cb.current = onChange
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    if (!ws) return
    const es = new EventSource(eventsURL(ws))
    const changed = () => cb.current()
    es.addEventListener('changed', changed)
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)
    return () => {
      es.removeEventListener('changed', changed)
      es.close()
      setConnected(false)
    }
  }, [ws])

  return connected
}

// useLiveResource loads a projection and re-loads it whenever the
// workspace reports a change. `key` distinguishes what is being loaded
// (the board, or one task's card) so switching tasks re-fetches.
export function useLiveResource<T>(
  ws: string | null,
  key: string,
  load: () => Promise<T>,
) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const loadRef = useRef(load)
  loadRef.current = load

  const refresh = useCallback(() => {
    if (!ws) return
    loadRef.current().then(
      d => {
        setData(d)
        setError(null)
      },
      e => setError(e?.message ? String(e.message) : String(e)),
    )
    // key participates so a different target re-fetches
  }, [ws, key])

  useEffect(() => {
    refresh()
  }, [refresh])

  const connected = useLive(ws, refresh)
  return { data, error, refresh, connected }
}
