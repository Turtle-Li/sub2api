import { describe, expect, it } from 'vitest'
import { calculateGalleryLayout, measureGalleryLayout } from '../galleryLayout'

describe('视频画廊确定性布局', () => {
  it('首排保持源顺序，后续投入当前最短列且同高取左', () => {
    const layout = calculateGalleryLayout(632, [180, 260, 140, 220, 100, 190], 3, 16)
    expect(layout.placements.map(({ column, x, y }) => ({ column, x, y }))).toEqual([
      { column: 0, x: 0, y: 0 },
      { column: 1, x: 216, y: 0 },
      { column: 2, x: 432, y: 0 },
      { column: 2, x: 432, y: 156 },
      { column: 0, x: 0, y: 196 },
      { column: 1, x: 216, y: 276 },
    ])
    expect(layout.placements.every(item => item.width === 200)).toBe(true)
    expect(layout.height).toBe(466)
  })

  it('手机两列首排为 1/2，第三条接在较矮的第一列', () => {
    const layout = calculateGalleryLayout(310, [100, 180, 120], 2, 10)
    expect(layout.placements.map(({ column, x, y }) => ({ column, x, y }))).toEqual([
      { column: 0, x: 0, y: 0 },
      { column: 1, x: 160, y: 0 },
      { column: 0, x: 0, y: 110 },
    ])
    expect(layout.height).toBe(230)
  })

  it('测量实际卡高并原位写入坐标，不改变 DOM 顺序', () => {
    const container = document.createElement('ul')
    container.style.setProperty('--vs-gallery-columns', '3')
    container.style.setProperty('--vs-gallery-gap', '14px')
    Object.defineProperty(container, 'clientWidth', { configurable: true, value: 628 })
    const heights = [120, 200, 150]
    const cards = heights.map((height, index) => {
      const card = document.createElement('li')
      card.dataset.id = String(index + 1)
      card.getBoundingClientRect = () => ({ height }) as DOMRect
      container.append(card)
      return card
    })

    const layout = measureGalleryLayout(container)
    expect(layout?.placements.map(item => item.y)).toEqual([0, 0, 0])
    expect(cards.map(card => card.dataset.id)).toEqual(['1', '2', '3'])
    expect(cards.map(card => card.style.left)).toEqual(['0px', '214px', '428px'])
    expect(cards.every(card => card.style.width === '200px')).toBe(true)
    expect(container.style.height).toBe('200px')
    expect(container.dataset.layout).toBe('ready')
  })

  it('分页、过滤及宽度变化后按当前卡片重新测量', () => {
    const container = document.createElement('ul')
    container.style.setProperty('--vs-gallery-columns', '2')
    container.style.setProperty('--vs-gallery-gap', '10px')
    let width = 310
    Object.defineProperty(container, 'clientWidth', { configurable: true, get: () => width })
    const heights = new Map<string, number>([['1', 100], ['2', 180], ['3', 120]])
    const add = (id: string) => {
      const card = document.createElement('li')
      card.dataset.id = id
      card.getBoundingClientRect = () => ({ height: heights.get(id) || 0 }) as DOMRect
      container.append(card)
    }
    add('1'); add('2'); add('3')
    measureGalleryLayout(container)
    expect([...container.children].map(card => (card as HTMLElement).style.top)).toEqual(['0px', '0px', '110px'])

    heights.set('4', 90); add('4')
    measureGalleryLayout(container)
    expect((container.children[3] as HTMLElement).style.left).toBe('160px')
    expect((container.children[3] as HTMLElement).style.top).toBe('190px')

    container.children[1].remove()
    measureGalleryLayout(container)
    expect([...container.children].map(card => (card as HTMLElement).dataset.id)).toEqual(['1', '3', '4'])
    expect([...container.children].map(card => (card as HTMLElement).style.top)).toEqual(['0px', '0px', '110px'])

    width = 630
    heights.set('1', 140)
    measureGalleryLayout(container)
    expect([...container.children].every(card => (card as HTMLElement).style.width === '310px')).toBe(true)
    expect((container.children[2] as HTMLElement).style.left).toBe('320px')
    expect((container.children[2] as HTMLElement).style.top).toBe('130px')
    expect(container.style.height).toBe('220px')
  })
})
