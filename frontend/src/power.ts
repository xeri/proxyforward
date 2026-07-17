import {useEffect, useState} from 'react'
import {
  ServiceStatus, StartEngine, StartService, StopEngine, StopService,
} from '../wailsjs/go/app/App'
import {UIStatus} from './state'

/**
 * The engine power verbs, service-aware. When a Windows service owns the run,
 * on/off means the *service*: the run is the service's to hold up, so an
 * attached StopEngine would only be undone by its restart policy. Otherwise it
 * is the in-process (or attached-daemon) run itself.
 *
 * useServiceRunning polls once per relevant status change so a control targets
 * the right layer without Settings threading service state down to it.
 */
export function useServiceRunning(status: UIStatus): boolean {
  const [running, setRunning] = useState(false)
  useEffect(() => {
    let live = true
    ServiceStatus()
      .then(s => { if (live) setRunning(normalize(s) === 'running') })
      .catch(() => { if (live) setRunning(false) })
    return () => { live = false }
  }, [status.runState, status.mode])
  return running
}

function normalize(s: string): string {
  return s.toLowerCase().replace(/[-_ ]/g, '')
}

/** serviceRunning is useServiceRunning's answer for a non-React caller (the
 * command palette): one query, no subscription. */
export async function serviceRunning(): Promise<boolean> {
  try {
    return normalize(await ServiceStatus()) === 'running'
  } catch {
    return false
  }
}

export function powerStart(svcRunning: boolean): Promise<void> {
  return svcRunning ? StartService() : StartEngine()
}

export function powerStop(svcRunning: boolean, force: boolean): Promise<void> {
  return svcRunning ? StopService() : StopEngine(force)
}
