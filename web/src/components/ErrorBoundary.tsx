import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

interface ErrorBoundaryProps {
  resetKey: string
  children: ReactNode
}

interface ErrorBoundaryState {
  error: Error | null
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  componentDidUpdate(prevProps: ErrorBoundaryProps) {
    if (prevProps.resetKey !== this.props.resetKey && this.state.error !== null) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(_error: Error, _info: ErrorInfo) {
    // Error captured in state for localized fallback UI
  }

  render() {
    if (this.state.error) {
      return (
        <Card className="p-6 space-y-3">
          <h2 className="text-[16px] font-semibold text-white">Failed to render view</h2>
          <p className="text-[13px] text-[#FCA5A5]">{this.state.error.message}</p>
          <Button variant="secondary" onClick={() => this.setState({ error: null })}>
            Retry
          </Button>
        </Card>
      )
    }
    return this.props.children
  }
}
