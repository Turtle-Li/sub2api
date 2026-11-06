import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useEditContinuation } from '../useEditContinuation'
import type { StudioTask } from '../api'

vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let controller: AbortController
let selected: ReturnType<typeof useEditContinuation>
let setTasks: (tasks: StudioTask[]) => void
let load: ReturnType<typeof vi.fn<(task: StudioTask) => Promise<string>>>
let version: number
const task = (status = 'success', images = 2, failed = 0): StudioTask => ({ id: 'edit-1/one', job: { id: 'edit-1', parent_batch_id: 'root-0', status: 'completed', fail_count: failed }, item: { custom_id: 'one', status, image_count: images } } as StudioTask)
function Harness() {
 const [tasks, changeTasks] = useState<StudioTask[]>([])
 const [reference, setReference] = useState('original-image')
 const [parent, setParent] = useState('root-0')
 const [prompt] = useState('next modification already typed')
 setTasks = changeTasks
 selected = useEditContinuation({ tasks, signal: controller.signal, load, onAdvance: (image, source) => { setReference(image); setParent(source.job.id) }, onError: vi.fn() })
 return <output>{JSON.stringify({ reference, parent, prompt })}</output>
}
beforeEach(() => {
 host = document.createElement('div'); document.body.append(host); root = createRoot(host)
 controller = new AbortController(); load = vi.fn().mockResolvedValue('new-output-first-image')
 act(() => root.render(<Harness />))
 act(() => { version = selected.beginSelection(); selected.expectOutput('edit-1', version) })
})
afterEach(() => { act(() => root.unmount()); host.remove() })
const state = () => JSON.parse(host.textContent || '{}')
const complete = async (tasks = [task()]) => { await act(async () => { setTasks(tasks); await Promise.resolve() }) }
describe('continuous edit reference advancement', () => {
 it('advances once to the completed job output and updates source parent without overwriting the next prompt', async () => {
   await complete()
   expect(load).toHaveBeenCalledTimes(1)
   expect(state()).toEqual({ reference: 'new-output-first-image', parent: 'edit-1', prompt: 'next modification already typed' })
   await complete([task()])
   expect(load).toHaveBeenCalledTimes(1)
 })
 it.each(['manual image', 'model', 'reference removal', 'upload'])('does not override a newer %s selection while output is pending', async () => {
   act(() => { selected.beginSelection() })
   await complete()
   expect(load).not.toHaveBeenCalled(); expect(state().reference).toBe('original-image')
 })
 it('rejects a late submit response after the selection changes', async () => {
   act(() => { selected.beginSelection(); selected.expectOutput('edit-1', version) })
   await complete()
   expect(load).not.toHaveBeenCalled()
 })
 it('rejects an old download completing after manual selection changes', async () => {
   let finish!: (value: string) => void
   load.mockImplementation(() => new Promise(resolve => { finish = resolve }))
   await complete()
   act(() => { selected.beginSelection() })
   await act(async () => { finish('stale-output'); await Promise.resolve() })
   expect(state().reference).toBe('original-image')
 })
 it('rejects an old key/session download after abort', async () => {
   let finish!: (value: string) => void
   load.mockImplementation(() => new Promise(resolve => { finish = resolve }))
   await complete(); controller.abort()
   await act(async () => { finish('other-key-output'); await Promise.resolve() })
   expect(state().reference).toBe('original-image')
 })
 it.each([task('failed', 0, 1), task('success', 1, 1), task('success', 0)])('preserves the existing reference on failure, partial failure or no output', async value => {
   await complete([value])
   expect(load).not.toHaveBeenCalled(); expect(state().reference).toBe('original-image')
 })
 it('ignores unrelated completed jobs', async () => {
   await complete([{ ...task(), job: { ...task().job, id: 'unrelated' } }])
   expect(load).not.toHaveBeenCalled()
 })
})
