import { useCallback, useEffect, useState } from 'react'
import { checkpoints, type Checkpoint } from './api'

// AiOpponents.tsx — S421, founder real-time: "can we make it so that the current AI is saved?
// and then just like the level editor (or the skins interface) we should be able to select a
// model for the opponent from the registry" -- a real, direct browsing/selection UI over the
// S420 checkpoint registry, matching LevelEditor.tsx's own established "list on the left, act on
// the right" shape (see this file's own layout below).
//
// Real, honest, named scope: this UI sets a real, persisted, global "active opponent" pointer
// (IDUNA/internal/brawlpit/checkpoint_store.go's own SetActiveOpponent) -- BRAWLPIT's own native
// client does not yet read this flag to actually drive gameplay (that needs exporting a PPO
// policy's weights to a real C inference function, matching REDGARDEN's own established
// export_rl_policy_to_c.py/mlp_infer.c precedent -- real, separate, larger work, not built here).
// This page is the real, complete SELECTION half of that feature.

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

const ROLE_LABELS: Record<string, string> = {
  main: 'Main',
  main_exploiter: 'Main Exploiter',
  league_exploiter: 'League Exploiter',
}

function CheckpointRow({
  c,
  isActive,
  onActivate,
  onToggleDisabled,
  busy,
}: {
  c: Checkpoint
  isActive: boolean
  onActivate: () => void
  onToggleDisabled: () => void
  busy: boolean
}) {
  return (
    <tr className={[isActive ? 'active-opponent-row' : '', c.is_disabled ? 'disabled-checkpoint-row' : ''].join(' ').trim()}>
      <td className="checkpoint-name">{c.name || `#${c.id}`}</td>
      <td>{ROLE_LABELS[c.role] ?? c.role}</td>
      <td>{c.generation}</td>
      <td>{c.elo.toFixed(0)}</td>
      <td>{c.source_location}</td>
      <td title={c.has_weights ? 'the native BRAWLPIT client can load this one' : 'no exported weights yet -- native client can\'t use this one'}>
        {c.has_weights ? '✓' : '—'}
      </td>
      <td>{formatBytes(c.size_bytes)}</td>
      <td>{new Date(c.created_at).toLocaleString()}</td>
      <td>
        <label title="Excludes this checkpoint from --resume-from-registry warm-starts and the bot pool -- reversible, the row and its blob stay intact.">
          <input type="checkbox" checked={c.is_disabled} disabled={busy} onChange={onToggleDisabled} />
          {' '}Disabled
        </label>
      </td>
      <td>
        {isActive ? (
          <span className="active-badge">★ current opponent</span>
        ) : (
          <button type="button" disabled={busy || !c.has_weights} onClick={onActivate}
                  title={!c.has_weights ? 'this checkpoint has no exported native-inference weights yet' : ''}>
            Set as opponent
          </button>
        )}
      </td>
    </tr>
  )
}

export default function AiOpponents() {
  const [list, setList] = useState<Checkpoint[]>([])
  const [active, setActive] = useState<Checkpoint | null>(null)
  const [roleFilter, setRoleFilter] = useState<string>('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(() => {
    setLoading(true)
    Promise.all([checkpoints.list(roleFilter || undefined), checkpoints.getActive()])
      .then(([l, a]) => {
        // Newest generation first within each role reads more usefully than the API's own
        // newest-CREATED-first ordering when you're comparing across roles at a glance.
        setList([...l].sort((a, b) => b.generation - a.generation))
        setActive(a)
        setError(null)
      })
      .catch((err) => setError(String(err)))
      .finally(() => setLoading(false))
  }, [roleFilter])

  useEffect(() => {
    refresh()
  }, [refresh])

  const activate = async (id: number) => {
    setBusyId(id)
    setError(null)
    try {
      await checkpoints.activate(id)
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusyId(null)
    }
  }

  const toggleDisabled = async (c: Checkpoint) => {
    setBusyId(c.id)
    setError(null)
    try {
      await checkpoints.setDisabled(c.id, !c.is_disabled)
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div className="ai-opponents">
      <div className="ai-opponents-header">
        <h2>BRAWLPIT AI Opponents</h2>
        <p className="hint">
          Real checkpoints pushed from any training location (this box, Colab, or elsewhere) to the shared registry.
          Pick one to be the current opponent.
        </p>
        <label>
          Role{' '}
          <select value={roleFilter} onChange={(e) => setRoleFilter(e.target.value)}>
            <option value="">All roles</option>
            <option value="main">Main</option>
            <option value="main_exploiter">Main Exploiter</option>
            <option value="league_exploiter">League Exploiter</option>
          </select>
        </label>
        {error && <span className="error">{error}</span>}
      </div>

      {active && (
        <p className="hint">
          Current opponent: <strong>{active.name || `#${active.id}`}</strong> ({ROLE_LABELS[active.role] ?? active.role} gen{' '}
          {active.generation}, Elo {active.elo.toFixed(0)}, from {active.source_location})
        </p>
      )}

      {loading ? (
        <p className="hint">Loading…</p>
      ) : list.length === 0 ? (
        <p className="hint">No checkpoints in the registry yet -- start a training run (scripts/rl_train_packet.py --registry-url ...) to populate it.</p>
      ) : (
        <table className="checkpoint-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Role</th>
              <th>Gen</th>
              <th>Elo</th>
              <th>From</th>
              <th>Native</th>
              <th>Size</th>
              <th>Created</th>
              <th>League</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {list.map((c) => (
              <CheckpointRow
                key={c.id}
                c={c}
                isActive={active?.id === c.id}
                busy={busyId === c.id}
                onActivate={() => activate(c.id)}
                onToggleDisabled={() => toggleDisabled(c)}
              />
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
