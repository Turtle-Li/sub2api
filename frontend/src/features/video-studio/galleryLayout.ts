import { nextTick, onBeforeUnmount, watch, type Ref, type WatchSource } from 'vue'

export interface GalleryPlacement {
  column: number
  x: number
  y: number
  width: number
  height: number
}

export interface GalleryLayout {
  placements: GalleryPlacement[]
  height: number
}

export function calculateGalleryLayout(width: number, heights: readonly number[], columnCount: number, gap: number): GalleryLayout {
  const columns = Math.max(1, Math.floor(columnCount))
  const spacing = Math.max(0, gap)
  const cardWidth = (width - spacing * (columns - 1)) / columns
  const columnHeights = Array.from({ length: columns }, () => 0)
  const placements = heights.map(height => {
    let column = 0
    for (let candidate = 1; candidate < columns; candidate += 1) {
      if (columnHeights[candidate] < columnHeights[column]) column = candidate
    }
    const placement = { column, x: column * (cardWidth + spacing), y: columnHeights[column], width: cardWidth, height }
    columnHeights[column] += height + spacing
    return placement
  })
  return { placements, height: Math.max(0, ...columnHeights) - (placements.length ? spacing : 0) }
}

export function measureGalleryLayout(container: HTMLElement): GalleryLayout | undefined {
  const width = container.clientWidth
  if (width <= 0) return undefined
  const cards = Array.from(container.children).filter((child): child is HTMLElement => child instanceof HTMLElement)
  const styles = getComputedStyle(container)
  const configuredColumns = Number.parseInt(styles.getPropertyValue('--vs-gallery-columns'), 10)
  const configuredGap = Number.parseFloat(styles.getPropertyValue('--vs-gallery-gap'))
  const columns = Number.isFinite(configuredColumns) && configuredColumns > 0 ? configuredColumns : 1
  const gap = Number.isFinite(configuredGap) ? configuredGap : 0
  const cardWidth = (width - gap * (columns - 1)) / columns
  for (const card of cards) card.style.width = `${cardWidth}px`
  const layout = calculateGalleryLayout(width, cards.map(card => card.getBoundingClientRect().height), columns, gap)
  cards.forEach((card, index) => {
    const placement = layout.placements[index]
    card.style.left = `${placement.x}px`
    card.style.top = `${placement.y}px`
  })
  container.style.height = `${layout.height}px`
  container.dataset.layout = 'ready'
  return layout
}

export function useGalleryLayout(container: Ref<HTMLElement | undefined>, items: WatchSource<unknown>) {
  let frame: number | undefined
  let fallback: ReturnType<typeof setTimeout> | undefined
  let observer: ResizeObserver | undefined
  let observed = new Set<HTMLElement>()

  function sync() {
    const element = container.value
    if (!element) return
    const cards = Array.from(element.children).filter((child): child is HTMLElement => child instanceof HTMLElement)
    if (observer) {
      const next = new Set([element, ...cards])
      for (const target of observed) if (!next.has(target)) observer.unobserve(target)
      for (const target of next) if (!observed.has(target)) observer.observe(target)
      observed = next
    }
    measureGalleryLayout(element)
  }

  function schedule() {
    if (frame !== undefined) cancelAnimationFrame(frame)
    if (fallback !== undefined) clearTimeout(fallback)
    if (typeof requestAnimationFrame === 'function') {
      frame = requestAnimationFrame(() => { frame = undefined; sync() })
    } else {
      fallback = setTimeout(() => { fallback = undefined; sync() }, 0)
    }
  }

  if (typeof ResizeObserver !== 'undefined') observer = new ResizeObserver(schedule)
  watch(container, schedule, { flush: 'post' })
  watch(items, () => { void nextTick(schedule) }, { flush: 'post' })
  window.addEventListener('resize', schedule, { passive: true })

  onBeforeUnmount(() => {
    if (frame !== undefined) cancelAnimationFrame(frame)
    if (fallback !== undefined) clearTimeout(fallback)
    observer?.disconnect()
    observed.clear()
    window.removeEventListener('resize', schedule)
  })
}
