import React, { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import '@/styles/liquid-glass.css'

export interface LiquidSelectOption {
  readonly value: string
  readonly label: string
  readonly meta?: string
  readonly disabled?: boolean
}

interface LiquidSelectProps {
  readonly value: string
  readonly options: readonly LiquidSelectOption[]
  readonly onChange: (value: string) => void
  readonly ariaLabel: string
  readonly className?: string
  readonly compact?: boolean
  readonly disabled?: boolean
  readonly placeholder?: string
  readonly prefix?: string
  readonly minMenuWidth?: number
}

interface MenuCoords {
  readonly top: number
  readonly left: number
  readonly width: number
  readonly maxHeight: number
  readonly placement: 'bottom' | 'top'
}

function findOpenDialog(el: HTMLElement | null): HTMLElement | null {
  let cur = el?.parentElement ?? null
  while (cur) {
    if (cur.tagName === 'DIALOG' && ((cur as HTMLDialogElement).open || cur.hasAttribute('open'))) {
      return cur
    }
    cur = cur.parentElement
  }
  return null
}

export function LiquidSelect({
  value,
  options,
  onChange,
  ariaLabel,
  className = '',
  compact = false,
  disabled = false,
  placeholder = '请选择',
  prefix,
  minMenuWidth = 0,
}: LiquidSelectProps) {
  const listboxId = useId()
  const [open, setOpen] = useState(false)
  const [highlightedIndex, setHighlightedIndex] = useState(0)
  const [coords, setCoords] = useState<MenuCoords | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  const selectedOption = options.find(opt => opt.value === value)
  const selectedIndex = options.findIndex(opt => opt.value === value)

  const computeCoords = useCallback(() => {
    const trigger = triggerRef.current
    if (!trigger) return
    const rect = trigger.getBoundingClientRect()
    const margin = 8
    const gap = 6
    const maxDesiredHeight = 280
    const roomBelow = Math.max(0, window.innerHeight - rect.bottom - gap - margin)
    const roomAbove = Math.max(0, rect.top - gap - margin)
    const placement: 'bottom' | 'top' = roomBelow < 140 && roomAbove > roomBelow ? 'top' : 'bottom'
    const maxHeight = Math.max(96, Math.min(maxDesiredHeight, Math.floor(placement === 'top' ? roomAbove : roomBelow)))
    const width = Math.max(rect.width, minMenuWidth)
    const maxLeft = Math.max(margin, window.innerWidth - width - margin)
    const left = Math.max(margin, Math.min(rect.left, maxLeft))
    const estimatedHeight = Math.min(maxHeight, options.length * 36 + 12)
    const top = placement === 'top'
      ? Math.max(margin, rect.top - estimatedHeight - gap)
      : Math.min(window.innerHeight - margin - 96, rect.bottom + gap)
    setCoords({ top, left, width, maxHeight, placement })
  }, [minMenuWidth, options.length])

  useLayoutEffect(() => {
    if (!open) return
    computeCoords()
    const onReposition = () => computeCoords()
    window.addEventListener('resize', onReposition)
    window.addEventListener('scroll', onReposition, true)
    return () => {
      window.removeEventListener('resize', onReposition)
      window.removeEventListener('scroll', onReposition, true)
    }
  }, [open, computeCoords])

  useEffect(() => {
    if (disabled || !options.length) setOpen(false)
  }, [disabled, options.length])

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) return
      setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        setOpen(false)
        triggerRef.current?.focus()
      }
    }
    document.addEventListener('pointerdown', onPointerDown, true)
    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown, true)
      document.removeEventListener('keydown', onKeyDown, true)
    }
  }, [open])

  function openMenu() {
    if (disabled || !options.length) return
    setHighlightedIndex(selectedIndex >= 0 ? selectedIndex : 0)
    setOpen(true)
  }

  function choose(option: LiquidSelectOption) {
    if (option.disabled) return
    setOpen(false)
    if (option.value !== value) onChange(option.value)
    triggerRef.current?.focus()
  }

  function moveHighlight(delta: number) {
    if (!options.length) return
    let next = highlightedIndex
    for (let step = 0; step < options.length; step++) {
      next = (next + delta + options.length) % options.length
      if (!options[next]?.disabled) {
        setHighlightedIndex(next)
        return
      }
    }
  }

  function onTriggerKeyDown(event: React.KeyboardEvent<HTMLButtonElement>) {
    if (disabled) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      if (!open) openMenu()
      else moveHighlight(1)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      if (!open) openMenu()
      else moveHighlight(-1)
    } else if (event.key === 'Home' && open) {
      event.preventDefault()
      setHighlightedIndex(0)
    } else if (event.key === 'End' && open) {
      event.preventDefault()
      setHighlightedIndex(Math.max(0, options.length - 1))
    } else if (event.key === 'Enter' || event.key === ' ') {
      if (open) {
        event.preventDefault()
        event.stopPropagation()
        const picked = options[highlightedIndex]
        if (picked) choose(picked)
      }
    }
  }

  const portalTarget = typeof document !== 'undefined'
    ? findOpenDialog(triggerRef.current) ?? document.body
    : null

  return (
    <div className={`lg-select${compact ? ' is-compact' : ''}${className ? ` ${className}` : ''}`}>
      {/* Visually hidden native <select> preserves programmatic DOM/test queries without rendering native UI */}
      <select
        className="lg-select-native"
        aria-label={ariaLabel}
        tabIndex={-1}
        aria-hidden="true"
        disabled={disabled}
        value={value}
        onChange={event => onChange(event.target.value)}
      >
        {options.map(option => (
          <option key={option.value} value={option.value} disabled={option.disabled}>
            {option.label}
          </option>
        ))}
      </select>

      <button
        ref={triggerRef}
        type="button"
        className="lg-select-trigger"
        disabled={disabled}
        aria-label={ariaLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listboxId : undefined}
        onClick={() => (open ? setOpen(false) : openMenu())}
        onKeyDown={onTriggerKeyDown}
      >
        <span className="lg-select-value">
          {prefix && <span className="lg-select-prefix">{prefix}</span>}
          <span className="lg-select-label">{selectedOption?.label ?? placeholder}</span>
        </span>
        <svg className="lg-select-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="m7 10 5 5 5-5" />
        </svg>
      </button>

      {open && coords && portalTarget && createPortal(
        <div
          id={listboxId}
          ref={menuRef}
          className="lg-select-menu"
          role="listbox"
          aria-label={ariaLabel}
          data-placement={coords.placement}
          style={{
            top: `${coords.top}px`,
            left: `${coords.left}px`,
            width: `${coords.width}px`,
            maxHeight: `${coords.maxHeight}px`,
          }}
        >
          {options.map((option, index) => {
            const isSelected = option.value === value
            const isHighlighted = index === highlightedIndex
            return (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={isSelected}
                disabled={option.disabled}
                className={`lg-select-option${isHighlighted ? ' is-highlighted' : ''}`}
                onMouseEnter={() => setHighlightedIndex(index)}
                onMouseDown={event => event.preventDefault()}
                onClick={() => choose(option)}
              >
                <span className="lg-select-option-main">
                  <span className="lg-select-option-text">{option.label}</span>
                </span>
                {option.meta && <span className="lg-select-option-meta">{option.meta}</span>}
                {isSelected && (
                  <svg className="lg-select-check" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M5 13l4 4L19 7" />
                  </svg>
                )}
              </button>
            )
          })}
        </div>,
        portalTarget,
      )}
    </div>
  )
}

export default LiquidSelect
