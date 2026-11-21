import { shallowRef } from 'vue'
import type { VideoJob } from './api'

export function validDurationMs(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return null
  return value
}

export function durationSecondsToMs(seconds: unknown): number | null {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) return null
  const ms = Math.round(seconds * 1000)
  return ms > 0 ? ms : null
}

export function measuredDurationMs(
  job: Pick<VideoJob, 'id' | 'actual_duration_ms'>,
  localDurationMs?: number | null,
): number | null {
  const local = validDurationMs(localDurationMs)
  if (local !== null) return local
  return validDurationMs(job.actual_duration_ms)
}

export function formatVideoDuration(durationMs: number | null | undefined): string {
  const valid = validDurationMs(durationMs)
  if (valid === null) return ''
  const rounded = Math.round((valid / 1000) * 10) / 10
  const displaySeconds = rounded > 0 ? rounded : 0.1
  if (displaySeconds >= 60) {
    const mins = Math.floor(displaySeconds / 60)
    const secs = Math.round((displaySeconds - mins * 60) * 10) / 10
    const whole = Math.floor(secs)
    const frac = Number.isInteger(secs) ? '' : secs.toFixed(1).slice(-2)
    return `${String(mins).padStart(2, '0')}:${String(whole).padStart(2, '0')}${frac}`
  }
  return `${Number.isInteger(displaySeconds) ? displaySeconds : displaySeconds.toFixed(1)} 秒`
}

/** Workspace-scoped in-memory duration measurements from explicit local video playback. */
export function createWorkspaceDurationStore() {
  const version = shallowRef(0)
  const durations = new Map<string, number>()

  function get(jobId: string): number | null {
    version.value
    return durations.get(jobId) ?? null
  }

  function set(jobId: string, durationMs: number) {
    const valid = validDurationMs(durationMs)
    if (!jobId || valid === null) return
    if (durations.get(jobId) === valid) return
    durations.set(jobId, valid)
    version.value += 1
  }

  function clear() {
    if (!durations.size) return
    durations.clear()
    version.value += 1
  }

  return { get, set, clear }
}

export type WorkspaceDurationStore = ReturnType<typeof createWorkspaceDurationStore>
