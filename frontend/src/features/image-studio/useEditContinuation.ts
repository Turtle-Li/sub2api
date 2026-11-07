import { useEffect, useRef, useState } from 'react'
import { taskPending, type StudioTask } from './api'

interface Options {
  tasks: StudioTask[]
  signal: AbortSignal
  load: (task: StudioTask) => Promise<string>
  onAdvance: (image: string, task: StudioTask) => void
  onError: (error: unknown) => void
}

// A completion belongs to the selection at submit time, never to a later manual choice.
export function useEditContinuation(options: Options) {
  const version = useRef(0)
  const callbacks = useRef(options)
  callbacks.current = options
  const [pending, setPending] = useState<{ jobId: string; version: number } | null>(null)

  function beginSelection() {
    version.current++
    setPending(null)
    return version.current
  }
  function isCurrent(candidate: number) {
    return candidate === version.current && !options.signal.aborted
  }
  function expectOutput(jobId: string, candidate: number) {
    if (isCurrent(candidate)) setPending({ jobId, version: candidate })
  }

  useEffect(() => {
    if (!pending || pending.version !== version.current || options.signal.aborted) return
    const outputs = options.tasks.filter(task => task.job.id === pending.jobId)
    if (!outputs.length || outputs.some(taskPending)) return
    const output = outputs.find(task => task.job.status === 'completed' && !task.job.fail_count && ['succeeded', 'success', 'completed'].includes(task.item.status) && task.item.image_count > 0)
    if (!output || outputs.some(task => task.item.status === 'failed')) { setPending(null); return }
    let active = true
    void callbacks.current.load(output).then(image => {
      if (!active || pending.version !== version.current || options.signal.aborted) return
      callbacks.current.onAdvance(image, output)
      setPending(null)
    }).catch(error => {
      if (!active || pending.version !== version.current || options.signal.aborted) return
      callbacks.current.onError(error)
      setPending(null)
    })
    return () => { active = false }
  }, [options.tasks, options.signal, pending])

  return { beginSelection, isCurrent, expectOutput }
}
