import { useCallback, useEffect, useRef, useState } from 'react'
import { levels, type LevelSummary, type Platform } from './api'

// LevelEditor.tsx — S415-02/03, founder real-time: "get the brawlpit level editor online - web
// technologies - we already started building nock - can we finish building out some of that
// interface so we can kind of parlay it into an online brawlpit level editor?"
//
// A real, direct-manipulation canvas editor for BRAWLPIT levels. Platform(x,y,w,h) is CENTER-
// based with full width/height (confirmed directly against BRAWLPIT/packages/common/physics.h's
// own resolve_platform_collisions: `b.x - b.w/2` .. `b.x + b.w/2`, `top = b.y + b.h/2`) -- every
// screen<->world conversion and the resize handle's own corner-anchoring math below depend on
// that being exactly right, not assumed.
//
// No canvas/drag library is used -- plain pointer events on a <canvas>, matching this app's own
// established "no framework beyond React itself for a v0 this small" precedent (NOCK_NORTHSTAR.md's
// own explicit "no Redux" call).

const SCALE = 6 // px per world unit
const HANDLE_PX = 9 // resize-handle hit target, in screen pixels

function newDefaultLevel(): { name: string; width: number; height: number; platforms: Platform[] } {
  return {
    name: '',
    width: 80,
    height: 60,
    platforms: [{ x: 0, y: -5, w: 60, h: 10, type: 0 }],
  }
}

type DragMode = 'move' | 'resize'
interface DragState {
  mode: DragMode
  index: number
  startWorldX: number
  startWorldY: number
  orig: Platform
}

function LevelCanvas({
  width,
  height,
  platforms,
  selected,
  onSelect,
  onChange,
}: {
  width: number
  height: number
  platforms: Platform[]
  selected: number | null
  onSelect: (i: number | null) => void
  onChange: (platforms: Platform[]) => void
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const dragRef = useRef<DragState | null>(null)

  const pxW = Math.max(1, Math.round(width * SCALE))
  const pxH = Math.max(1, Math.round(height * SCALE))
  const cx = pxW / 2
  const cy = pxH / 2

  const worldToScreen = useCallback((x: number, y: number) => ({ sx: cx + x * SCALE, sy: cy - y * SCALE }), [cx, cy])
  const screenToWorld = useCallback((sx: number, sy: number) => ({ x: (sx - cx) / SCALE, y: (cy - sy) / SCALE }), [cx, cy])

  const draw = useCallback(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    ctx.clearRect(0, 0, pxW, pxH)
    ctx.fillStyle = '#0f0d09'
    ctx.fillRect(0, 0, pxW, pxH)

    // world-axis guide lines (x=0 / y=0) so authoring against a centered origin is legible.
    ctx.strokeStyle = '#3a3325'
    ctx.lineWidth = 1
    const origin = worldToScreen(0, 0)
    ctx.beginPath()
    ctx.moveTo(origin.sx, 0)
    ctx.lineTo(origin.sx, pxH)
    ctx.moveTo(0, origin.sy)
    ctx.lineTo(pxW, origin.sy)
    ctx.stroke()

    platforms.forEach((p, i) => {
      const topLeft = worldToScreen(p.x - p.w / 2, p.y + p.h / 2)
      const w = p.w * SCALE
      const h = p.h * SCALE
      const isSel = i === selected
      ctx.fillStyle = p.type === 1 ? (isSel ? '#c9a24a' : '#8a7440') : isSel ? '#c1543a' : '#5a4a2f'
      ctx.fillRect(topLeft.sx, topLeft.sy, w, h)
      ctx.strokeStyle = isSel ? '#f2ead9' : '#221e17'
      ctx.lineWidth = isSel ? 2 : 1
      ctx.strokeRect(topLeft.sx, topLeft.sy, w, h)
      if (isSel) {
        ctx.fillStyle = '#f2ead9'
        ctx.fillRect(topLeft.sx + w - HANDLE_PX / 2, topLeft.sy + h - HANDLE_PX / 2, HANDLE_PX, HANDLE_PX)
      }
    })
  }, [platforms, selected, pxW, pxH, worldToScreen])

  useEffect(draw, [draw])

  const hitTest = (sx: number, sy: number): { index: number; onHandle: boolean } | null => {
    for (let i = platforms.length - 1; i >= 0; i--) {
      const p = platforms[i]
      const topLeft = worldToScreen(p.x - p.w / 2, p.y + p.h / 2)
      const w = p.w * SCALE
      const h = p.h * SCALE
      const onHandle =
        Math.abs(sx - (topLeft.sx + w)) < HANDLE_PX && Math.abs(sy - (topLeft.sy + h)) < HANDLE_PX
      const inBody = sx >= topLeft.sx && sx <= topLeft.sx + w && sy >= topLeft.sy && sy <= topLeft.sy + h
      if (onHandle || inBody) return { index: i, onHandle }
    }
    return null
  }

  const onPointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const rect = canvasRef.current!.getBoundingClientRect()
    const sx = e.clientX - rect.left
    const sy = e.clientY - rect.top
    const hit = hitTest(sx, sy)
    if (!hit) {
      onSelect(null)
      return
    }
    onSelect(hit.index)
    const world = screenToWorld(sx, sy)
    dragRef.current = {
      mode: hit.onHandle ? 'resize' : 'move',
      index: hit.index,
      startWorldX: world.x,
      startWorldY: world.y,
      orig: { ...platforms[hit.index] },
    }
    ;(e.target as Element).setPointerCapture(e.pointerId)
  }

  const onPointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current
    if (!drag) return
    const rect = canvasRef.current!.getBoundingClientRect()
    const world = screenToWorld(e.clientX - rect.left, e.clientY - rect.top)
    const dx = world.x - drag.startWorldX
    const dy = world.y - drag.startWorldY
    const next = platforms.slice()

    if (drag.mode === 'move') {
      next[drag.index] = { ...drag.orig, x: drag.orig.x + dx, y: drag.orig.y + dy }
    } else {
      // Corner-anchored resize: the world top-left corner (min-x, max-y) of the ORIGINAL box
      // stays fixed; only the bottom-right corner follows the pointer -- see this file's own
      // top-of-file comment for why (x,y) is the box CENTER, not a corner.
      const leftEdge = drag.orig.x - drag.orig.w / 2
      const topEdge = drag.orig.y + drag.orig.h / 2
      const newW = Math.max(0.5, drag.orig.w + dx)
      const newH = Math.max(0.5, drag.orig.h - dy)
      next[drag.index] = {
        ...drag.orig,
        w: newW,
        h: newH,
        x: leftEdge + newW / 2,
        y: topEdge - newH / 2,
      }
    }
    onChange(next)
  }

  const onPointerUp = () => {
    dragRef.current = null
  }

  return (
    <canvas
      ref={canvasRef}
      width={pxW}
      height={pxH}
      className="level-canvas"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerLeave={onPointerUp}
    />
  )
}

function PlatformInspector({
  platform,
  onChange,
  onDelete,
}: {
  platform: Platform
  onChange: (p: Platform) => void
  onDelete: () => void
}) {
  const num = (v: string) => (v === '' || v === '-' ? 0 : Number(v))
  return (
    <div className="platform-inspector">
      <h3>Selected platform</h3>
      <label>
        X <input type="number" value={platform.x} onChange={(e) => onChange({ ...platform, x: num(e.target.value) })} />
      </label>
      <label>
        Y <input type="number" value={platform.y} onChange={(e) => onChange({ ...platform, y: num(e.target.value) })} />
      </label>
      <label>
        W{' '}
        <input
          type="number"
          min={0.5}
          value={platform.w}
          onChange={(e) => onChange({ ...platform, w: Math.max(0.5, num(e.target.value)) })}
        />
      </label>
      <label>
        H{' '}
        <input
          type="number"
          min={0.5}
          value={platform.h}
          onChange={(e) => onChange({ ...platform, h: Math.max(0.5, num(e.target.value)) })}
        />
      </label>
      <label>
        <select value={platform.type} onChange={(e) => onChange({ ...platform, type: Number(e.target.value) as 0 | 1 })}>
          <option value={0}>Solid</option>
          <option value={1}>Passthrough</option>
        </select>
      </label>
      <button className="danger" type="button" onClick={onDelete}>
        Delete platform
      </button>
    </div>
  )
}

function useLevelList() {
  const [list, setList] = useState<LevelSummary[]>([])
  const refresh = useCallback(() => {
    levels.list().then(setList).catch(() => setList([]))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])
  return { list, refresh }
}

export default function LevelEditor() {
  const { list, refresh } = useLevelList()
  const [activeId, setActiveId] = useState<number | null>(null)
  const [draft, setDraft] = useState(newDefaultLevel())
  const [selected, setSelected] = useState<number | null>(null)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')

  const load = useCallback(async (id: number) => {
    const lvl = await levels.get(id)
    setDraft({ name: lvl.name, width: lvl.width, height: lvl.height, platforms: lvl.platforms })
    setActiveId(id)
    setSelected(null)
    setDirty(false)
    setError(null)
  }, [])

  const startNew = () => {
    setDraft(newDefaultLevel())
    setActiveId(null)
    setSelected(0)
    setDirty(true)
    setError(null)
  }

  const setPlatforms = (platforms: Platform[]) => {
    setDraft((d) => ({ ...d, platforms }))
    setDirty(true)
  }

  const addPlatform = () => {
    setPlatforms([...draft.platforms, { x: 0, y: 0, w: 10, h: 1, type: 0 }])
    setSelected(draft.platforms.length)
  }

  const deleteSelected = () => {
    if (selected === null) return
    setPlatforms(draft.platforms.filter((_, i) => i !== selected))
    setSelected(null)
  }

  const save = async () => {
    setError(null)
    try {
      if (activeId === null) {
        if (!draft.name) {
          setError('Name is required to create a new level.')
          return
        }
        const created = await levels.create(draft.name, draft.width, draft.height, draft.platforms)
        setActiveId(created.id)
      } else {
        await levels.save(activeId, draft.width, draft.height, draft.platforms)
      }
      setDirty(false)
      refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  const doClone = async () => {
    if (activeId === null || !newName) return
    try {
      const cloned = await levels.clone(activeId, newName)
      setNewName('')
      refresh()
      await load(cloned.id)
    } catch (err) {
      setError(String(err))
    }
  }

  const doDelete = async () => {
    if (activeId === null) return
    if (!confirm(`Delete level "${draft.name}"? This can't be undone.`)) return
    await levels.delete(activeId)
    setActiveId(null)
    setDraft(newDefaultLevel())
    refresh()
  }

  return (
    <div className="layout">
      <aside className="project-list">
        <h2>Levels</h2>
        <ul>
          {list.map((l) => (
            <li key={l.id} className={l.id === activeId ? 'active' : ''}>
              <button onClick={() => load(l.id)}>
                {l.name} <span className="hint">({l.platform_count} platforms)</span>
              </button>
            </li>
          ))}
        </ul>
        <button type="button" onClick={startNew}>
          + New level
        </button>
      </aside>

      <main>
        <div className="project-header">
          <input
            className="level-name-input"
            placeholder="Level name"
            value={draft.name}
            onChange={(e) => {
              setDraft((d) => ({ ...d, name: e.target.value }))
              setDirty(true)
            }}
          />
          <div className="dims">
            <label>
              Width{' '}
              <input
                type="number"
                min={1}
                value={draft.width}
                onChange={(e) => {
                  setDraft((d) => ({ ...d, width: Math.max(1, Number(e.target.value)) }))
                  setDirty(true)
                }}
              />
            </label>
            <label>
              Height{' '}
              <input
                type="number"
                min={1}
                value={draft.height}
                onChange={(e) => {
                  setDraft((d) => ({ ...d, height: Math.max(1, Number(e.target.value)) }))
                  setDirty(true)
                }}
              />
            </label>
          </div>
          <button type="button" onClick={addPlatform}>
            + Add platform
          </button>
          <button type="button" onClick={save} disabled={!dirty}>
            {activeId === null ? 'Create level' : 'Save changes'}
          </button>
          {activeId !== null && (
            <>
              <input placeholder="clone as..." value={newName} onChange={(e) => setNewName(e.target.value)} />
              <button type="button" onClick={doClone} disabled={!newName}>
                Clone
              </button>
              <a className="export-link" href={levels.exportUrl(activeId)} target="_blank" rel="noreferrer">
                Export JSON
              </a>
              <button className="danger" type="button" onClick={doDelete}>
                Delete
              </button>
            </>
          )}
          {error && <span className="error">{error}</span>}
        </div>

        <div className="editor-body">
          <div className="canvas-pane">
            <LevelCanvas
              width={draft.width}
              height={draft.height}
              platforms={draft.platforms}
              selected={selected}
              onSelect={setSelected}
              onChange={setPlatforms}
            />
            <p className="hint">
              Drag a platform to move it, drag its bottom-right corner to resize. Click empty space to deselect.
            </p>
          </div>
          <div className="side-pane">
            {selected !== null && draft.platforms[selected] ? (
              <PlatformInspector
                platform={draft.platforms[selected]}
                onChange={(p) => {
                  const next = draft.platforms.slice()
                  next[selected] = p
                  setPlatforms(next)
                }}
                onDelete={deleteSelected}
              />
            ) : (
              <p className="hint">Select a platform to edit its exact position/size, or add a new one.</p>
            )}
          </div>
        </div>
      </main>
    </div>
  )
}
