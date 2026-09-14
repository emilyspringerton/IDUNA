import { useCallback, useEffect, useState } from 'react'
import { shankpitSprays, type ShankpitSpray } from './api'

// Sprays.tsx -- S459-19, founder real-time: "we need a nock sprays interface right now just to
// set the default." A real, deliberate v0 scope: list every spray (created via the Projects
// tab's own "Export to Spray" button), pick ONE real global default, delete. Pricing/a shop
// ("when we have sprays shop for GFD glow we can configure the price") is real, named, deferred
// future work -- not built here.
export default function Sprays() {
  const [sprays, setSprays] = useState<ShankpitSpray[]>([])
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(() => {
    shankpitSprays.list().then(setSprays).catch(() => setSprays([]))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])

  const setDefault = async (id: number) => {
    setError(null)
    try {
      await shankpitSprays.setDefault(id)
      refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  const doDelete = async (spray: ShankpitSpray) => {
    if (!confirm(`Delete spray "${spray.name}"? This can't be undone.`)) return
    setError(null)
    try {
      await shankpitSprays.delete(spray.id)
      refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  return (
    <div className="layout-single">
      <h2>Sprays</h2>
      <p className="hint">
        Sprays are exported from the Projects tab ("Export to Spray"). Pick one as the real, global default -- per-
        player selection and a real spray shop (GFD glow pricing) are deliberately deferred, not built yet.
      </p>
      {error && <p className="error">{error}</p>}
      <div className="spray-grid">
        {sprays.map((s) => (
          <div key={s.id} className={`spray-card ${s.is_default ? 'active' : ''}`}>
            <img src={shankpitSprays.imageUrl(s.id)} alt={s.name} width={96} height={96} />
            <div className="spray-card-name">{s.name}</div>
            <div className="hint">
              {s.width}×{s.height}
            </div>
            {s.is_default ? (
              <span className="hint">Default</span>
            ) : (
              <button type="button" onClick={() => setDefault(s.id)}>
                Set as default
              </button>
            )}
            <button className="danger" type="button" onClick={() => doDelete(s)}>
              Delete
            </button>
          </div>
        ))}
        {sprays.length === 0 && <p className="hint">No sprays yet -- export one from the Projects tab.</p>}
      </div>
    </div>
  )
}
