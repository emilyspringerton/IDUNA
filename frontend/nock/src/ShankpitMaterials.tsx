import { useCallback, useEffect, useState } from 'react'
import { shankpitMaterials, textures, type ShankpitMaterial, type TextureSummary } from './api'

// ShankpitMaterials.tsx -- S478c, founder real-time: "do we need to make friction per material?
// i think we need a separate materials config screen it doesnt really make sense under the
// levels on the left side bar in the level editor - like how can i change the texture for a
// material and possibly even update the shader for a material." Pulls the materials
// define/edit/delete surface OUT of ShankpitLevelEditor.tsx (where S459-16's own MaterialsPanel
// used to live, add-only, nested inside the level editor) into its own top-level NOCK tab --
// same real "list on the left, act on the right" table shape ShankpitAiOpponents.tsx already
// establishes. The level editor keeps only its own real, legitimately level-scoped concern: a
// per-wall material *assignment* dropdown (WallPanel's own `<select>`), not the definition of
// what materials exist.
//
// Edit/Delete were already real, live API calls (api.ts's own shankpitMaterials.update/.delete)
// with no UI ever wired to them -- this screen is mostly that wiring, plus the new friction field
// (S478b) and a real texture picker sourced from the existing texture library (textures.list()),
// closing the "set their textures" half of the founder's own original S459-16 ask.

const SHADER_OPTIONS: { value: string; label: string }[] = [
  { value: 'standard', label: 'standard (Blinn-Phong)' },
  { value: 'ips_light', label: 'ips_light (emissive panel)' },
  { value: 'hps_light', label: 'hps_light (flickering sodium lamp)' },
]

function useMaterialList() {
  const [materials, setMaterials] = useState<ShankpitMaterial[]>([])
  const [loading, setLoading] = useState(true)
  const refresh = useCallback(() => {
    setLoading(true)
    shankpitMaterials
      .list()
      .then(setMaterials)
      .catch(() => setMaterials([]))
      .finally(() => setLoading(false))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])
  return { materials, loading, refresh }
}

function useTextureList() {
  const [list, setList] = useState<TextureSummary[]>([])
  useEffect(() => {
    textures.list().then(setList).catch(() => setList([]))
  }, [])
  return list
}

type MaterialFormState = {
  specular: number
  shininess: number
  friction: number
  shaderName: string
  textureId: number | null
}

function materialToFormState(m: ShankpitMaterial): MaterialFormState {
  return { specular: m.specular, shininess: m.shininess, friction: m.friction, shaderName: m.shader_name, textureId: m.texture_id ?? null }
}

function MaterialFields({
  state,
  onChange,
  textureList,
}: {
  state: MaterialFormState
  onChange: (next: MaterialFormState) => void
  textureList: TextureSummary[]
}) {
  return (
    <>
      <label>
        Specular{' '}
        <input
          type="number" min={0} max={1} step={0.05} value={state.specular}
          onChange={(e) => onChange({ ...state, specular: Number(e.target.value) })}
        />
      </label>
      <label>
        Shininess{' '}
        <input
          type="number" min={1} max={256} step={1} value={state.shininess}
          onChange={(e) => onChange({ ...state, shininess: Number(e.target.value) })}
        />
      </label>
      <label title="Real, live ground friction (S478b) -- consumed natively by resolve_collision/apply_friction for any box using this material. 0.30 matches SHANKPIT's own tuned global baseline; lower is slicker, higher is stickier.">
        Friction{' '}
        <input
          type="number" min={0} max={1} step={0.02} value={state.friction}
          onChange={(e) => onChange({ ...state, friction: Number(e.target.value) })}
        />
      </label>
      <label>
        Shader{' '}
        <select value={state.shaderName} onChange={(e) => onChange({ ...state, shaderName: e.target.value })}>
          {SHADER_OPTIONS.map((s) => (
            <option key={s.value} value={s.value}>
              {s.label}
            </option>
          ))}
        </select>
      </label>
      <label title="Real NOCK-managed override -- unset means 'use the native procedural default for this material name.' The native client stores this but doesn't fetch/decode it yet (no image decoder) -- only this web preview shows it.">
        Texture{' '}
        <select
          value={state.textureId ?? ''}
          onChange={(e) => onChange({ ...state, textureId: e.target.value ? Number(e.target.value) : null })}
        >
          <option value="">(native procedural default)</option>
          {textureList.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
      </label>
    </>
  )
}

function MaterialRow({
  m,
  textureList,
  onSaved,
  onDeleted,
}: {
  m: ShankpitMaterial
  textureList: TextureSummary[]
  onSaved: () => void
  onDeleted: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [form, setForm] = useState<MaterialFormState>(() => materialToFormState(m))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const startEdit = () => {
    setForm(materialToFormState(m))
    setError(null)
    setEditing(true)
  }

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      await shankpitMaterials.update(m.id, form.specular, form.shininess, form.textureId, form.shaderName, form.friction)
      setEditing(false)
      onSaved()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const del = async () => {
    if (!window.confirm(`Delete material "${m.name}"? Any wall still referencing this name falls back to the default material ("brick").`)) return
    setBusy(true)
    setError(null)
    try {
      await shankpitMaterials.delete(m.id)
      onDeleted()
    } catch (err) {
      setError(String(err))
      setBusy(false)
    }
  }

  const textureName = m.texture_id ? textureList.find((t) => t.id === m.texture_id)?.name ?? `#${m.texture_id}` : null

  if (editing) {
    return (
      <tr>
        <td>{m.name}</td>
        <td colSpan={5}>
          <div className="material-edit-row">
            <MaterialFields state={form} onChange={setForm} textureList={textureList} />
            <button type="button" disabled={busy} onClick={save}>
              {busy ? 'Saving…' : 'Save'}
            </button>
            <button type="button" disabled={busy} onClick={() => setEditing(false)}>
              Cancel
            </button>
            {error && <span className="error">{error}</span>}
          </div>
        </td>
      </tr>
    )
  }

  return (
    <tr>
      <td>{m.name}</td>
      <td>{m.shader_name}</td>
      <td>{m.specular}</td>
      <td>{m.shininess}</td>
      <td>{m.friction}</td>
      <td>{textureName ?? <span className="hint">(default)</span>}</td>
      <td>
        <button type="button" disabled={busy} onClick={startEdit}>
          Edit
        </button>{' '}
        <button type="button" disabled={busy} onClick={del}>
          Delete
        </button>
        {error && <span className="error">{error}</span>}
      </td>
    </tr>
  )
}

export default function ShankpitMaterials() {
  const { materials, loading, refresh } = useMaterialList()
  const textureList = useTextureList()

  const [name, setName] = useState('')
  const [form, setForm] = useState<MaterialFormState>({ specular: 0.05, shininess: 8, friction: 0.3, shaderName: 'standard', textureId: null })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const add = async () => {
    if (!name) return
    setBusy(true)
    setError(null)
    try {
      await shankpitMaterials.create(name, form.specular, form.shininess, form.textureId, form.shaderName, form.friction)
      setName('')
      setForm({ specular: 0.05, shininess: 8, friction: 0.3, shaderName: 'standard', textureId: null })
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="shankpit-materials">
      <h2>SHANKPIT Materials</h2>
      <p className="hint">
        The real, shared material registry (S459-16) every SHANKPIT level's own blocks reference by name -- shader,
        specular/shininess, ground friction (S478b), and an optional texture override all live here. A level's own
        block just picks a material by name in the level editor; this is where that name's actual properties are
        defined.
      </p>

      {loading ? (
        <p className="hint">Loading…</p>
      ) : (
        <table className="checkpoint-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Shader</th>
              <th>Specular</th>
              <th>Shininess</th>
              <th>Friction</th>
              <th>Texture</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {materials.map((m) => (
              <MaterialRow key={m.id} m={m} textureList={textureList} onSaved={refresh} onDeleted={refresh} />
            ))}
          </tbody>
        </table>
      )}

      <div className="material-panel">
        <h3>+ Add material</h3>
        <input placeholder="new material name" value={name} onChange={(e) => setName(e.target.value)} />
        <MaterialFields state={form} onChange={setForm} textureList={textureList} />
        <button type="button" onClick={add} disabled={!name || busy}>
          {busy ? 'Adding…' : '+ Add material'}
        </button>
        {error && <span className="error">{error}</span>}
      </div>
    </div>
  )
}
