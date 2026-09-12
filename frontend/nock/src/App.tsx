import { useCallback, useEffect, useState } from 'react'
import { api, generateProcedural, textures, type Project, type TextureSummary } from './api'
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
      <p className="hint">Both adjustments are destructive (baked into the layer immediately) — undo isn't real yet.</p>
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
      <div className="texture-grid">
        {list.map((t) => (
          <TextureLibraryRow key={t.id} t={t} onChanged={refresh} />
        ))}
        {list.length === 0 && <p className="hint">No textures yet — generate one above.</p>}
      </div>
    </div>
  )
}

export default function App() {
  const { projects, refresh } = useProjects()
  const [active, setActive] = useState<string | null>(null)
  const [tab, setTab] = useState<'projects' | 'textures'>('textures')

  return (
    <div className="app">
      <header>
        <h1>NOCK</h1>
        <p className="tagline">texture generator &amp; manager — SHANKPIT texture tools, built on ImageMagick + PARENA</p>
        <nav className="tabs">
          <button className={tab === 'textures' ? 'active' : ''} onClick={() => setTab('textures')}>
            Texture Library
          </button>
          <button className={tab === 'projects' ? 'active' : ''} onClick={() => setTab('projects')}>
            Projects (layer editor)
          </button>
        </nav>
      </header>

      {tab === 'textures' ? (
        <div className="layout-single">
          <TextureLibrary />
        </div>
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
