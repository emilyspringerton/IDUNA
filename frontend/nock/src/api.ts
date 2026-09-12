// api.ts — typed client for /admin/nock/api, mirroring internal/nock.Project/Layer's own real
// JSON shape exactly (see IDUNA/internal/nock/project.go). Every call goes through the same Go
// Service the CLI (cmd/nock) calls too — this file has no logic of its own beyond building the
// right HTTP request, matching the "same shape CLI and GUI" design.

export interface Layer {
  name: string
  file: string
  opacity: number
  visible: boolean
  mask?: string
}

export interface Project {
  name: string
  width: number
  height: number
  layers: Layer[]
}

const API_BASE = '/admin/nock/api'

async function req<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> =
    opts.body && !(opts.body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}
  const res = await fetch(`${API_BASE}${path}`, {
    credentials: 'include',
    ...opts,
    headers: { ...headers, ...(opts.headers as Record<string, string> | undefined) },
  })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

const enc = encodeURIComponent

export const api = {
  listProjects: () => req<string[]>('/projects'),

  createProject: (name: string, width: number, height: number) =>
    req<Project>('/projects', { method: 'POST', body: JSON.stringify({ name, width, height }) }),

  getProject: (name: string) => req<Project>(`/projects/${enc(name)}`),

  deleteProject: (name: string) => req<void>(`/projects/${enc(name)}`, { method: 'DELETE' }),

  addLayer: (project: string, name: string, file: File) => {
    const form = new FormData()
    form.append('name', name)
    form.append('file', file)
    return req<Project>(`/projects/${enc(project)}/layers`, { method: 'POST', body: form })
  },

  removeLayer: (project: string, layer: string) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}`, { method: 'DELETE' }),

  setOpacity: (project: string, layer: string, opacity: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/opacity`, {
      method: 'PATCH',
      body: JSON.stringify({ opacity }),
    }),

  setVisible: (project: string, layer: string, visible: boolean) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/visible`, {
      method: 'PATCH',
      body: JSON.stringify({ visible }),
    }),

  moveLayer: (project: string, layer: string, delta: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/move`, {
      method: 'PATCH',
      body: JSON.stringify({ delta }),
    }),

  setMask: (project: string, layer: string, file: File) => {
    const form = new FormData()
    form.append('file', file)
    return req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/mask`, { method: 'POST', body: form })
  },

  clearMask: (project: string, layer: string) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/mask`, { method: 'DELETE' }),

  hueSat: (project: string, layer: string, brightness: number, saturation: number, hue: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/hue-sat`, {
      method: 'PATCH',
      body: JSON.stringify({ brightness, saturation, hue }),
    }),

  sharpen: (project: string, layer: string, radius: number, sigma: number, amount: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/sharpen`, {
      method: 'PATCH',
      body: JSON.stringify({ radius, sigma, amount }),
    }),

  addGradient: (project: string, name: string, from: string, to: string, direction: string) =>
    req<Project>(`/projects/${enc(project)}/gradient`, {
      method: 'POST',
      body: JSON.stringify({ name, from, to, direction }),
    }),

  resize: (project: string, width: number, height: number) =>
    req<Project>(`/projects/${enc(project)}/resize`, {
      method: 'PATCH',
      body: JSON.stringify({ width, height }),
    }),

  exportUrl: (project: string, format: 'png' | 'jpg' = 'png') =>
    `${API_BASE}/projects/${enc(project)}/export?format=${format}&_=${Date.now()}`,
}
