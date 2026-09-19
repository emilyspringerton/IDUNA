import { useCallback, useEffect, useState } from 'react'
import { deadweightCheckpoints, type DeadweightCheckpoint } from './api'

// DeadweightAiOpponents.tsx -- DEADWEIGHT's registry view, a port of DeadweightAiOpponents.tsx (same list/disable/hide-disabled/
// set-as-opponent affordances) against the game-scoped registry. Honest scope: activating marks the checkpoint the registry's
// active opponent; the DEADWEIGHT bot pool does not consume it yet.

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
  c: DeadweightCheckpoint
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
      <td>{new Date(c.created_at).toLocaleString()}</td>
      <td>
        <label title="Excludes this checkpoint from the training pipeline's own resume/warm-start and the bot pool -- reversible, the row and its blob stay intact.">
          <input type="checkbox" checked={c.is_disabled} disabled={busy} onChange={onToggleDisabled} />
          {' '}Disabled
        </label>
      </td>
      <td>
        {isActive ? (
          <span className="active-badge">★ current opponent</span>
        ) : (
          <button type="button" disabled={busy} onClick={onActivate}
                  title="Marks this checkpoint as the registry's active opponent (the DEADWEIGHT bot pool does not consume it yet).">
            Set as opponent
          </button>
        )}
      </td>
    </tr>
  )
}

const HIDE_DISABLED_STORAGE_KEY = 'nock.deadweightAiOpponents.hideDisabled'

export default function DeadweightAiOpponents() {
  const [list, setList] = useState<DeadweightCheckpoint[]>([])
  const [active, setActive] = useState<DeadweightCheckpoint | null>(null)
  const [roleFilter, setRoleFilter] = useState<string>('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const [bulkBusy, setBulkBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  // Same real, per-viewer localStorage display-preference convention AiOpponents.tsx's own
  // hideDisabled already establishes (see that file's own doc comment for the full rationale) --
  // defaults to true (hidden) for the identical reason.
  const [hideDisabled, setHideDisabled] = useState<boolean>(() => {
    try {
      return localStorage.getItem(HIDE_DISABLED_STORAGE_KEY) !== 'false'
    } catch {
      return true
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(HIDE_DISABLED_STORAGE_KEY, String(hideDisabled))
    } catch {
      // per-viewer convenience only
    }
  }, [hideDisabled])

  const refresh = useCallback(() => {
    setLoading(true)
    Promise.all([deadweightCheckpoints.list(roleFilter || undefined), deadweightCheckpoints.getActive()])
      .then(([l, a]) => {
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

  const activate = async (c: DeadweightCheckpoint) => {
    setBusyId(c.id)
    setError(null)
    try {
      await deadweightCheckpoints.activate(c.id)
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusyId(null)
    }
  }

  const toggleDisabled = async (c: DeadweightCheckpoint) => {
    setBusyId(c.id)
    setError(null)
    try {
      await deadweightCheckpoints.setDisabled(c.id, !c.is_disabled)
      refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusyId(null)
    }
  }

  // Same real "Disable All" bulk affordance AiOpponents.tsx's own disableAll establishes (S431) --
  // respects the Role filter, only touches rows not already disabled.
  const disableAll = async () => {
    const targets = list.filter((c) => !c.is_disabled)
    if (targets.length === 0) return
    const scope = roleFilter ? (ROLE_LABELS[roleFilter] ?? roleFilter) : 'ALL roles'
    if (!window.confirm(`Disable all ${targets.length} currently-listed checkpoint(s) (${scope})? This excludes them from the training pipeline's resume/warm-start and the bot pool.`)) {
      return
    }
    setBulkBusy(true)
    setError(null)
    try {
      const results = await Promise.allSettled(targets.map((c) => deadweightCheckpoints.setDisabled(c.id, true)))
      const failed = results.filter((r) => r.status === 'rejected')
      if (failed.length > 0) {
        setError(`${failed.length} of ${targets.length} failed to disable -- see the individual rows and retry those.`)
      }
      refresh()
    } finally {
      setBulkBusy(false)
    }
  }

  const hiddenCount = list.filter((c) => c.is_disabled).length
  const visibleList = hideDisabled ? list.filter((c) => !c.is_disabled) : list

  return (
    <div className="ai-opponents">
      <div className="ai-opponents-header">
        <h2>DEADWEIGHT AI Opponents</h2>
        <p className="hint">
          Real checkpoints pushed from any training location (e.g. the Colab script) to the shared DEADWEIGHT registry. Disable a
          checkpoint to exclude it from the league's resume/warm-start. "Set as opponent" marks the registry's active opponent.
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
        {' '}
        <label title="Disabling a checkpoint is real, reversible state -- this just stops rendering rows already in that state, so it can double as a soft-delete for old, inert models without actually deleting anything.">
          <input type="checkbox" checked={hideDisabled} onChange={(e) => setHideDisabled(e.target.checked)} />
          {' '}Hide Disabled{hiddenCount > 0 ? ` (${hiddenCount})` : ''}
        </label>
        {' '}
        <button type="button" disabled={bulkBusy || list.every((c) => c.is_disabled)} onClick={disableAll}
                title="Disables every currently-listed checkpoint (respects the Role filter above).">
          {bulkBusy ? 'Disabling…' : 'Disable All'}
        </button>
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
        <p className="hint">No checkpoints in the registry yet -- run DEADWEIGHT/training/colab_train.py to push some.</p>
      ) : visibleList.length === 0 ? (
        <p className="hint">
          All {list.length} currently-listed checkpoint(s) are disabled and hidden.{' '}
          <button type="button" onClick={() => setHideDisabled(false)}>Show Disabled</button>
        </p>
      ) : (
        <table className="checkpoint-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Role</th>
              <th>Gen</th>
              <th>Elo</th>
              <th>From</th>
              <th>Created</th>
              <th>League</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {visibleList.map((c) => (
              <CheckpointRow
                key={c.id}
                c={c}
                isActive={active?.id === c.id}
                busy={busyId === c.id || bulkBusy}
                onActivate={() => activate(c)}
                onToggleDisabled={() => toggleDisabled(c)}
              />
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
