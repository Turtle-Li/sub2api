/**
 * 站点的形状语言取自 mark：龟甲的六边形甲片，加一条穿过它的轨道。
 * 页面上凡是需要「标记 / 节点 / 状态点」的地方一律用六边形，不用圆点。
 */

/** 尖顶六边形，返回 SVG polygon 的 points 字符串。 */
export function hexPoints(cx: number, cy: number, r: number): string {
  return Array.from({ length: 6 }, (_, i) => {
    const angle = (Math.PI / 180) * (60 * i - 30)
    return `${(cx + r * Math.cos(angle)).toFixed(2)},${(cy + r * Math.sin(angle)).toFixed(2)}`
  }).join(' ')
}

/** 中心甲片加周围六片，间距留出缝，读起来才像龟甲而不是蜂窝。 */
export function shellPlates(radius = 46, gap = 6): Array<{ cx: number; cy: number }> {
  const spacing = Math.sqrt(3) * (radius + gap)
  return [
    { cx: 0, cy: 0 },
    ...Array.from({ length: 6 }, (_, i) => {
      const angle = (Math.PI / 180) * (60 * i)
      return { cx: spacing * Math.cos(angle), cy: spacing * Math.sin(angle) }
    }),
  ]
}

/** 倾斜椭圆轨道上的点，用于放轨道节点。 */
export function orbitNodes(
  degrees: number[],
  rx = 300,
  ry = 108,
  tiltDeg = -20,
): Array<{ x: number; y: number }> {
  const tilt = (Math.PI / 180) * tiltDeg
  return degrees.map((deg) => {
    const t = (Math.PI / 180) * deg
    const x = rx * Math.cos(t)
    const y = ry * Math.sin(t)
    return {
      x: x * Math.cos(tilt) - y * Math.sin(tilt),
      y: x * Math.sin(tilt) + y * Math.cos(tilt),
    }
  })
}
