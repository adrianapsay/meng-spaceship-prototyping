import { Component, type ReactNode } from 'react'

/** Shows a render error instead of unmounting the whole app to a blank page. */
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <div className="crash">
        <h1>Something broke in the UI</h1>
        <pre>{this.state.error.message}</pre>
        <button className="primary" onClick={() => location.reload()}>Reload</button>
      </div>
    )
  }
}
