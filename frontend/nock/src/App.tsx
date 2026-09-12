import { useCallback, useEffect, useState } from 'react'
import { api, type Project } from './api'
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

export default function App() {
  const { projects, refresh } = useProjects()
  const [active, setActive] = useState<string | null>(null)

  return (
    <div className="app">
      <header>
        <h1>NOCK</h1>
        <p className="tagline">layered image editor — SHANKPIT texture tools, built on ImageMagick</p>
      </header>

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
    </div>
  )
}
