import { useEffect, useRef } from 'react'
import type { TimelineItem } from './useDesignStream'

export function Timeline({ items, phase, selected, onSelect }: {
  items: TimelineItem[]
  phase: string | null
  selected: number | null
  onSelect: (n: number) => void
}) {
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    // Braces matter: scrollIntoView returns a Promise in recent Chrome, and an
    // effect must not return anything but a cleanup function.
    end.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
  }, [items.length, phase])

  if (items.length === 0 && !phase) return <p className="muted">Agent activity will stream here.</p>

  return (
    <ol className="timeline">
      {items.map((item) => {
        switch (item.kind) {
          case 'message':
            return <li key={item.key} className="t-message">{item.text}</li>
          case 'building':
            return (
              <li key={item.key} className="t-building">
                <span className="spinner" /> Building iteration {item.n}…
              </li>
            )
          case 'iteration': {
            const { n, passed, issues, metrics } = item.iteration
            const errors = issues.filter((i) => i.severity === 'error')
            const warnings = issues.filter((i) => i.severity === 'warning')
            return (
              <li key={item.key}>
                <button
                  className={`t-iteration ${passed ? 'pass' : 'fail'} ${selected === n ? 'selected' : ''}`}
                  onClick={() => onSelect(n)}
                >
                  <header>
                    <strong>Iteration {n}</strong>
                    <span className="badge">{passed ? 'PASSED' : `${errors.length} error${errors.length === 1 ? '' : 's'}`}</span>
                  </header>
                  {metrics && (
                    <p className="muted small">
                      {metrics.part_count} parts · {metrics.total_mass_kg} / {metrics.mass_limit_kg} kg · {metrics.peak_power_w} W peak
                    </p>
                  )}
                  {[...errors, ...warnings].length > 0 && (
                    <ul className="issues">
                      {[...errors, ...warnings].map((issue, i) => (
                        <li key={i} className={issue.severity}>
                          <code>{issue.code}</code> {issue.message}
                        </li>
                      ))}
                    </ul>
                  )}
                </button>
              </li>
            )
          }
          case 'done':
            return (
              <li key={item.key} className={`t-done ${item.status}`}>
                <strong>{item.status === 'passed' ? 'Design complete' : 'Design failed'}</strong>
                {item.summary && <p>{item.summary}</p>}
                {item.error && <p className="error-text">{item.error}</p>}
              </li>
            )
        }
      })}
      {phase && (
        <li className="t-building">
          <span className="spinner" /> {phase}
        </li>
      )}
      <div ref={end} />
    </ol>
  )
}
