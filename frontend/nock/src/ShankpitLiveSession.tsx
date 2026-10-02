import { useCallback, useEffect, useRef, useState } from 'react'

// Live map-editing session shared with the native SHANKPIT client (kanban #518/#519/#520/#532, EDIT-4).
// Server side: IDUNA/internal/http/handlers/shankpit_edit_sessions.go. The session id is a capability
// token; either side creates, the other joins by id. The yellow spawner marker follows the SHANKPIT
// avatar (move in SHANKPIT -> marker moves here) and posts its own moves back (drag here -> the
// character moves there). Spawn-at spawner|crosshair is the shared toggle for where "spawn" puts the
// character; the crosshair raycast itself runs on the SHANKPIT side, where the crosshair is.

const BASE = '/api/v1/shankpit-edit-sessions'

export type Vec3 = { x: number; y: number; z: number }
export type SpawnMode = 'spawner' | 'crosshair'
type EditEvent = { seq: number; source: string; kind: string; data?: Record<string, number | string> }
type PollResponse = {
  seq: number
  events: EditEvent[]
  state?: { spawner?: Vec3 & { yaw?: number }; avatar?: Vec3 & { yaw?: number }; spawn_mode?: SpawnMode }
}

export type LiveSession = {
  id: string | null
  status: string
  spawnMode: SpawnMode
  avatar: (Vec3 & { yaw: number }) | null
  create: () => Promise<void>
  join: (id: string) => Promise<void>
  leave: () => void
  setSpawnMode: (m: SpawnMode) => void
}

const same = (a: Vec3, b: Vec3) => Math.abs(a.x - b.x) + Math.abs(a.y - b.y) + Math.abs(a.z - b.z) < 0.01

export function useLiveSession(opts: {
  levelId: number | null
  levelName: string
  spawner: Vec3
  onSpawnerChange: (s: Vec3) => void
}): LiveSession {
  const [id, setId] = useState<string | null>(null)
  const [status, setStatus] = useState('not connected')
  const [spawnMode, setSpawnModeState] = useState<SpawnMode>('spawner')
  const [avatar, setAvatar] = useState<(Vec3 & { yaw: number }) | null>(null)
  const idRef = useRef<string | null>(null)
  const optsRef = useRef(opts)
  optsRef.current = opts
  const lastSent = useRef<Vec3 | null>(null) // last spawner we posted OR received, so we never echo a remote move back

  const post = useCallback(async (kind: string, data: unknown) => {
    const sid = idRef.current
    if (!sid) return
    await fetch(`${BASE}/${sid}/events`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source: 'nock', kind, data }),
    }).catch(() => undefined)
  }, [])

  const apply = useCallback((state: PollResponse['state'], events: EditEvent[]) => {
    if (state?.spawn_mode) setSpawnModeState(state.spawn_mode)
    const stream: EditEvent[] = events.filter((e) => e.source === 'shankpit')
    let av = state?.avatar
    for (const e of stream) {
      if (e.kind === 'avatar' && e.data) av = e.data as unknown as Vec3 & { yaw?: number }
      if (e.kind === 'spawn_mode' && e.data) setSpawnModeState(e.data.mode as SpawnMode)
    }
    if (av && typeof av.x === 'number') {
      setAvatar({ x: av.x, y: av.y, z: av.z, yaw: av.yaw ?? 0 })
      // "move your character in shankpit and it moves the spawner"
      const p = { x: av.x, y: av.y, z: av.z }
      if (!lastSent.current || !same(lastSent.current, p)) {
        lastSent.current = p
        optsRef.current.onSpawnerChange(p)
      }
    }
  }, [])

  // long-poll loop while a session is open
  useEffect(() => {
    if (!id) return
    idRef.current = id
    let stop = false
    const ctl = new AbortController()
    let since = 0
    ;(async () => {
      let fails = 0
      while (!stop) {
        try {
          const r = await fetch(`${BASE}/${id}?since=${since}&wait=20`, { signal: ctl.signal })
          if (r.status === 404) {
            setStatus('session ended (expired or server restarted)')
            setId(null)
            return
          }
          if (!r.ok) throw new Error(String(r.status))
          const body = (await r.json()) as PollResponse
          if (since === 0) apply(body.state, [])
          apply(undefined, body.events)
          since = body.seq
          fails = 0
          setStatus('live')
        } catch {
          if (stop) return
          if (++fails >= 5) {
            setStatus('connection lost')
            setId(null)
            return
          }
          await new Promise((res) => setTimeout(res, 1000))
        }
      }
    })()
    return () => {
      stop = true
      ctl.abort()
    }
  }, [id, apply])

  // our spawner moved -> tell SHANKPIT (debounced; remote-originated moves are not echoed)
  const sp = opts.spawner
  useEffect(() => {
    if (!id) return
    if (lastSent.current && same(lastSent.current, sp)) return
    const t = setTimeout(() => {
      lastSent.current = { x: sp.x, y: sp.y, z: sp.z }
      void post('spawner', { x: sp.x, y: sp.y, z: sp.z, yaw: 0 })
    }, 100)
    return () => clearTimeout(t)
  }, [id, sp.x, sp.y, sp.z, post]) // eslint-disable-line react-hooks/exhaustive-deps

  const open = useCallback(
    async (sid: string) => {
      idRef.current = sid
      lastSent.current = null
      setId(sid)
      setStatus('joining...')
      await post('hello', { client: 'nock' })
    },
    [post],
  )

  return {
    id,
    status,
    spawnMode,
    avatar,
    create: async () => {
      setStatus('creating...')
      try {
        const r = await fetch(BASE, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ level_id: opts.levelId ?? 0, level_name: opts.levelName }),
        })
        if (!r.ok) throw new Error(String(r.status))
        const body = (await r.json()) as { id: string }
        await open(body.id)
      } catch (e) {
        setStatus(`could not create session (${e instanceof Error ? e.message : 'error'})`)
      }
    },
    join: async (sid: string) => {
      const clean = sid.trim().toLowerCase()
      if (!/^[0-9a-f]{32}$/.test(clean)) {
        setStatus('session id must be 32 hex characters')
        return
      }
      await open(clean)
    },
    leave: () => {
      idRef.current = null
      setId(null)
      setAvatar(null)
      setStatus('not connected')
    },
    setSpawnMode: (m: SpawnMode) => {
      setSpawnModeState(m)
      void post('spawn_mode', { mode: m })
    },
  }
}

export function LiveSessionPanel({ live }: { live: LiveSession }) {
  const [joinId, setJoinId] = useState('')
  return (
    <div className="mode-toggle" data-testid="live-session-panel">
      <strong>Live SHANKPIT session</strong>{' '}
      {live.id === null ? (
        <>
          <button type="button" onClick={() => void live.create()}>
            Start live session
          </button>
          <input
            placeholder="join: paste session id"
            value={joinId}
            onChange={(e) => setJoinId(e.target.value)}
            style={{ width: '16em' }}
          />
          <button type="button" onClick={() => void live.join(joinId)} disabled={joinId.trim() === ''}>
            Join
          </button>
        </>
      ) : (
        <>
          <code title="Give this id to the SHANKPIT client: shank_lobby --edit-map --edit-session <id>">{live.id}</code>
          <button type="button" onClick={() => void navigator.clipboard?.writeText(live.id ?? '')}>
            Copy
          </button>
          <label>
            Spawn at{' '}
            <select value={live.spawnMode} onChange={(e) => live.setSpawnMode(e.target.value as SpawnMode)}>
              <option value="spawner">spawner</option>
              <option value="crosshair">crosshair</option>
            </select>
          </label>
          <button type="button" onClick={live.leave}>
            Leave
          </button>
        </>
      )}{' '}
      <span className="hint">{live.status}</span>
    </div>
  )
}
