import { useCallback, useEffect, useState } from 'react'
import { animations, api, doorScripts, generateProcedural, shankpitSprays, textures, type AnimationSummary, type DoorScriptSummary, type Layer, type Project, type TextureSummary } from './api'
import LevelEditor from './LevelEditor'
import ShankpitLevelEditor from './ShankpitLevelEditor'
import AiOpponents from './AiOpponents'
import ShankpitAiOpponents from './ShankpitAiOpponents'
import Sprays from './Sprays'
import './App.css'

// NOCK — real v0 editor UI (founder real-time, 2026-09-12: "we want the tool similar in shape
// cli and gui"). Every action here calls the exact same /admin/nock/api routes cmd/nock's own
// CLI subcommands hit — no client-side compositing logic lives in this file; the preview image
// is literally the server's own real flattened export, refetched after each change.

function useProjects() {
  const [projects, setProjects] = useState<string[]>([])
  const refresh = useCallback(() => {
    api.listProjects().then(setProjects).catch(() => setProjects([]))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])
  return { projects, refresh }
}

function NewProjectForm({ onCreated }: { onCreated: (name: string) => void }) {
  const [name, setName] = useState('')
  const [width, setWidth] = useState(512)
  const [height, setHeight] = useState(512)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    try {
      const p = await api.createProject(name, width, height)
      onCreated(p.name)
      setName('')
    } catch (err) {
      setError(String(err))
    }
  }

  return (
    <form className="new-project-form" onSubmit={submit}>
      <input placeholder="new project name" value={name} onChange={(e) => setName(e.target.value)} />
      <input
        type="number"
        value={width}
        min={1}
        onChange={(e) => setWidth(Number(e.target.value))}
        aria-label="width"
      />
      <span>×</span>
      <input
        type="number"
        value={height}
        min={1}
        onChange={(e) => setHeight(Number(e.target.value))}
        aria-label="height"
      />
      <button type="submit" disabled={!name}>
        Create
      </button>
      {error && <span className="error">{error}</span>}
    </form>
  )
}

function GradientForm({ project, onChanged }: { project: string; onChanged: () => void }) {
  const [name, setName] = useState('gradient')
  const [from, setFrom] = useState('#000033')
  const [to, setTo] = useState('#3399ff')
  const [direction, setDirection] = useState<'vertical' | 'horizontal'>('vertical')

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    await api.addGradient(project, name, from, to, direction)
    onChanged()
  }

  return (
    <form className="gradient-form" onSubmit={submit}>
      <h3>Gradient layer</h3>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="layer name" />
      <label>
        From <input type="color" value={from} onChange={(e) => setFrom(e.target.value)} />
      </label>
      <label>
        To <input type="color" value={to} onChange={(e) => setTo(e.target.value)} />
      </label>
      <select value={direction} onChange={(e) => setDirection(e.target.value as 'vertical' | 'horizontal')}>
        <option value="vertical">Vertical</option>
        <option value="horizontal">Horizontal</option>
      </select>
      <button type="submit">Add gradient layer</button>
    </form>
  )
}

function ProceduralTextureForm({ project, onChanged }: { project: string; onChanged: (newLayerName?: string) => void }) {
  const [name, setName] = useState('procedural')
  const [prompt, setPrompt] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [failedSource, setFailedSource] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setFailedSource(null)
    try {
      const result = await generateProcedural(project, name, prompt)
      if (result.ok) {
        onChanged(name)
        setPrompt('')
      } else {
        setError(result.error)
        setFailedSource(result.source)
      }
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="procedural-form" onSubmit={submit}>
      <h3>Generate procedural texture (PARENA + Vertex AI)</h3>
      <input placeholder="layer name" value={name} onChange={(e) => setName(e.target.value)} />
      <textarea
        placeholder="describe the texture, e.g. 'a weathered brick wall' or 'cracked desert mud'"
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        rows={2}
      />
      <button type="submit" disabled={!prompt || busy}>
        {busy ? 'Generating…' : 'Generate'}
      </button>
      {error && (
        <div className="error">
          <p>{error}</p>
          {failedSource && (
            <p className="hint">
              The model's own generated source didn't validate or compile -- open it from the layer panel
              after adding it by hand via the CLI (`nock proc-add`) if you want to fix it up, or just try
              generating again.
            </p>
          )}
        </div>
      )}
      <p className="hint">
        Compiles to PARENA's Java target (never C) specifically so generated code can't inject raw system
        calls -- see docs/NOCK_NORTHSTAR.md. A model sometimes writes source that doesn't compile in this
        restricted language subset; that's a real, expected outcome, not a bug.
      </p>
    </form>
  )
}

function ProceduralSourceEditor({
  project,
  layerName,
  onChanged,
}: {
  project: string
  layerName: string
  onChanged: () => void
}) {
  const [source, setSource] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setSource(null)
    setError(null)
    api.getProceduralSource(project, layerName).then((r) => setSource(r.source))
  }, [project, layerName])

  if (source === null) return <p className="hint">Loading source…</p>

  const rerun = async () => {
    setBusy(true)
    setError(null)
    try {
      await api.regenerateProcedural(project, layerName, source)
      onChanged()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="procedural-editor">
      <h3>Generating source: {layerName}.prn</h3>
      <textarea className="source-textarea" value={source} onChange={(e) => setSource(e.target.value)} rows={12} spellCheck={false} />
      <button onClick={rerun} disabled={busy}>
        {busy ? 'Re-running…' : 'Re-run'}
      </button>
      {error && <p className="error">{error}</p>}
      <p className="hint">Editing here doesn't save until you hit Re-run -- a failed re-run leaves the layer's current render untouched.</p>
    </div>
  )
}

function LayerRow({
  project,
  layerName,
  opacity,
  visible,
  hasMask,
  onChanged,
  selected,
  onSelect,
}: {
  project: string
  layerName: string
  opacity: number
  visible: boolean
  hasMask: boolean
  onChanged: () => void
  selected: boolean
  onSelect: () => void
}) {
  const maskInputId = `mask-upload-${layerName}`

  return (
    <div className={`layer-row${selected ? ' selected' : ''}`} onClick={onSelect}>
      <input
        type="checkbox"
        checked={visible}
        onChange={async (e) => {
          e.stopPropagation()
          await api.setVisible(project, layerName, e.target.checked)
          onChanged()
        }}
        title="visible"
      />
      <span className="layer-name">{layerName}</span>
      <input
        type="range"
        min={0}
        max={100}
        value={opacity}
        onClick={(e) => e.stopPropagation()}
        onChange={async (e) => {
          await api.setOpacity(project, layerName, Number(e.target.value))
          onChanged()
        }}
      />
      <span className="opacity-value">{opacity}%</span>
      <button
        onClick={async (e) => {
          e.stopPropagation()
          await api.moveLayer(project, layerName, 1)
          onChanged()
        }}
        title="move up"
      >
        ↑
      </button>
      <button
        onClick={async (e) => {
          e.stopPropagation()
          await api.moveLayer(project, layerName, -1)
          onChanged()
        }}
        title="move down"
      >
        ↓
      </button>
      <label className="mask-btn" title={hasMask ? 'replace mask' : 'add mask'} onClick={(e) => e.stopPropagation()}>
        {hasMask ? '🎭' : '➕🎭'}
        <input
          id={maskInputId}
          type="file"
          accept="image/*"
          hidden
          onChange={async (e) => {
            const file = e.target.files?.[0]
            if (!file) return
            await api.setMask(project, layerName, file)
            onChanged()
          }}
        />
      </label>
      {hasMask && (
        <button
          onClick={async (e) => {
            e.stopPropagation()
            await api.clearMask(project, layerName)
            onChanged()
          }}
          title="clear mask"
        >
          ✕🎭
        </button>
      )}
      <button
        className="danger"
        onClick={async (e) => {
          e.stopPropagation()
          await api.removeLayer(project, layerName)
          onChanged()
        }}
        title="delete layer"
      >
        🗑
      </button>
    </div>
  )
}

function LayerEffectsPanel({
  project,
  layerName,
  onChanged,
}: {
  project: string
  layerName: string
  onChanged: () => void
}) {
  const [brightness, setBrightness] = useState(100)
  const [saturation, setSaturation] = useState(100)
  const [hue, setHue] = useState(100)
  const [radius, setRadius] = useState(0)
  const [sigma, setSigma] = useState(1)
  const [amount, setAmount] = useState(1)

  return (
    <div className="effects-panel">
      <h3>Adjust: {layerName}</h3>
      <div className="effect-row">
        <label>Brightness {brightness}%</label>
        <input type="range" min={0} max={200} value={brightness} onChange={(e) => setBrightness(Number(e.target.value))} />
        <label>Saturation {saturation}%</label>
        <input type="range" min={0} max={200} value={saturation} onChange={(e) => setSaturation(Number(e.target.value))} />
        <label>Hue {hue}%</label>
        <input type="range" min={0} max={200} value={hue} onChange={(e) => setHue(Number(e.target.value))} />
        <button
          onClick={async () => {
            await api.hueSat(project, layerName, brightness, saturation, hue)
            onChanged()
          }}
        >
          Apply hue/saturation
        </button>
      </div>
      <div className="effect-row">
        <label>Radius {radius}</label>
        <input type="range" min={0} max={10} step={0.5} value={radius} onChange={(e) => setRadius(Number(e.target.value))} />
        <label>Sigma {sigma}</label>
        <input type="range" min={0.1} max={5} step={0.1} value={sigma} onChange={(e) => setSigma(Number(e.target.value))} />
        <label>Amount {amount}</label>
        <input type="range" min={0} max={5} step={0.1} value={amount} onChange={(e) => setAmount(Number(e.target.value))} />
        <button
          onClick={async () => {
            await api.sharpen(project, layerName, radius, sigma, amount)
            onChanged()
          }}
        >
          Apply sharpen
        </button>
      </div>
      <p className="hint">Non-destructive (S416-04) — re-tuning always starts from the original layer, never from an already-adjusted one.</p>
    </div>
  )
}

// LayerTransformPanel -- S416-02, "the biggest real gap between NOCK v0 and an actual
// Photoshop-shaped tool -- every layer is forced full-canvas today." Same real "local draft
// state, Apply button commits it" pattern LayerEffectsPanel above already established for
// hue/sat/sharpen -- position/scale/rotation are real, destructive (baked into the manifest
// immediately) adjustments too, not a live drag-handle overlay (a real, separate, larger
// interaction-model addition, not attempted here).
function LayerTransformPanel({
  project,
  layerName,
  layer,
  onChanged,
}: {
  project: string
  layerName: string
  layer: Layer
  onChanged: () => void
}) {
  const [x, setX] = useState(layer.x ?? 0)
  const [y, setY] = useState(layer.y ?? 0)
  const [scale, setScale] = useState(layer.scale && layer.scale > 0 ? layer.scale : 100)
  const [rotation, setRotation] = useState(layer.rotation ?? 0)

  // Re-sync local draft state whenever the selected layer itself changes (switching layers
  // shouldn't carry over the PREVIOUS layer's unsaved draft values) -- same real reason
  // LayerEffectsPanel's own sibling would need this if it read initial values from props instead
  // of a fixed default; this panel's initial values genuinely depend on which real layer is
  // selected, so this effect is the real, necessary piece that one didn't need.
  useEffect(() => {
    setX(layer.x ?? 0)
    setY(layer.y ?? 0)
    setScale(layer.scale && layer.scale > 0 ? layer.scale : 100)
    setRotation(layer.rotation ?? 0)
  }, [layerName, layer.x, layer.y, layer.scale, layer.rotation])

  return (
    <div className="effects-panel">
      <h3>Transform: {layerName}</h3>
      <div className="effect-row">
        <label>X {x}px</label>
        <input type="number" value={x} onChange={(e) => setX(Number(e.target.value))} />
        <label>Y {y}px</label>
        <input type="number" value={y} onChange={(e) => setY(Number(e.target.value))} />
      </div>
      <div className="effect-row">
        <label>Scale {scale}%</label>
        <input type="range" min={1} max={400} value={scale} onChange={(e) => setScale(Number(e.target.value))} />
        <label>Rotation {rotation}°</label>
        <input type="range" min={0} max={359} value={rotation} onChange={(e) => setRotation(Number(e.target.value))} />
        <button
          onClick={async () => {
            await api.setTransform(project, layerName, x, y, scale, rotation)
            onChanged()
          }}
        >
          Apply transform
        </button>
        <button
          onClick={async () => {
            setX(0); setY(0); setScale(100); setRotation(0)
            await api.setTransform(project, layerName, 0, 0, 100, 0)
            onChanged()
          }}
        >
          Reset
        </button>
      </div>
      <p className="hint">Destructive (baked into the layer immediately) — undo isn't real yet.</p>
    </div>
  )
}

function ProjectEditor({ name, onDeleted }: { name: string; onDeleted: () => void }) {
  const [project, setProject] = useState<Project | null>(null)
  const [selectedLayer, setSelectedLayer] = useState<string | null>(null)
  const [previewNonce, setPreviewNonce] = useState(0)

  const refresh = useCallback(() => {
    api.getProject(name).then(setProject)
    setPreviewNonce((n) => n + 1)
  }, [name])

  useEffect(() => {
    refresh()
  }, [refresh])

  if (!project) return <p>Loading…</p>

  const selectedLayerObj = project.layers.find((l) => l.name === selectedLayer)

  return (
    <div className="project-editor">
      <div className="project-header">
        <h2>
          {project.name} <span className="dims">{project.width}×{project.height}</span>
        </h2>
        <button
          className="danger"
          onClick={async () => {
            if (!confirm(`Delete project "${project.name}"? This can't be undone.`)) return
            await api.deleteProject(project.name)
            onDeleted()
          }}
        >
          Delete project
        </button>
      </div>

      <div className="editor-body">
        <div className="canvas-pane">
          <img
            className="preview"
            key={previewNonce}
            src={api.exportUrl(project.name) + `&t=${previewNonce}`}
            alt={`${project.name} composite preview`}
          />
          <div className="export-links">
            <a href={api.exportUrl(project.name, 'png')} download={`${project.name}.png`}>
              Export PNG
            </a>
            <a href={api.exportUrl(project.name, 'jpg')} download={`${project.name}.jpg`}>
              Export JPG
            </a>
            <button
              type="button"
              onClick={async () => {
                const sprayName = prompt(`Spray name`, project.name)
                if (!sprayName) return
                try {
                  await shankpitSprays.exportProjectToSpray(project.name, project.width, project.height, sprayName)
                  alert(`Exported "${sprayName}" to the sprays registry.`)
                } catch (err) {
                  alert(`Export to spray failed: ${String(err)}`)
                }
              }}
            >
              Export to Spray
            </button>
          </div>
        </div>

        <div className="side-pane">
          <h3>Layers ({project.layers.length})</h3>
          <div className="layer-list">
            {[...project.layers].reverse().map((l) => (
              <LayerRow
                key={l.name}
                project={project.name}
                layerName={l.name}
                opacity={l.opacity}
                visible={l.visible}
                hasMask={!!l.mask}
                selected={selectedLayer === l.name}
                onSelect={() => setSelectedLayer(l.name)}
                onChanged={refresh}
              />
            ))}
            {project.layers.length === 0 && <p className="hint">No layers yet — add one below.</p>}
          </div>

          <AddLayerForm project={project.name} onChanged={refresh} />
          <GradientForm project={project.name} onChanged={refresh} />
          <ProceduralTextureForm
            project={project.name}
            onChanged={(newLayerName) => {
              refresh()
              if (newLayerName) setSelectedLayer(newLayerName)
            }}
          />

          {selectedLayer && selectedLayerObj?.source && (
            <ProceduralSourceEditor project={project.name} layerName={selectedLayer} onChanged={refresh} />
          )}
          {selectedLayer && selectedLayerObj && (
            <LayerTransformPanel project={project.name} layerName={selectedLayer} layer={selectedLayerObj} onChanged={refresh} />
          )}
          {selectedLayer && (
            <LayerEffectsPanel project={project.name} layerName={selectedLayer} onChanged={refresh} />
          )}
        </div>
      </div>
    </div>
  )
}

function AddLayerForm({ project, onChanged }: { project: string; onChanged: () => void }) {
  const [name, setName] = useState('')

  return (
    <form
      className="add-layer-form"
      onSubmit={(e) => e.preventDefault()}
    >
      <h3>Add layer from image</h3>
      <input placeholder="layer name" value={name} onChange={(e) => setName(e.target.value)} />
      <input
        type="file"
        accept="image/*"
        disabled={!name}
        onChange={async (e) => {
          const file = e.target.files?.[0]
          if (!file || !name) return
          await api.addLayer(project, name, file)
          setName('')
          onChanged()
        }}
      />
    </form>
  )
}

// TextureLibraryGenerateForm is the texture-library equivalent of ProceduralTextureForm above,
// hitting textures.generate (a standalone Texture row) instead of a Project's own .../generate
// (which adds a Project layer). Two separate real call sites into the same underlying Vertex
// pipeline, on purpose -- Project/Layer and the Texture library are two real, currently
// independent concepts (see internal/nock/texture_store.go's own header comment).
function TextureLibraryGenerateForm({ onChanged }: { onChanged: (newId?: number) => void }) {
  const [name, setName] = useState('')
  const [prompt, setPrompt] = useState('')
  const [width, setWidth] = useState(256)
  const [height, setHeight] = useState(256)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const result = await textures.generate(name, prompt, width, height)
      if (result.ok) {
        setName('')
        setPrompt('')
        onChanged(result.texture.id)
      } else {
        setError(result.error)
      }
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="procedural-form" onSubmit={submit}>
      <h3>Generate a new master texture</h3>
      <input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
      <textarea placeholder="describe the texture" value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={2} />
      <div className="new-project-form">
        <input type="number" value={width} min={1} onChange={(e) => setWidth(Number(e.target.value))} aria-label="width" />
        <span>×</span>
        <input type="number" value={height} min={1} onChange={(e) => setHeight(Number(e.target.value))} aria-label="height" />
      </div>
      <button type="submit" disabled={!name || !prompt || busy}>
        {busy ? 'Generating…' : 'Generate'}
      </button>
      {error && <p className="error">{error}</p>}
    </form>
  )
}

// PARENA_STARTER_SOURCE is the blank-slate default dropped into TextureFromSourceForm's own
// textarea -- the exact real contract procgen.go's own validateProcTextureSource enforces
// (module gentexture / import math / pixel-r,g,b taking x,y,w,h), filled in with a trivial but
// real, non-degenerate gradient so "Create" works immediately without editing anything first.
const PARENA_STARTER_SOURCE = `(module gentexture)
(import math)

(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 (/ x w))
(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 (/ y h))
(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 0.5)
`

// TextureFromSourceForm is the blank-slate PARENA editor (founder real-time, 2026-09-16: "add a
// parena frontend to nock texture generator" -> "blank-slate PARENA editor") -- no Vertex AI
// prompt involved, hits textures.createFromSource directly against the same real
// CreateProceduralTexture/procgen.go compile pipeline TextureLibraryRow's own "Edit source" ->
// "Re-run" flow already uses for AI-generated textures. That existing flow only ever starts from
// AI output; this is the missing "start from nothing" counterpart, same real backend either way.
function TextureFromSourceForm({ onChanged }: { onChanged: (newId?: number) => void }) {
  const [name, setName] = useState('')
  const [width, setWidth] = useState(256)
  const [height, setHeight] = useState(256)
  const [source, setSource] = useState(PARENA_STARTER_SOURCE)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const t = await textures.createFromSource(name, width, height, source)
      setName('')
      setSource(PARENA_STARTER_SOURCE)
      onChanged(t.id)
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="procedural-form" onSubmit={submit}>
      <h3>Write a texture in PARENA</h3>
      <p className="hint">
        No AI involved -- hand-write the <code>pixel-r</code>/<code>pixel-g</code>/<code>pixel-b</code> functions directly. Compiled
        and rendered server-side, same sandboxed Java target the AI-generated textures use.
      </p>
      <input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
      <div className="new-project-form">
        <input type="number" value={width} min={1} onChange={(e) => setWidth(Number(e.target.value))} aria-label="width" />
        <span>×</span>
        <input type="number" value={height} min={1} onChange={(e) => setHeight(Number(e.target.value))} aria-label="height" />
      </div>
      <textarea className="source-textarea" value={source} onChange={(e) => setSource(e.target.value)} rows={10} spellCheck={false} />
      <button type="submit" disabled={!name || !source || busy}>
        {busy ? 'Compiling…' : 'Create'}
      </button>
      {error && <p className="error">{error}</p>}
    </form>
  )
}

function TextureLibraryRow({ t, onChanged }: { t: TextureSummary; onChanged: () => void }) {
  const [expanded, setExpanded] = useState(false)
  const [source, setSource] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const loadSource = async () => {
    if (!t.has_source) return
    const full = await textures.get(t.id)
    setSource(full.parena_source ?? '')
    setExpanded(true)
  }

  const rerun = async () => {
    if (source === null) return
    setBusy(true)
    setError(null)
    try {
      await textures.regenerate(t.id, source)
      onChanged()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="texture-card">
      <img className="texture-thumb" src={textures.imageUrl(t.id)} alt={t.name} />
      <div className="texture-meta">
        <strong>{t.name}</strong>
        <span className="hint">
          {t.width}×{t.height} {t.has_source && '· generated'}
        </span>
        {t.prompt && <span className="hint">“{t.prompt}”</span>}
        <div className="texture-actions">
          <button
            onClick={async () => {
              const name = prompt('New, independent texture name for the clone:', `${t.name}-copy`)
              if (!name) return
              await textures.clone(t.id, name)
              onChanged()
            }}
          >
            Clone
          </button>
          <button
            onClick={async () => {
              const name = window.prompt('Rename to:', t.name)
              if (!name) return
              await textures.rename(t.id, name)
              onChanged()
            }}
          >
            Rename
          </button>
          {t.has_source && <button onClick={loadSource}>{expanded ? 'Hide source' : 'Edit source'}</button>}
          <button
            className="danger"
            onClick={async () => {
              if (!confirm(`Delete texture "${t.name}"? This can't be undone.`)) return
              await textures.delete(t.id)
              onChanged()
            }}
          >
            Delete
          </button>
        </div>
        {expanded && source !== null && (
          <div className="procedural-editor">
            <textarea className="source-textarea" value={source} onChange={(e) => setSource(e.target.value)} rows={8} spellCheck={false} />
            <button onClick={rerun} disabled={busy}>
              {busy ? 'Re-running…' : 'Re-run'}
            </button>
            {error && <p className="error">{error}</p>}
          </div>
        )}
      </div>
    </div>
  )
}

function TextureLibrary() {
  const [list, setList] = useState<TextureSummary[]>([])
  const refresh = useCallback(() => {
    textures.list().then(setList)
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])

  return (
    <div className="texture-library">
      <TextureLibraryGenerateForm onChanged={refresh} />
      <TextureFromSourceForm onChanged={refresh} />
      <div className="texture-grid">
        {list.map((t) => (
          <TextureLibraryRow key={t.id} t={t} onChanged={refresh} />
        ))}
        {list.length === 0 && <p className="hint">No textures yet — generate one above.</p>}
      </div>
    </div>
  )
}

// DOOR_SCRIPT_STARTER_SOURCE is the real, working starter template dropped into DoorScripts' own
// blank-slate textarea -- the exact real door-tick contract (SHANKPIT/docs/
// STORY_SYSTEM_NORTHSTAR.md Part 2) IDUNA's own compile pipeline (internal/nock/
// door_script_compile.go) validates against, filled in with a real, non-trivial hysteresis
// pattern (same shape examples/story-doors/door_tick.prn already uses) so "Create" compiles
// unedited.
const DOOR_SCRIPT_STARTER_SOURCE = `(module doorscript)
(import math)

(defn door-tick [(dist-to-player : F64) (state : F64)] : F64
  (if (< dist-to-player 3.0)
    1.0
    (if (> dist-to-player 5.0)
      0.0
      state)))
`

// DoorScripts is the real NOCK authoring surface for SHANKPIT Story System door scripts
// (S459-81/82, founder real-time: "fill the gap in the designer can't write scripts"). Write
// real PARENA source, IDUNA compiles it server-side (parena build + gcc -shared, internal/nock/
// door_script_compile.go) into a real, downloadable .so -- no local toolchain needed. Copy the
// shown Script URL straight into a level's own "doors" array (`script_url` field,
// packages/world/level_boxes.h) and SHANKPIT's server downloads + caches it automatically at
// level load (packages/world/story_doors.h).
function DoorScripts() {
  const [list, setList] = useState<DoorScriptSummary[]>([])
  const [name, setName] = useState('')
  const [source, setSource] = useState(DOOR_SCRIPT_STARTER_SOURCE)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(() => {
    doorScripts.list().then(setList)
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await doorScripts.create(name, source)
      setName('')
      setSource(DOOR_SCRIPT_STARTER_SOURCE)
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="animation-repository">
      <h3>Write a door script (PARENA)</h3>
      <p className="hint">
        Real PARENA source, compiled server-side into a real, downloadable <code>.so</code> -- no local <code>parena</code>/<code>gcc</code>{' '}
        needed. The one real contract: <code>(defn door-tick [(dist-to-player : F64) (state : F64)] : F64 ...)</code>, returning the
        door's own new state (0.0 = closed, 1.0 = open).
      </p>
      <form className="procedural-form" onSubmit={submit}>
        <input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
        <textarea className="source-textarea" value={source} onChange={(e) => setSource(e.target.value)} rows={10} spellCheck={false} />
        <button type="submit" disabled={!name || !source || busy}>
          {busy ? 'Compiling…' : 'Create'}
        </button>
        {error && <p className="error">{error}</p>}
      </form>

      <div className="animation-list">
        {list.map((d) => (
          <DoorScriptRow key={d.id} d={d} onChanged={refresh} />
        ))}
        {list.length === 0 && <p className="hint">No door scripts yet — write one above.</p>}
      </div>
    </div>
  )
}

function DoorScriptRow({ d, onChanged }: { d: DoorScriptSummary; onChanged: () => void }) {
  const [expanded, setExpanded] = useState(false)
  const [source, setSource] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const loadSource = async () => {
    const full = await doorScripts.get(d.id)
    setSource(full.parena_source)
    setExpanded(true)
  }

  const rerun = async () => {
    if (source === null) return
    setBusy(true)
    setError(null)
    try {
      await doorScripts.regenerate(d.id, source)
      onChanged()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="animation-card">
      <strong>{d.name}</strong>
      <span className="hint">script_url: {doorScripts.downloadUrl(d.id)}</span>
      <div className="animation-actions">
        <button
          onClick={() => {
            navigator.clipboard?.writeText(window.location.origin + doorScripts.downloadUrl(d.id))
          }}
        >
          Copy script_url
        </button>
        <button onClick={loadSource}>{expanded ? 'Hide source' : 'Edit source'}</button>
        <button
          className="danger"
          onClick={async () => {
            if (!confirm(`Delete door script "${d.name}"? This can't be undone.`)) return
            await doorScripts.delete(d.id)
            onChanged()
          }}
        >
          Delete
        </button>
      </div>
      {expanded && source !== null && (
        <div className="procedural-editor">
          <textarea className="source-textarea" value={source} onChange={(e) => setSource(e.target.value)} rows={8} spellCheck={false} />
          <button onClick={rerun} disabled={busy}>
            {busy ? 'Re-compiling…' : 'Re-compile'}
          </button>
          {error && <p className="error">{error}</p>}
        </div>
      )}
    </div>
  )
}

// Animations is the real NOCK animation repository (founder real-time, 2026-09-16: "need
// animation repository" -> "ok I need to import quaternion assets nock tools drag and drop").
// A drag-and-drop zone for a raw .glb/.gltf file straight from Blender's own glTF export --
// converted to real GOLDENBAND .gband/.gskel/.gmesh assets server-side (see
// IDUNA/internal/nock/gltf_convert.go), no local gbtool run required. Uploading via gbtool's own
// CLI first (for a .gltf+external .bin pair, which can't be converted from one dropped file) is
// still real and still works -- POST /admin/nock/api/animations itself, not built as a form here
// yet since drag-and-drop covers Blender's own default "glTF Binary (.glb)" export already.
function Animations() {
  const [list, setList] = useState<AnimationSummary[]>([])
  const [dragOver, setDragOver] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pendingName, setPendingName] = useState('')
  // attachOpenId/attachSourceId (2026-09-17): which row's own "attach animation" panel is open,
  // and which existing library row is selected as the source -- see attachFromExisting/
  // attachFromFile below, the real affordance this component's own copy already promised
  // ("you can add animations to it later, either by uploading a separate file with the same
  // rig") but didn't build until now.
  const [attachOpenId, setAttachOpenId] = useState<number | null>(null)
  const [attachSourceId, setAttachSourceId] = useState<number | ''>('')
  // filter (2026-09-17, founder real-time: "i dont see the animations browser or rig browser or
  // anything like that") -- this one tab already covers meshes/rigs/animations together (a row
  // is really "a GOLDENBAND character asset," any real combination of the three), but nothing
  // let you browse just one kind. These filter views are that -- a real "rig browser"/"animation
  // browser" as a view over the same data, not a separate page.
  const [filter, setFilter] = useState<'all' | 'mesh' | 'rig' | 'animated' | 'needs-animation'>('all')
  const filteredList = list.filter((a) => {
    switch (filter) {
      case 'mesh': return a.has_mesh
      case 'rig': return a.has_skel
      case 'animated': return a.has_animation
      case 'needs-animation': return !a.has_animation
      default: return true
    }
  })

  const refresh = useCallback(() => {
    animations.list().then(setList)
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])

  const doImport = async (file: File) => {
    setBusy(true)
    setError(null)
    try {
      const name = pendingName.trim() || file.name.replace(/\.(glb|gltf)$/i, '')
      await animations.importGLTF(file, name)
      setPendingName('')
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const attachFromExisting = async (targetId: number) => {
    if (attachSourceId === '') return
    setBusy(true)
    setError(null)
    try {
      await animations.attachAnimationFromExisting(targetId, attachSourceId)
      setAttachOpenId(null)
      setAttachSourceId('')
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const attachFromFile = async (targetId: number, file: File) => {
    setBusy(true)
    setError(null)
    try {
      await animations.attachAnimationFromFile(targetId, file)
      setAttachOpenId(null)
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="animation-repository">
      <h3>Character library</h3>
      <p className="hint">
        Drop in a character from Blender, Mixamo, or anywhere else that exports <code>.glb</code>/<code>.gltf</code>. Any of these is fine on
        its own -- you don't need all three:
      </p>
      <ul className="hint">
        <li><strong>Mesh</strong> -- the 3D shape (what it looks like).</li>
        <li><strong>Rig / skeleton</strong> -- the bones inside it that let it bend and pose.</li>
        <li><strong>Animation</strong> -- a recorded motion using that rig (a walk, a wave, etc).</li>
      </ul>
      <p className="hint">
        A rigged mesh with no animation yet -- like a mannequin standing in T-pose -- imports just fine; you can add
        animations to it later, either by uploading a separate file with the same rig or by animating it right here
        once NOCK's rigging/animation tools land.
      </p>
      <input
        placeholder="name (defaults to the file name)"
        value={pendingName}
        onChange={(e) => setPendingName(e.target.value)}
      />
      <label
        className={`dropzone${dragOver ? ' drag-over' : ''}${busy ? ' busy' : ''}`}
        onDragOver={(e) => {
          e.preventDefault()
          setDragOver(true)
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault()
          setDragOver(false)
          const file = e.dataTransfer.files[0]
          if (file) doImport(file)
        }}
      >
        {busy ? 'Importing…' : 'Drop a .glb/.gltf file here, or click to browse'}
        <input
          type="file"
          accept=".glb,.gltf"
          style={{ display: 'none' }}
          onChange={(e) => {
            const file = e.target.files?.[0]
            if (file) doImport(file)
            e.target.value = ''
          }}
        />
      </label>
      {error && <p className="error">{error}</p>}

      <nav className="animation-filter-tabs">
        <button className={filter === 'all' ? 'active' : ''} onClick={() => setFilter('all')}>
          All ({list.length})
        </button>
        <button className={filter === 'mesh' ? 'active' : ''} onClick={() => setFilter('mesh')}>
          Meshes ({list.filter((a) => a.has_mesh).length})
        </button>
        <button className={filter === 'rig' ? 'active' : ''} onClick={() => setFilter('rig')}>
          Rigs ({list.filter((a) => a.has_skel).length})
        </button>
        <button className={filter === 'animated' ? 'active' : ''} onClick={() => setFilter('animated')}>
          Animations ({list.filter((a) => a.has_animation).length})
        </button>
        <button className={filter === 'needs-animation' ? 'active' : ''} onClick={() => setFilter('needs-animation')}>
          Needs animation ({list.filter((a) => !a.has_animation).length})
        </button>
      </nav>

      <div className="animation-list">
        {filteredList.map((a) => (
          <div key={a.id} className="animation-card">
            <strong>{a.name}</strong>
            <span className="hint">
              {[
                a.has_mesh && 'mesh',
                a.has_skel && 'rig',
                a.has_animation ? `animation (${a.num_channels} channels · ${a.duration_ticks} ticks @ ${a.tick_rate}/s)` : 'no animation yet',
              ]
                .filter(Boolean)
                .join(' · ')}
            </span>
            {a.source_location && <span className="hint">{a.source_location}</span>}
            <div className="animation-actions">
              {a.has_animation && <a href={animations.downloadUrl(a.id, 'gband')}>.gband</a>}
              {a.has_skel && <a href={animations.downloadUrl(a.id, 'gskel')}>.gskel</a>}
              {a.has_mesh && <a href={animations.downloadUrl(a.id, 'gmesh')}>.gmesh</a>}
              <button
                onClick={async () => {
                  const name = window.prompt('Rename to:', a.name)
                  if (!name) return
                  await animations.rename(a.id, name)
                  refresh()
                }}
              >
                Rename
              </button>
              <button
                onClick={async () => {
                  const name = window.prompt('New, independent name for the clone:', `${a.name}-copy`)
                  if (!name) return
                  await animations.clone(a.id, name)
                  refresh()
                }}
              >
                Clone
              </button>
              <button
                className="danger"
                onClick={async () => {
                  if (!confirm(`Delete animation "${a.name}"? This can't be undone.`)) return
                  await animations.delete(a.id)
                  refresh()
                }}
              >
                Delete
              </button>
              {!a.has_animation && (
                <button
                  onClick={() => {
                    setAttachOpenId(attachOpenId === a.id ? null : a.id)
                    setAttachSourceId('')
                    setError(null)
                  }}
                >
                  {attachOpenId === a.id ? 'Cancel' : 'Attach animation'}
                </button>
              )}
            </div>
            {attachOpenId === a.id && (
              <div className="animation-attach-panel hint">
                <p>
                  Pick a clip that already has animation, or drop a fresh <code>.glb</code>/<code>.gltf</code> file with the motion baked in.
                  {a.skeleton_hash
                    ? ' Only clips sharing this exact rig are checked automatically -- a mismatched rig is rejected, not silently misapplied.'
                    : ' (This row was imported before rig-matching existed -- attaching still works, just without the automatic compatibility check.)'}
                </p>
                <div className="animation-attach-row">
                  <select value={attachSourceId} onChange={(e) => setAttachSourceId(e.target.value ? Number(e.target.value) : '')}>
                    <option value="">-- pick an existing clip --</option>
                    {list
                      .filter((cand) => cand.has_animation && cand.id !== a.id)
                      .map((cand) => (
                        <option key={cand.id} value={cand.id}>
                          {cand.name}
                          {a.skeleton_hash && cand.skeleton_hash
                            ? a.skeleton_hash === cand.skeleton_hash
                              ? ' (same rig)'
                              : ' (different rig -- will be rejected)'
                            : ''}
                        </option>
                      ))}
                  </select>
                  <button disabled={attachSourceId === '' || busy} onClick={() => attachFromExisting(a.id)}>
                    Attach
                  </button>
                </div>
                <div className="animation-attach-row">
                  <span>or upload a file:</span>
                  <input
                    type="file"
                    accept=".glb,.gltf"
                    disabled={busy}
                    onChange={(e) => {
                      const file = e.target.files?.[0]
                      if (file) attachFromFile(a.id, file)
                      e.target.value = ''
                    }}
                  />
                </div>
              </div>
            )}
          </div>
        ))}
        {list.length === 0 && <p className="hint">No animations yet — drop a .glb above.</p>}
        {list.length > 0 && filteredList.length === 0 && <p className="hint">Nothing matches this filter yet.</p>}
      </div>
    </div>
  )
}

type Tab = 'projects' | 'textures' | 'animations' | 'door-scripts' | 'brawlpit' | 'ai-opponents' | 'shankpit' | 'shankpit-ai-opponents' | 'sprays'
const VALID_TABS: Tab[] = ['projects', 'textures', 'animations', 'door-scripts', 'brawlpit', 'ai-opponents', 'shankpit', 'shankpit-ai-opponents', 'sprays']

// Founder real-time: "deep links into that interface url wise? i have to click on it every time
// i reload" -- a real, deep-linkable tab, not just in-memory `useState`. No router dependency
// needed for 4 flat tabs: the URL hash IS the state (`#ai-opponents`), read once on mount and
// kept in sync both ways (tab click -> hash, and browser back/forward -> tab) via `hashchange`.
// An unrecognized/missing hash falls back to the same 'textures' default this always had.
function tabFromHash(): Tab {
  const h = window.location.hash.slice(1)
  return (VALID_TABS as string[]).includes(h) ? (h as Tab) : 'textures'
}

// useTheme -- S459-20, founder real-time: "have a light mode and a dark mode both colorful."
// daisyUI's own two real, stock themes (cupcake/dracula, wired in index.css's own @plugin
// config) already auto-follow the OS's prefers-color-scheme with no JS at all -- this hook adds
// the real, explicit manual override on top (persisted in localStorage), the same real "OS
// default, explicit override wins" contract most theme toggles use. null means "no override yet,
// follow the OS," matching daisyUI's own --default/--prefersdark behavior exactly.
type ThemeOverride = 'cupcake' | 'dracula' | null
function useTheme() {
  const [theme, setThemeState] = useState<ThemeOverride>(() => {
    const stored = localStorage.getItem('nock-theme')
    return stored === 'cupcake' || stored === 'dracula' ? stored : null
  })
  useEffect(() => {
    if (theme) {
      document.documentElement.dataset.theme = theme
      localStorage.setItem('nock-theme', theme)
    } else {
      delete document.documentElement.dataset.theme
      localStorage.removeItem('nock-theme')
    }
  }, [theme])
  const toggle = useCallback(() => {
    setThemeState((t) => {
      const current = t ?? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dracula' : 'cupcake')
      return current === 'cupcake' ? 'dracula' : 'cupcake'
    })
  }, [])
  return { theme, toggle }
}

export default function App() {
  const { projects, refresh } = useProjects()
  const [active, setActive] = useState<string | null>(null)
  const [tab, setTabState] = useState<Tab>(tabFromHash)
  const { theme, toggle: toggleTheme } = useTheme()

  const setTab = useCallback((t: Tab) => {
    setTabState(t)
    if (window.location.hash.slice(1) !== t) {
      window.location.hash = t
    }
  }, [])

  useEffect(() => {
    const onHashChange = () => setTabState(tabFromHash())
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  return (
    <div className="app">
      <header>
        <div className="header-top">
          <h1>NOCK</h1>
          <button type="button" className="theme-toggle" onClick={toggleTheme} title="Toggle light/dark theme">
            {theme === 'dracula' ? 'Light mode' : theme === 'cupcake' ? 'Dark mode' : 'Toggle theme'}
          </button>
        </div>
        <p className="tagline">texture generator &amp; manager — SHANKPIT texture tools, built on ImageMagick + PARENA</p>
        <nav className="tabs">
          <button className={tab === 'textures' ? 'active' : ''} onClick={() => setTab('textures')}>
            Texture Library
          </button>
          <button className={tab === 'animations' ? 'active' : ''} onClick={() => setTab('animations')}>
            Animations
          </button>
          <button className={tab === 'door-scripts' ? 'active' : ''} onClick={() => setTab('door-scripts')}>
            Door Scripts
          </button>
          <button className={tab === 'projects' ? 'active' : ''} onClick={() => setTab('projects')}>
            Projects (layer editor)
          </button>
          <button className={tab === 'brawlpit' ? 'active' : ''} onClick={() => setTab('brawlpit')}>
            BRAWLPIT Levels
          </button>
          <button className={tab === 'ai-opponents' ? 'active' : ''} onClick={() => setTab('ai-opponents')}>
            BRAWLPIT AI Opponents
          </button>
          <button className={tab === 'shankpit' ? 'active' : ''} onClick={() => setTab('shankpit')}>
            SHANKPIT Levels
          </button>
          <button className={tab === 'shankpit-ai-opponents' ? 'active' : ''} onClick={() => setTab('shankpit-ai-opponents')}>
            SHANKPIT AI Opponents
          </button>
          <button className={tab === 'sprays' ? 'active' : ''} onClick={() => setTab('sprays')}>
            Sprays
          </button>
        </nav>
      </header>

      {tab === 'textures' ? (
        <div className="layout-single">
          <TextureLibrary />
        </div>
      ) : tab === 'animations' ? (
        <div className="layout-single">
          <Animations />
        </div>
      ) : tab === 'door-scripts' ? (
        <div className="layout-single">
          <DoorScripts />
        </div>
      ) : tab === 'brawlpit' ? (
        <LevelEditor />
      ) : tab === 'shankpit' ? (
        <ShankpitLevelEditor />
      ) : tab === 'ai-opponents' ? (
        <div className="layout-single">
          <AiOpponents />
        </div>
      ) : tab === 'shankpit-ai-opponents' ? (
        <div className="layout-single">
          <ShankpitAiOpponents />
        </div>
      ) : tab === 'sprays' ? (
        <Sprays />
      ) : (
        <div className="layout">
          <aside className="project-list">
            <h2>Projects</h2>
            <ul>
              {projects.map((p) => (
                <li key={p} className={p === active ? 'active' : ''}>
                  <button onClick={() => setActive(p)}>{p}</button>
                </li>
              ))}
            </ul>
            <NewProjectForm
              onCreated={(name) => {
                refresh()
                setActive(name)
              }}
            />
          </aside>

          <main>
            {active ? (
              <ProjectEditor
                key={active}
                name={active}
                onDeleted={() => {
                  setActive(null)
                  refresh()
                }}
              />
            ) : (
              <p className="hint">Select or create a project to start editing.</p>
            )}
          </main>
        </div>
      )}
    </div>
  )
}
