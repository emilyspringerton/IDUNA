// TabErrorBoundary.tsx -- real, live-motivated fix. Founder real-time, 2026-09-28: "shankpit
// levels is down its just a blank screen" -- root cause was an uncaught THREE.WebGLRenderer
// construction error (their browser couldn't create a WebGL context). With NO error boundary
// anywhere in this app, that error propagated up through React's own commit phase and unmounted
// the ENTIRE app -- header, nav, every other tab, all of it, not just the one tab that crashed.
// The individual WebGL construction sites are now guarded too (see webglSupport.ts), but this is
// the real, general-purpose safety net: whatever crashes next (a bad API response, a bug in a
// tab nobody's guarded yet), it takes down only that one tab's content, not the whole app -- the
// header/nav stay live so a founder can just click a different tab instead of reloading blind.
import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

export class TabErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('TabErrorBoundary caught a render error:', error, info.componentStack)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="layout-single">
          <p className="error">
            This tab crashed: {this.state.error.message}. Try a different tab, or reload the page.
          </p>
        </div>
      )
    }
    return this.props.children
  }
}
