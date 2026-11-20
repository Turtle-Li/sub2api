import React, { useEffect, useLayoutEffect, useRef } from 'react'

interface GalleryTailProps {
  pageKey: string | null
  loading: boolean
  error: string
  onLoadMore: () => void
  onRetry: () => void
}

const OBSERVER_ROOT_MARGIN = '0px 0px 400px 0px'

export default function GalleryTail({
  pageKey,
  loading,
  error,
  onLoadMore,
  onRetry,
}: GalleryTailProps): React.ReactElement {
  const tailRef = useRef<HTMLDivElement>(null)
  const endStatusRef = useRef<HTMLSpanElement>(null)
  const buttonFocusedRef = useRef(false)
  const showError = !loading && Boolean(error)
  const isEnded = !pageKey && !loading && !showError

  useEffect(() => {
    const target = tailRef.current
    if (!target || !pageKey || loading || error || typeof IntersectionObserver === 'undefined') return

    let active = true
    const observer = new IntersectionObserver(entries => {
      if (active && entries.some(entry => entry.isIntersecting)) onLoadMore()
    }, { rootMargin: OBSERVER_ROOT_MARGIN })

    observer.observe(target)
    return () => {
      active = false
      observer.disconnect()
    }
  }, [pageKey, loading, error, onLoadMore])

  useLayoutEffect(() => {
    if (!isEnded || !buttonFocusedRef.current) return
    buttonFocusedRef.current = false
    const activeElement = document.activeElement
    if (!activeElement || activeElement === document.body || activeElement === document.documentElement) {
      endStatusRef.current?.focus({ preventScroll: true })
    }
  }, [isEnded])

  const handleButtonBlur = (event: React.FocusEvent<HTMLButtonElement>) => {
    if (event.currentTarget.isConnected) {
      buttonFocusedRef.current = false
    }
  }

  const handleActionClick = () => {
    if (loading) return
    if (showError) onRetry()
    else if (pageKey) onLoadMore()
  }

  const buttonLabel = loading ? '加载中…' : showError ? '重试加载' : '加载更多'
  const statusMessage = loading ? '正在读取作品…' : isEnded ? '已显示全部作品' : ''

  return (
    <div ref={tailRef} className="studio-gallery-tail">
      <span
        ref={endStatusRef}
        role="status"
        aria-atomic="true"
        tabIndex={isEnded ? -1 : undefined}
        className={isEnded ? undefined : 'sr-only'}
      >
        {statusMessage}
      </span>
      <span
        role="alert"
        aria-atomic="true"
        className={showError ? 'studio-gallery-tail-error' : 'sr-only'}
      >
        {showError ? error : ''}
      </span>
      {!isEnded && (
        <button
          type="button"
          onClick={handleActionClick}
          onFocus={() => { buttonFocusedRef.current = true }}
          onBlur={handleButtonBlur}
          aria-disabled={loading || undefined}
        >
          {buttonLabel}
        </button>
      )}
    </div>
  )
}
