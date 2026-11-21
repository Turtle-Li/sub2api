import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import type { Connection, Edge, Node } from '@xyflow/vue'
import BoardStage from '../BoardStage.vue'
import { createWorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import { createVideoPosterCache } from '@/features/video-studio/posterCache'
import { createCanvasImageThumbCache } from '../imageThumbCache'
import type { CanvasDocument } from '../types'

vi.mock('@xyflow/vue', async original => {
  const { ref } = await import('vue')
  return { ...await original<typeof import('@xyflow/vue')>(), setupVueFlow: () => ({
    viewport: ref({ x: 0, y: 0, zoom: 1 }), setViewport: vi.fn(), zoomIn: vi.fn(), zoomOut: vi.fn(), fitView: vi.fn(),
    screenToFlowPosition: vi.fn(),
  }) }
})
const Flow = defineComponent({ name: 'FlowProbe', props: ['nodes', 'edges', 'isValidConnection'], emits: ['update:nodes', 'update:edges'], setup: () => () => h('div') })
const id = (n: number) => `ab000000-0000-4000-8000-${String(n).padStart(12, '0')}`

it('accepts persisted edges when Vue Flow 2 validates its input and separately rejects drawing a duplicate', async () => {
  const doc: CanvasDocument = { viewport: { x: 0, y: 0, zoom: 1 }, nodes: [
    { kind: 'image', id: id(1), x: 10, y: 20, image: { route: 0, batchId: 'original', itemId: 'frame', index: 0 } },
    { kind: 'video-draft', id: id(2), x: 360, y: 20 }, { kind: 'note', id: id(3), x: 10, y: 320 },
  ], edges: [{ source: id(1), target: id(2) }] }
  const wrapper = mount(BoardStage, { props: { boardId: id(6), doc, assets: { [id(1)]: { available: true } }, attempts: [], selectedNodeId: null,
    posterCache: createVideoPosterCache(), posterRevision: 0, thumbCache: createCanvasImageThumbCache(), durations: createWorkspaceDurationStore(),
  }, global: { stubs: { VueFlow: Flow } } })
  try {
    const engine = wrapper.getComponent(Flow)
    const checked = engine.props('isValidConnection') as (value: Edge | Connection) => boolean
    const saved = (engine.props('edges') as Edge[])[0]!
    const drawing = { source: id(1), target: id(2), sourceHandle: null, targetHandle: null }
    expect(checked(saved)).toBe(true)
    expect(checked({ ...saved, id: 'not-in-the-document' })).toBe(false)
    expect(checked(drawing)).toBe(false)
    expect(checked({ ...drawing, source: id(3) })).toBe(true)
    const nodes = engine.props('nodes') as Node[]
    engine.vm.$emit('update:nodes', nodes.map(n => ({ ...n, measured: { width: 270, height: 210 } })))
    await nextTick(); await wrapper.setProps({ selectedNodeId: id(1) })
    expect((engine.props('nodes') as Node[])[0]).toMatchObject({ selected: true, measured: { width: 270, height: 210 } })
    expect(doc.nodes[0]).not.toHaveProperty('measured')
  } finally { wrapper.unmount() }
})
