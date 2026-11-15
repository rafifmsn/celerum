import { useEffect, useState } from 'react'

export type StreamStatus = 'connecting' | 'live' | 'reconnecting'

const STREAM_EVENTS = ['poll_complete', 'cluster_updated', 'dispatch_updated'] as const

export function useTelemetryStream(onUpdate: () => void): StreamStatus {
  const [status, setStatus] = useState<StreamStatus>('connecting')

  useEffect(() => {
    const source = new EventSource('/api/events')
    const handleEvent = () => {
      setStatus('live')
      onUpdate()
    }

    source.onopen = () => setStatus('live')
    source.onerror = () => setStatus('reconnecting')
    source.addEventListener('connected', () => setStatus('live'))
    for (const evt of STREAM_EVENTS) {
      source.addEventListener(evt, handleEvent)
    }

    return () => {
      source.close()
    }
  }, [onUpdate])

  return status
}
