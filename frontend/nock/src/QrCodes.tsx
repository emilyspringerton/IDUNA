import { useCallback, useEffect, useState } from 'react'
import { qrCodes, type QrCode } from './api'

// QR registry panel (kanban #459). Full capability: create (blank slug = auto-generate),
// retarget an already-printed code in place, delete, live preview, download PNG, hit counts.
// Every image encodes IDUNA's own stable /q/{slug} redirect, so retargeting never needs a reprint.

function QrRow({ c, onChanged }: { c: QrCode; onChanged: () => void }) {
  const [editing, setEditing] = useState(false)
  const [target, setTarget] = useState(c.target_url)
  const [label, setLabel] = useState(c.label)
  const [err, setErr] = useState<string | null>(null)
  const [stamp, setStamp] = useState(0)

  const save = async () => {
    setErr(null)
    try {
      await qrCodes.retarget(c.slug, target, label)
      setEditing(false)
      setStamp((s) => s + 1)
      onChanged()
    } catch (e) {
      setErr(String(e))
    }
  }
  const remove = async () => {
    if (!confirm(`Delete QR code "${c.slug}"? Printed copies will stop working.`)) return
    try {
      await qrCodes.delete(c.slug)
      onChanged()
    } catch (e) {
      setErr(String(e))
    }
  }

  return (
    <div className="texture-card">
      <img src={`${qrCodes.imageUrl(c.slug)}&s=${stamp}`} alt={`QR for ${c.slug}`} width={160} height={160} />
      <div>
        <strong>{c.slug}</strong> <span className="hint">{c.hit_count} hits</span>
      </div>
      {editing ? (
        <>
          <input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="label" />
          <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="https://…" />
          <button onClick={save}>Save</button> <button onClick={() => setEditing(false)}>Cancel</button>
        </>
      ) : (
        <>
          <div className="hint">{c.label || '(no label)'}</div>
          <a href={c.target_url} target="_blank" rel="noreferrer">{c.target_url}</a>
          <div>
            <button onClick={() => setEditing(true)}>Retarget</button>{' '}
            <a href={c.image_url} download={`${c.slug}.png`}>Download PNG</a>{' '}
            <button onClick={remove}>Delete</button>
          </div>
        </>
      )}
      {err && <p className="error">{err}</p>}
    </div>
  )
}

export function QrCodesPanel() {
  const [list, setList] = useState<QrCode[]>([])
  const [slug, setSlug] = useState('')
  const [target, setTarget] = useState('')
  const [label, setLabel] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const refresh = useCallback(() => {
    qrCodes.list().then(setList).catch((e) => setErr(String(e)))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])

  const create = async () => {
    setBusy(true)
    setErr(null)
    try {
      await qrCodes.create(slug.trim(), target.trim(), label.trim())
      setSlug('')
      setTarget('')
      setLabel('')
      refresh()
    } catch (e) {
      setErr(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="texture-library">
      <div className="generate-form">
        <h3>Create a QR code</h3>
        <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="destination URL (https://…)" />
        <input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="slug (blank = auto)" />
        <input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="label" />
        <button onClick={create} disabled={busy || !target.trim()}>{busy ? 'Creating…' : 'Create'}</button>
        {err && <p className="error">{err}</p>}
      </div>
      <div className="texture-grid">
        {list.map((c) => (
          <QrRow key={c.slug} c={c} onChanged={refresh} />
        ))}
        {list.length === 0 && <p className="hint">No QR codes yet — create one above.</p>}
      </div>
    </div>
  )
}
