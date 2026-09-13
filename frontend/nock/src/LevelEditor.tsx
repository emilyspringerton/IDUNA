import { useCallback, useEffect, useRef, useState } from 'react'
import { levels, type Guide, type LevelSummary, type Platform } from './api'

// LevelEditor.tsx — S415-02/03 (base editor), S418-01..04 (guide-based snapping), founder
// real-time: "get the brawlpit level editor online..." then a full requirements doc, "NOCK —
// Guide-Based Snapping": no grid, ever -- free placement is the only default; snapping exists
// only against author-placed guides. See EMILY/BACKLOG.md SECTION 418 for the full doc.
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

const SCALE = 6 // px per world unit. No zoom control exists yet, so "identical at every zoom
// level" (requirements doc 2.3) is trivially true today (there is only one level); the threshold
// is still computed as screenPx/SCALE (world units), not a bare world-unit constant, so this
// keeps holding the moment a zoom control is added.
const HANDLE_PX = 9 // resize-handle / guide-line hit target, in screen pixels
const RULER_PX = 18 // ruler strip thickness, in screen pixels
const DEFAULT_SNAP_THRESHOLD_PX = 7 // requirements doc 2.3: "Roughly 6-8px. Configurable."

function newDefaultLevel(): { name: string; width: number; height: number; platforms: Platform[]; guides: Guide[] } {
  return {
    name: '',
    width: 80,
    height: 60,
    platforms: [{ x: 0, y: -5, w: 60, h: 10, type: 0 }],
    guides: [],
  }
}

// computeTransform is the one real source of truth for world<->screen conversion, shared by
// LevelCanvas and CanvasArea's own ruler-drag-to-create-guide math -- both must agree exactly on
// where world (0,0) sits on screen, or a guide dragged from a ruler would land at the wrong
// coordinate the moment the canvas actually draws it.
function computeTransform(width: number, height: number) {
  const pxW = Math.max(1, Math.round(width * SCALE))
  const pxH = Math.max(1, Math.round(height * SCALE))
  const cx = pxW / 2
  const cy = pxH / 2
  return {
    pxW,
    pxH,
    worldToScreen: (x: number, y: number) => ({ sx: cx + x * SCALE, sy: cy - y * SCALE }),
    screenToWorld: (sx: number, sy: number) => ({ x: (sx - cx) / SCALE, y: (cy - sy) / SCALE }),
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

// bestSnap finds the closest (candidate, guide) pair within threshold. candidateIndex tells the
// caller WHICH candidate matched (only meaningful for resize, where the candidates aren't all
// rigidly linked by a single delta the way move's candidates are).
function bestSnap(
  candidates: number[],
  guides: { coord: number; i: number }[],
  threshold: number,
): { candidateIndex: number; delta: number; guideIndex: number } | null {
  let best: { candidateIndex: number; delta: number; guideIndex: number; abs: number } | null = null
  candidates.forEach((c, ci) => {
    guides.forEach((g) => {
      const delta = g.coord - c
      const abs = Math.abs(delta)
      if (abs <= threshold && (!best || abs < best.abs)) {
        best = { candidateIndex: ci, delta, guideIndex: g.i, abs }
      }
    })
  })
  return best
}

function LevelCanvas({
  width,
  height,
  platforms,
  guides,
  selected,
  selectedGuide,
  showGuides,
  snapEnabled,
  snapThresholdPx,
  onSelect,
  onSelectGuide,
  onChange,
  onChangeGuides,
}: {
  width: number
  height: number
  platforms: Platform[]
  guides: Guide[]
  selected: number | null
  selectedGuide: number | null
  showGuides: boolean
  snapEnabled: boolean
  snapThresholdPx: number
  onSelect: (i: number | null) => void
  onSelectGuide: (i: number | null) => void
  onChange: (platforms: Platform[]) => void
  onChangeGuides: (guides: Guide[]) => void
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const dragRef = useRef<DragState | null>(null)
  const guideDragRef = useRef<number | null>(null)
  const [snapHighlight, setSnapHighlight] = useState<number[]>([])

  const { pxW, pxH, worldToScreen, screenToWorld } = computeTransform(width, height)

  const draw = useCallback(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    ctx.clearRect(0, 0, pxW, pxH)
    ctx.fillStyle = '#0f0d09'
    ctx.fillRect(0, 0, pxW, pxH)

    // world-axis guide lines (x=0 / y=0) so authoring against a centered origin is legible.
    // Deliberately distinct from real author-placed Guides below (dimmer, never selectable).
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

    // Real, author-placed guides (S418-01/02). Requirements doc 2.5: "The guide being snapped to
    // highlights during the drag. No other chrome, no tooltips, no numbers popping up" -- so the
    // ONLY extra state drawn here beyond color is the highlight itself.
    if (showGuides) {
      guides.forEach((g, i) => {
        const isSelected = i === selectedGuide
        const isSnapped = snapHighlight.includes(i)
        let color = g.is_mirror_axis ? '#7ab8c9' : '#5fb0e6'
        if (isSnapped) color = '#ffe27a'
        else if (isSelected) color = '#ffffff'
        ctx.strokeStyle = color
        ctx.lineWidth = isSnapped ? 2.5 : g.locked ? 1 : 1.5
        if (g.locked) ctx.setLineDash([4, 3])
        else ctx.setLineDash([])
        ctx.beginPath()
        if (g.axis === 'horizontal') {
          const { sy } = worldToScreen(0, g.coord)
          ctx.moveTo(0, sy)
          ctx.lineTo(pxW, sy)
        } else {
          const { sx } = worldToScreen(g.coord, 0)
          ctx.moveTo(sx, 0)
          ctx.lineTo(sx, pxH)
        }
        ctx.stroke()
        ctx.setLineDash([])
      })
    }
  }, [platforms, guides, selected, selectedGuide, showGuides, snapHighlight, pxW, pxH, worldToScreen])

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

  // hitTestGuide takes priority over platforms, matching Photoshop's own established convention
  // (guides stay grabbable even over content, until locked).
  const hitTestGuide = (sx: number, sy: number): number | null => {
    if (!showGuides) return null
    for (let i = guides.length - 1; i >= 0; i--) {
      const g = guides[i]
      if (g.axis === 'horizontal') {
        const { sy: gy } = worldToScreen(0, g.coord)
        if (Math.abs(sy - gy) <= HANDLE_PX) return i
      } else {
        const { sx: gx } = worldToScreen(g.coord, 0)
        if (Math.abs(sx - gx) <= HANDLE_PX) return i
      }
    }
    return null
  }

  const onPointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const rect = canvasRef.current!.getBoundingClientRect()
    const sx = e.clientX - rect.left
    const sy = e.clientY - rect.top

    const gIdx = hitTestGuide(sx, sy)
    if (gIdx !== null) {
      onSelect(null)
      onSelectGuide(gIdx)
      // Requirements doc 1.3: "Lock toggle, so an established guide can't be nudged by
      // accident." A locked guide is still selectable (for the inspector/unlock), just not
      // draggable.
      if (!guides[gIdx].locked) {
        guideDragRef.current = gIdx
        ;(e.target as Element).setPointerCapture(e.pointerId)
      }
      return
    }
    onSelectGuide(null)

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
    const rect = canvasRef.current!.getBoundingClientRect()
    const sx = e.clientX - rect.left
    const sy = e.clientY - rect.top

    if (guideDragRef.current !== null) {
      const i = guideDragRef.current
      const world = screenToWorld(sx, sy)
      const next = guides.slice()
      next[i] = { ...next[i], coord: guides[i].axis === 'horizontal' ? world.y : world.x }
      onChangeGuides(next)
      return
    }

    const drag = dragRef.current
    if (!drag) return
    const world = screenToWorld(sx, sy)
    const dx = world.x - drag.startWorldX
    const dy = world.y - drag.startWorldY
    const next = platforms.slice()

    // Requirements doc 2.4: "Holding a modifier suppresses snapping entirely for that drag."
    // Alt is used since Shift/Ctrl are the conventional browser text-selection/multi-select
    // modifiers this editor may want free later.
    const snapActive = snapEnabled && !e.altKey && guides.length > 0
    const thresholdWorld = snapThresholdPx / SCALE
    const vGuides = guides.map((g, i) => ({ coord: g.coord, i })).filter((_, i) => guides[i].axis === 'vertical')
    const hGuides = guides.map((g, i) => ({ coord: g.coord, i })).filter((_, i) => guides[i].axis === 'horizontal')
    const snapped: number[] = []

    if (drag.mode === 'move') {
      let nx = drag.orig.x + dx
      let ny = drag.orig.y + dy
      if (snapActive) {
        // All three X edges (left/right/center) move together under a rigid translation, so
        // whichever one lands closest to a guide determines a single shared delta.
        const snapX = bestSnap([nx - drag.orig.w / 2, nx + drag.orig.w / 2, nx], vGuides, thresholdWorld)
        if (snapX) {
          nx += snapX.delta
          snapped.push(snapX.guideIndex)
        }
        const snapY = bestSnap([ny + drag.orig.h / 2, ny - drag.orig.h / 2, ny], hGuides, thresholdWorld)
        if (snapY) {
          ny += snapY.delta
          snapped.push(snapY.guideIndex)
        }
      }
      next[drag.index] = { ...drag.orig, x: nx, y: ny }
    } else {
      // Corner-anchored resize: the world top-left corner (min-x, max-y) of the ORIGINAL box
      // stays fixed; only the bottom-right corner follows the pointer -- see this file's own
      // top-of-file comment for why (x,y) is the box CENTER, not a corner.
      const leftEdge = drag.orig.x - drag.orig.w / 2
      const topEdge = drag.orig.y + drag.orig.h / 2
      let newW = Math.max(0.5, drag.orig.w + dx)
      let newH = Math.max(0.5, drag.orig.h - dy)

      if (snapActive) {
        const rightEdge = leftEdge + newW
        const centerX = leftEdge + newW / 2
        const snapX = bestSnap([rightEdge, centerX], vGuides, thresholdWorld)
        if (snapX) {
          newW = snapX.candidateIndex === 0 ? newW + snapX.delta : newW + 2 * snapX.delta
          snapped.push(snapX.guideIndex)
        }
        const bottomEdge = topEdge - newH
        const centerY = topEdge - newH / 2
        const snapY = bestSnap([bottomEdge, centerY], hGuides, thresholdWorld)
        if (snapY) {
          newH = snapY.candidateIndex === 0 ? newH - snapY.delta : newH - 2 * snapY.delta
          snapped.push(snapY.guideIndex)
        }
        newW = Math.max(0.5, newW)
        newH = Math.max(0.5, newH)
      }

      next[drag.index] = {
        ...drag.orig,
        w: newW,
        h: newH,
        x: leftEdge + newW / 2,
        y: topEdge - newH / 2,
      }
    }
    setSnapHighlight(snapped)
    onChange(next)
  }

  const onPointerUp = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (guideDragRef.current !== null) {
      const i = guideDragRef.current
      const rect = canvasRef.current!.getBoundingClientRect()
      const sx = e.clientX - rect.left
      const sy = e.clientY - rect.top
      // Requirements doc 1.3: "Drag off-canvas to delete."
      if (sx < 0 || sx > pxW || sy < 0 || sy > pxH) {
        onChangeGuides(guides.filter((_, gi) => gi !== i))
        onSelectGuide(null)
      }
      guideDragRef.current = null
    }
    dragRef.current = null
    setSnapHighlight([])
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

// CanvasArea wraps LevelCanvas with the real ruler strips guides are dragged FROM (requirements
// doc 1.2: "Drag from a ruler edge into the canvas, Photoshop-style"). Pointer capture on the
// ruler element itself (same trick LevelCanvas's own platform-drag already uses) means the drag
// keeps reporting to this component's handlers even once the cursor visually crosses onto the
// canvas -- no window-level listeners needed.
function CanvasArea(props: {
  width: number
  height: number
  platforms: Platform[]
  guides: Guide[]
  selected: number | null
  selectedGuide: number | null
  showGuides: boolean
  snapEnabled: boolean
  snapThresholdPx: number
  onSelect: (i: number | null) => void
  onSelectGuide: (i: number | null) => void
  onChange: (platforms: Platform[]) => void
  onChangeGuides: (guides: Guide[]) => void
  onCreateGuide: (axis: 'horizontal' | 'vertical', coord: number) => void
}) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const [creating, setCreating] = useState<{ axis: 'horizontal' | 'vertical'; screenCoord: number } | null>(null)
  const { pxW, pxH, screenToWorld } = computeTransform(props.width, props.height)

  const localPoint = (clientX: number, clientY: number) => {
    const rect = wrapRef.current!.getBoundingClientRect()
    return { sx: clientX - rect.left, sy: clientY - rect.top }
  }

  const beginCreate = (axis: 'horizontal' | 'vertical') => (e: React.PointerEvent) => {
    ;(e.target as Element).setPointerCapture(e.pointerId)
    const { sx, sy } = localPoint(e.clientX, e.clientY)
    setCreating({ axis, screenCoord: axis === 'horizontal' ? sy : sx })
  }
  const moveCreate = (e: React.PointerEvent) => {
    if (!creating) return
    const { sx, sy } = localPoint(e.clientX, e.clientY)
    setCreating({ ...creating, screenCoord: creating.axis === 'horizontal' ? sy : sx })
  }
  const endCreate = (e: React.PointerEvent) => {
    if (!creating) return
    const { sx, sy } = localPoint(e.clientX, e.clientY)
    if (sx >= 0 && sx <= pxW && sy >= 0 && sy <= pxH) {
      const world = screenToWorld(sx, sy)
      props.onCreateGuide(creating.axis, creating.axis === 'horizontal' ? world.y : world.x)
    }
    setCreating(null)
  }
  const cancelCreate = () => setCreating(null)

  return (
    <div className="canvas-grid" style={{ gridTemplateColumns: `${RULER_PX}px auto`, gridTemplateRows: `${RULER_PX}px auto` }}>
      <div className="ruler-corner" />
      <div
        className="ruler ruler-top"
        style={{ width: pxW }}
        onPointerDown={beginCreate('horizontal')}
        onPointerMove={moveCreate}
        onPointerUp={endCreate}
        onPointerCancel={cancelCreate}
        title="Drag down to create a horizontal guide"
      />
      <div
        className="ruler ruler-left"
        style={{ height: pxH }}
        onPointerDown={beginCreate('vertical')}
        onPointerMove={moveCreate}
        onPointerUp={endCreate}
        onPointerCancel={cancelCreate}
        title="Drag right to create a vertical guide"
      />
      <div className="canvas-wrap" ref={wrapRef}>
        <LevelCanvas
          width={props.width}
          height={props.height}
          platforms={props.platforms}
          guides={props.guides}
          selected={props.selected}
          selectedGuide={props.selectedGuide}
          showGuides={props.showGuides}
          snapEnabled={props.snapEnabled}
          snapThresholdPx={props.snapThresholdPx}
          onSelect={props.onSelect}
          onSelectGuide={props.onSelectGuide}
          onChange={props.onChange}
          onChangeGuides={props.onChangeGuides}
        />
        {creating && (
          <div
            className={`guide-preview guide-preview-${creating.axis}`}
            style={
              creating.axis === 'horizontal'
                ? { top: creating.screenCoord, left: 0, width: pxW }
                : { left: creating.screenCoord, top: 0, height: pxH }
            }
          />
        )}
      </div>
    </div>
  )
}

function PlatformInspector({
  platform,
  onChange,
  onDelete,
  onCreateGuideFromEdge,
}: {
  platform: Platform
  onChange: (p: Platform) => void
  onDelete: () => void
  onCreateGuideFromEdge: (edge: 'left' | 'right' | 'top' | 'bottom') => void
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

      {/* Requirements doc 1.2: "Also creatable from a selected platform: 'guide from left /
          right / top / bottom edge.' This is the important one -- it captures a spacing that
          already works rather than a guessed number." */}
      <div className="guide-from-edge-row">
        <span className="hint">Guide from edge:</span>
        <button type="button" onClick={() => onCreateGuideFromEdge('left')}>Left</button>
        <button type="button" onClick={() => onCreateGuideFromEdge('right')}>Right</button>
        <button type="button" onClick={() => onCreateGuideFromEdge('top')}>Top</button>
        <button type="button" onClick={() => onCreateGuideFromEdge('bottom')}>Bottom</button>
      </div>

      <button className="danger" type="button" onClick={onDelete}>
        Delete platform
      </button>
    </div>
  )
}

// GuideInspector is the requirements doc's own §1.3 manipulation surface (numeric coordinate,
// lock, mirror-axis flag, delete) plus the §1.3/2.1 global visibility/snap-enable/threshold
// toggles (deliberately independent of any one guide's own state, per doc 1.3's own "Showing
// guides and snapping to them are independent").
function GuideInspector({
  guides,
  selected,
  showGuides,
  snapEnabled,
  snapThresholdPx,
  canMirror,
  onSelect,
  onUpdate,
  onDelete,
  onToggleShowGuides,
  onToggleSnapEnabled,
  onChangeThreshold,
  onMirrorSelection,
}: {
  guides: Guide[]
  selected: number | null
  showGuides: boolean
  snapEnabled: boolean
  snapThresholdPx: number
  canMirror: boolean
  onSelect: (i: number | null) => void
  onUpdate: (i: number, g: Guide) => void
  onDelete: (i: number) => void
  onToggleShowGuides: () => void
  onToggleSnapEnabled: () => void
  onChangeThreshold: (px: number) => void
  onMirrorSelection: () => void
}) {
  return (
    <div className="guide-inspector">
      <h3>Guides</h3>
      <label>
        <input type="checkbox" checked={showGuides} onChange={onToggleShowGuides} /> Show guides
      </label>
      <label>
        <input type="checkbox" checked={snapEnabled} onChange={onToggleSnapEnabled} /> Snap to guides
      </label>
      <label>
        Threshold (px){' '}
        <input
          type="number"
          min={1}
          max={40}
          value={snapThresholdPx}
          onChange={(e) => onChangeThreshold(Math.max(1, Number(e.target.value) || DEFAULT_SNAP_THRESHOLD_PX))}
        />
      </label>

      {guides.length === 0 ? (
        <p className="hint">No guides yet -- drag from a ruler, or use "guide from edge" on a selected platform.</p>
      ) : (
        <ul className="guide-list">
          {guides.map((g, i) => (
            <li key={i} className={i === selected ? 'active' : ''} onClick={() => onSelect(i)}>
              <span className="guide-axis">{g.axis === 'horizontal' ? 'H' : 'V'}</span>
              <input
                type="number"
                value={g.coord}
                onClick={(e) => e.stopPropagation()}
                onChange={(e) => onUpdate(i, { ...g, coord: Number(e.target.value) || 0 })}
              />
              <label onClick={(e) => e.stopPropagation()}>
                <input type="checkbox" checked={g.locked} onChange={() => onUpdate(i, { ...g, locked: !g.locked })} /> lock
              </label>
              <label onClick={(e) => e.stopPropagation()}>
                <input
                  type="checkbox"
                  checked={g.is_mirror_axis}
                  onChange={() => onUpdate(i, { ...g, is_mirror_axis: !g.is_mirror_axis })}
                />{' '}
                mirror
              </label>
              <button
                className="danger"
                type="button"
                onClick={(e) => {
                  e.stopPropagation()
                  onDelete(i)
                }}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}

      {/* Requirements doc §3: an explicit, non-automatic command -- never a live mode. */}
      <button type="button" disabled={!canMirror} onClick={onMirrorSelection} title={canMirror ? '' : 'Select a platform and flag one guide as the mirror axis first'}>
        Mirror selection across axis
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
  const [selectedGuide, setSelectedGuide] = useState<number | null>(null)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')

  // Requirements doc 1.3: visibility and snap-enable are global, session-level toggles, not
  // per-level saved data (only the guides THEMSELVES are level data -- see api.ts's own Guide
  // type and its "authoring metadata" comment).
  const [showGuides, setShowGuides] = useState(true)
  const [snapEnabled, setSnapEnabled] = useState(false)
  const [snapThresholdPx, setSnapThresholdPx] = useState(DEFAULT_SNAP_THRESHOLD_PX)

  const load = useCallback(async (id: number) => {
    const lvl = await levels.get(id)
    setDraft({ name: lvl.name, width: lvl.width, height: lvl.height, platforms: lvl.platforms, guides: lvl.guides ?? [] })
    setActiveId(id)
    setSelected(null)
    setSelectedGuide(null)
    setDirty(false)
    setError(null)
  }, [])

  const startNew = () => {
    setDraft(newDefaultLevel())
    setActiveId(null)
    setSelected(0)
    setSelectedGuide(null)
    setDirty(true)
    setError(null)
  }

  const setPlatforms = (platforms: Platform[]) => {
    setDraft((d) => ({ ...d, platforms }))
    setDirty(true)
  }

  const setGuides = (guides: Guide[]) => {
    setDraft((d) => ({ ...d, guides }))
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

  const createGuideFromRuler = (axis: 'horizontal' | 'vertical', coord: number) => {
    const next = [...draft.guides, { axis, coord, locked: false, is_mirror_axis: false }]
    setGuides(next)
    setSelectedGuide(next.length - 1)
  }

  const createGuideFromEdge = (edge: 'left' | 'right' | 'top' | 'bottom') => {
    if (selected === null || !draft.platforms[selected]) return
    const p = draft.platforms[selected]
    const axis: 'horizontal' | 'vertical' = edge === 'left' || edge === 'right' ? 'vertical' : 'horizontal'
    const coord =
      edge === 'left' ? p.x - p.w / 2 : edge === 'right' ? p.x + p.w / 2 : edge === 'top' ? p.y + p.h / 2 : p.y - p.h / 2
    const next = [...draft.guides, { axis, coord, locked: false, is_mirror_axis: false }]
    setGuides(next)
    setSelectedGuide(next.length - 1)
  }

  const updateGuide = (i: number, g: Guide) => {
    // Requirements doc §3: "One guide per level may be flagged as the mirror axis" -- enforced
    // client-side too (not just the Go store's own validateGuides) so the UI never shows a
    // momentarily-invalid two-mirror state.
    let next = draft.guides.slice()
    if (g.is_mirror_axis) {
      next = next.map((og, oi) => (oi === i ? og : { ...og, is_mirror_axis: false }))
    }
    next[i] = g
    setGuides(next)
  }

  const deleteGuide = (i: number) => {
    setGuides(draft.guides.filter((_, gi) => gi !== i))
    if (selectedGuide === i) setSelectedGuide(null)
  }

  const mirrorAxisGuide = draft.guides.find((g) => g.is_mirror_axis) ?? null

  const mirrorSelection = () => {
    if (selected === null || !mirrorAxisGuide || !draft.platforms[selected]) return
    const p = draft.platforms[selected]
    const reflected: Platform =
      mirrorAxisGuide.axis === 'vertical'
        ? { ...p, x: 2 * mirrorAxisGuide.coord - p.x }
        : { ...p, y: 2 * mirrorAxisGuide.coord - p.y }
    const next = [...draft.platforms, reflected]
    setPlatforms(next)
    setSelected(next.length - 1)
  }

  const save = async () => {
    setError(null)
    try {
      let id = activeId
      if (id === null) {
        if (!draft.name) {
          setError('Name is required to create a new level.')
          return
        }
        const created = await levels.create(draft.name, draft.width, draft.height, draft.platforms)
        id = created.id
        setActiveId(id)
      } else {
        await levels.save(id, draft.width, draft.height, draft.platforms)
      }
      // Guides save via their own real, separate call (LevelStore.SaveGuides) -- see api.ts.
      await levels.saveGuides(id, draft.guides)
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
            <CanvasArea
              width={draft.width}
              height={draft.height}
              platforms={draft.platforms}
              guides={draft.guides}
              selected={selected}
              selectedGuide={selectedGuide}
              showGuides={showGuides}
              snapEnabled={snapEnabled}
              snapThresholdPx={snapThresholdPx}
              onSelect={(i) => {
                setSelected(i)
                if (i !== null) setSelectedGuide(null)
              }}
              onSelectGuide={setSelectedGuide}
              onChange={setPlatforms}
              onChangeGuides={setGuides}
              onCreateGuide={createGuideFromRuler}
            />
            <p className="hint">
              Drag a platform to move it, drag its bottom-right corner to resize. Click empty space to deselect. Drag
              from a ruler to add a guide; hold Alt to move a platform without snapping.
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
                onCreateGuideFromEdge={createGuideFromEdge}
              />
            ) : (
              <p className="hint">Select a platform to edit its exact position/size, or add a new one.</p>
            )}
            <GuideInspector
              guides={draft.guides}
              selected={selectedGuide}
              showGuides={showGuides}
              snapEnabled={snapEnabled}
              snapThresholdPx={snapThresholdPx}
              canMirror={selected !== null && mirrorAxisGuide !== null}
              onSelect={setSelectedGuide}
              onUpdate={updateGuide}
              onDelete={deleteGuide}
              onToggleShowGuides={() => setShowGuides((v) => !v)}
              onToggleSnapEnabled={() => setSnapEnabled((v) => !v)}
              onChangeThreshold={setSnapThresholdPx}
              onMirrorSelection={mirrorSelection}
            />
          </div>
        </div>
      </main>
    </div>
  )
}
