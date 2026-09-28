import { useEffect, useReducer } from 'react'
import { api, type Iteration, type Status } from './api'

export type TimelineItem =
  | { kind: 'message'; key: string; text: string }
  | { kind: 'building'; key: string; n: number }
  | { kind: 'iteration'; key: string; iteration: Iteration }
  | { kind: 'done'; key: string; status: Status; summary: string | null; error: string | null }

type State = {
  items: TimelineItem[]
  iterations: Iteration[]
  status: Status | null
  finalIteration: number | null
  /** What the agent is doing right now, shown as a live status line. */
  phase: string | null
}
type Action = { type: 'reset' } | { type: 'event'; id: string; name: string; data: any }

const initial: State = { items: [], iterations: [], status: null, finalIteration: null, phase: null }

function reducer(state: State, action: Action): State {
  if (action.type === 'reset') return initial
  const { id, name, data } = action
  if (state.items.some((i) => i.key === id)) return state // replayed after reconnect
  switch (name) {
    case 'design.started':
      return { ...state, status: 'running', phase: `Agent is planning the layout (${data.model})…` }
    case 'agent.retrying':
      return {
        ...state,
        phase: `Provider busy (${data.reason}). Retrying in ${Math.round(data.wait_seconds)}s — attempt ${data.attempt}…`,
      }
    case 'agent.message':
      return { ...state, status: 'running', items: [...state.items, { kind: 'message', key: id, text: data.text }] }
    case 'iteration.started':
      return { ...state, status: 'running', phase: null, items: [...state.items, { kind: 'building', key: id, n: data.n }] }
    case 'iteration.completed': {
      const iteration = data as Iteration
      const items = state.items.filter((i) => !(i.kind === 'building' && i.n === iteration.n))
      return {
        ...state,
        phase: iteration.passed ? 'Checks passed. Agent is reviewing the request…' : 'Agent is fixing the issues…',
        items: [...items, { kind: 'iteration', key: id, iteration }],
        iterations: [...state.iterations, iteration],
      }
    }
    case 'design.completed':
      return {
        ...state,
        status: data.status,
        finalIteration: data.final_iteration || null,
        phase: null,
        items: [
          ...state.items.filter((i) => i.kind !== 'building'),
          { kind: 'done', key: id, status: data.status, summary: data.summary, error: data.error },
        ],
      }
    default:
      return state
  }
}

const EVENT_NAMES = [
  'design.started',
  'agent.retrying',
  'agent.message',
  'iteration.started',
  'iteration.completed',
  'design.completed',
]

/** Replays and follows a design's event stream (SSE). EventSource resumes with Last-Event-ID. */
export function useDesignStream(designId: string | null) {
  const [state, dispatch] = useReducer(reducer, initial)

  useEffect(() => {
    dispatch({ type: 'reset' })
    if (!designId) return
    const source = new EventSource(api.eventsURL(designId))
    const handlers = EVENT_NAMES.map((name) => {
      const handler = (e: MessageEvent) => {
        dispatch({ type: 'event', id: e.lastEventId, name, data: JSON.parse(e.data) })
        if (name === 'design.completed') source.close()
      }
      source.addEventListener(name, handler)
      return [name, handler] as const
    })
    return () => {
      handlers.forEach(([name, h]) => source.removeEventListener(name, h))
      source.close()
    }
  }, [designId])

  return state
}
