import { useCallback, useEffect, useState } from 'react'
import { api, type Design, type Provider } from './api'
import { Timeline } from './Timeline'
import { useDesignStream } from './useDesignStream'
import { Viewer } from './Viewer'

const EXAMPLES = [
  '3U CubeSat for Earth imaging with deployable solar panels',
  '6U deep-space radio science mission with high power needs and 3-axis pointing',
  '1U technology demo with a small camera and a UHF antenna',
]

export default function App() {
  const [prompt, setPrompt] = useState(EXAMPLES[0])
  const [providers, setProviders] = useState<Provider[]>([])
  const [provider, setProvider] = useState('')
  const [history, setHistory] = useState<Design[]>([])
  const [designId, setDesignId] = useState<string | null>(null)
  const [selected, setSelected] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  const { items, iterations, status, finalIteration, phase } = useDesignStream(designId)

  const refreshHistory = useCallback(() => {
    api.listDesigns().then((r) => setHistory(r.designs)).catch(() => {})
  }, [])

  useEffect(() => {
    api.providers()
      .then((r) => {
        setProviders(r.providers)
        setProvider(r.default)
      })
      .catch(() => setError('Cannot reach the API at ' + (import.meta.env.VITE_API_URL ?? 'http://localhost:8080')))
    refreshHistory()
  }, [refreshHistory])

  useEffect(() => {
    if (status && status !== 'running') refreshHistory()
  }, [status, refreshHistory])

  // Show the user's pick, else the final design, else follow the newest iteration.
  const shown = selected ?? finalIteration ?? iterations.at(-1)?.n ?? null
  const current = iterations.find((i) => i.n === shown) ?? null
  const running = status === 'running'

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    try {
      const d = await api.createDesign(prompt, provider)
      setSelected(null)
      setDesignId(d.id)
      refreshHistory()
    } catch (err) {
      setError((err as Error).message)
    }
  }

  function open(id: string) {
    setSelected(null)
    setDesignId(id)
  }

  return (
    <div className="app">
      <aside className="panel">
        <header className="brand">
          <h1>Text&nbsp;→&nbsp;Spaceship</h1>
          <p className="muted small">Describe a mission. An agent designs a multi-part CubeSat, checks it in CAD, and iterates until it passes.</p>
        </header>

        <form onSubmit={submit} className="prompt-form">
          <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={3} maxLength={2000} />
          <div className="chips">
            {EXAMPLES.map((ex) => (
              <button type="button" key={ex} className="chip" onClick={() => setPrompt(ex)}>{ex}</button>
            ))}
          </div>
          <div className="row">
            <select value={provider} onChange={(e) => setProvider(e.target.value)} disabled={!providers.length}>
              {providers.length === 0 && <option value="">no provider configured</option>}
              {providers.map((p) => (
                <option key={p.name} value={p.name}>{p.name} · {p.default_model}</option>
              ))}
            </select>
            <button type="submit" className="primary" disabled={running || !prompt.trim()}>
              {running ? 'Designing…' : 'Design it'}
            </button>
          </div>
          {error && <p className="error-text">{error}</p>}
        </form>

        <section className="activity">
          <h2>Agent activity</h2>
          <Timeline items={items} phase={phase} selected={shown} onSelect={setSelected} />
        </section>

        <section>
          <h2>History</h2>
          <ul className="history">
            {history.map((d) => (
              <li key={d.id}>
                <button className={d.id === designId ? 'selected' : ''} onClick={() => open(d.id)}>
                  <span className={`dot ${d.status}`} />
                  <span className="history-prompt">{d.prompt}</span>
                  <span className="muted small">{d.iteration_count ?? 0} it · {d.model}</span>
                </button>
              </li>
            ))}
            {history.length === 0 && <li className="muted small">No designs yet.</li>}
          </ul>
        </section>
      </aside>

      <main className="stage">
        <Viewer
          url={current?.glb_url ? api.artifactURL(current.glb_url) : null}
          errorParts={current?.issues.filter((i) => i.severity === 'error').flatMap((i) => i.parts) ?? []}
        />
        {current && (
          <div className="hud">
            <div className="iter-tabs">
              {iterations.map((it) => (
                <button key={it.n} className={`${it.passed ? 'pass' : 'fail'} ${it.n === shown ? 'selected' : ''}`} onClick={() => setSelected(it.n)}>
                  {it.n}
                </button>
              ))}
            </div>
            {current.metrics && (
              <dl className="metrics">
                <div><dt>Mass</dt><dd>{current.metrics.total_mass_kg} / {current.metrics.mass_limit_kg} kg</dd></div>
                <div><dt>CG</dt><dd>{current.metrics.cg_mm.map((v) => v.toFixed(0)).join(', ')} mm</dd></div>
                <div><dt>Peak power</dt><dd>{current.metrics.peak_power_w} W</dd></div>
                <div><dt>Parts</dt><dd>{current.metrics.part_count}</dd></div>
              </dl>
            )}
            <div className="downloads">
              {current.step_url && <a href={api.artifactURL(current.step_url)} download>STEP</a>}
              {current.glb_url && <a href={api.artifactURL(current.glb_url)} download>GLB</a>}
            </div>
          </div>
        )}
      </main>
    </div>
  )
}
