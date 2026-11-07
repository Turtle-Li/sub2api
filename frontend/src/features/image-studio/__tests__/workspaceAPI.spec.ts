import { beforeEach, describe, expect, it, vi } from 'vitest'
import { studioAPI, type StudioTask } from '../api'
import { modelIdentity, workspaceAPI } from '../workspaceAPI'

vi.mock('../api', () => ({ studioAPI: vi.fn() }))
const model = (id: string, provider = 'fixture') => ({ id, provider, object: 'model' })
const task = (id: string) => ({ id: `${id}/one`, job: { id, model: 'image', provider: 'fixture', status: 'completed' }, item: { custom_id: 'one', image_count: 1 } }) as StudioTask
function client(models = [model('image')]) {
  return { models: vi.fn(async () => models), tasks: vi.fn(async (_cursor = '') => ({ tasks: [] as StudioTask[], itemContinuations: [], nextCursor: null as string | null })), items: vi.fn(), image: vi.fn(), cancel: vi.fn(), refreshPending: vi.fn(async (tasks: StudioTask[]) => tasks), submit: vi.fn() }
}
beforeEach(() => vi.clearAllMocks())
describe('model-directed workspace', () => {
  it('deduplicates model routes while retaining provider identity and ignores non-image admission', async () => {
    const first = client(), duplicate = client(), second = client([model('image', 'other')]), text = client()
    text.models.mockRejectedValue({ status: 403 })
    const clients = new Map([[1, first], [2, duplicate], [3, second], [4, text]])
    vi.mocked(studioAPI).mockImplementation(key => clients.get(key)!)
    const api = workspaceAPI([4, 2, 3, 1], new AbortController().signal)
    expect((await api.models()).map(modelIdentity)).toEqual(['fixture:image', 'other:image'])
    expect(api.warnings()).toEqual([])
  })
  it('keeps independent history cursors and routes assets and cancellation to their original owner', async () => {
    const first = client(), second = client()
    const a = task('a'), b = task('b'), c = task('c')
    first.tasks.mockResolvedValueOnce({ tasks: [a], itemContinuations: [], nextCursor: '12' }).mockResolvedValueOnce({ tasks: [c], itemContinuations: [], nextCursor: null })
    second.tasks.mockResolvedValue({ tasks: [b], itemContinuations: [], nextCursor: null })
    vi.mocked(studioAPI).mockImplementation(key => key === 10 ? first : second)
    const api = workspaceAPI([10, 20], new AbortController().signal)
    const page = await api.tasks()
    expect(page.tasks.map(task => task.id).sort()).toEqual(['a/one', 'b/one'])
    await api.image(b, 0); await api.cancel(a)
    expect(second.image).toHaveBeenCalledWith(b, 0)
    expect(first.cancel).toHaveBeenCalledWith(a)
    await api.tasks(page.nextCursor!)
    expect(first.tasks).toHaveBeenLastCalledWith('12')
    expect(second.tasks).toHaveBeenCalledOnce()
  })
  it('pins an uncertain POST to the same key and never rotates to a newly preferred route', async () => {
    const first = client(), second = client()
    first.submit.mockRejectedValue(new Error('timeout'))
    vi.mocked(studioAPI).mockImplementation(key => key === 10 ? first : second)
    const api = workspaceAPI([10, 20], new AbortController().signal)
    const body = { model: 'image', provider: 'fixture', items: [{ custom_id: 'one', prompt: 'boat' }] }
    await expect(api.submit(body, 'same-attempt')).rejects.toThrow('timeout')
    first.models.mockResolvedValue([]); api.refreshDirectory()
    await api.models()
    await expect(api.submit(body, 'same-attempt')).rejects.toThrow('timeout')
    expect(first.submit).toHaveBeenCalledTimes(2)
    expect(first.submit.mock.calls[1]?.[1]).toBe('same-attempt')
    expect(second.submit).not.toHaveBeenCalled()
  })
  it('retains an editing root on its own key and refuses cross-key continuation', async () => {
    const first = client(), second = client([model('muse')]), source = task('root')
    first.tasks.mockResolvedValue({ tasks: [source], itemContinuations: [], nextCursor: null })
    first.submit.mockResolvedValue(task('child').job)
    vi.mocked(studioAPI).mockImplementation(key => key === 10 ? first : second)
    const api = workspaceAPI([10, 20], new AbortController().signal)
    await api.tasks()
    expect(await api.canContinue(source, model('image'))).toBe(true)
    expect(await api.canContinue(source, model('muse'))).toBe(false)
    await expect(api.submit({ model: 'muse', provider: 'fixture', parent_batch_id: 'root', items: [] }, 'edit')).rejects.toThrow('原作品')
    expect(second.submit).not.toHaveBeenCalled()
    await api.submit({ model: 'image', provider: 'fixture', parent_batch_id: 'root', items: [] }, 'valid-edit')
    expect(first.submit).toHaveBeenCalledOnce()
  })
})
