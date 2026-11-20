import { useEffect, useState, type RefObject } from 'react'
import type { ThumbnailPriority } from '../../thumbnailLoader'

// Adapted from 88lin 5523a592, MIT; see UPSTREAM.md. Separate prefetch from display lifetime.
function observeWithin(rootMargin: string) {
  let observer: IntersectionObserver | undefined
  const callbacks = new WeakMap<Element, (intersects: boolean) => void>()
  return (el: Element, callback: (intersects: boolean) => void) => {
    observer ??= new IntersectionObserver(entries => {
      for (const entry of entries) callbacks.get(entry.target)?.(entry.isIntersecting)
    }, { rootMargin })
    callbacks.set(el, callback)
    observer.observe(el)
    return () => { observer?.unobserve(el); callbacks.delete(el) }
  }
}

const observeNearby = observeWithin('640px 0px')
const observeVisible = observeWithin('0px')

export function useThumbnailPriority(ref: RefObject<Element | null>): ThumbnailPriority {
  const [near, setNear] = useState(false)
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === 'undefined')
  useEffect(() => {
    const el = ref.current
    if (!el || typeof IntersectionObserver === 'undefined') return
    const stopNearby = observeNearby(el, setNear)
    const stopVisible = observeVisible(el, setVisible)
    return () => { stopNearby(); stopVisible() }
  }, [ref])
  return visible ? 2 : near ? 1 : 0
}
