export const API_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

export type Status = 'running' | 'passed' | 'failed'

export type Issue = {
  code: string
  message: string
  parts: string[]
  severity: 'error' | 'warning'
}

export type Metrics = {
  part_count: number
  total_mass_kg: number
  mass_limit_kg: number
  cg_mm: number[]
  bbox_mm: { min: number[]; max: number[] }
  solar_area_cm2: number
  peak_power_w: number
}

export type Iteration = {
  n: number
  passed: boolean
  issues: Issue[]
  metrics?: Metrics
  glb_url?: string
  step_url?: string
}

export type Design = {
  id: string
  prompt: string
  provider: string
  model: string
  status: Status
  summary: string | null
  error: string | null
  created_at: string
  iteration_count?: number
}

export type Provider = { name: string; default_model: string }

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(API_URL + path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  const body = await res.json()
  if (!res.ok) throw new Error(body?.error?.message ?? res.statusText)
  return body as T
}

export const api = {
  providers: () => request<{ default: string; providers: Provider[] }>('/v1/providers'),
  listDesigns: () => request<{ designs: Design[] }>('/v1/designs'),
  createDesign: (prompt: string, provider?: string) =>
    request<Design>('/v1/designs', {
      method: 'POST',
      body: JSON.stringify({ prompt, provider: provider || undefined }),
    }),
  eventsURL: (id: string) => `${API_URL}/v1/designs/${id}/events`,
  artifactURL: (path: string) => API_URL + path,
}
