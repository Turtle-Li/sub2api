import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { taskDisplay, taskPending, type StudioTask } from '../api'
import TaskCard from '../upstream/components/TaskCard'
import { createThumbnailLoader } from '../thumbnailLoader'
const previews = createThumbnailLoader(vi.fn(), new AbortController().signal)
const record = (jobStatus: StudioTask['job']['status'], itemStatus: string): StudioTask => ({ id: 'job/one', job: { id: 'job', status: jobStatus, model: 'fixture', error: { code: 'BATCH_FAILED', message: 'provider unavailable' } }, item: { custom_id: 'one', status: itemStatus, image_count: 0 } } as StudioTask)
describe('terminal batch / unfinished item display projection', () => {
 it.each(['succeeded', 'success', 'completed'])('labels the successful item status %s in Chinese', status => {
   expect(taskDisplay(record('completed', status)).label).toBe('已完成')
 })
 it.each([['cancelled', '已取消'], ['failed', '任务失败'], ['output_deleted', '结果已清理']] as const)('projects %s without mutating pending server items or retaining a spinner', (status, label) => {
   const task = record(status, 'pending')
   const html = renderToStaticMarkup(<TaskCard record={task} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />)
   expect(html).toContain(label); expect(html).not.toContain('animate-spin')
   expect(task.item.status).toBe('pending'); expect(taskPending(task)).toBe(false)
   if (status === 'failed') expect(html).toContain('provider unavailable')
 })
 it.each(['cancelled', 'failed', 'completed', 'output_deleted'] as const)('preserves explicit item successes and failures inside a %s batch', status => {
   expect(taskDisplay(record(status, 'success')).label).toBe('已完成')
   expect(taskDisplay(record(status, 'failed')).label).toBe('失败')
   expect(taskDisplay(record(status, 'failed')).message).toBeUndefined()
 })
 it('never promotes an unfinished item to success merely because the job completed', () => {
   expect(taskDisplay(record('completed', 'pending')).label).toBe('排队中')
 })
 it('separates cleaned output from a failure in the same batch', () => {
   const task = record('output_deleted', 'succeeded')
   const cleaned = { ...task, item: { ...task.item, image_count: 1 } }
   const failed = { ...task, item: { ...task.item, status: 'failed', error: { code: 'RESULT_DELIVERY_FAILED', message: 'result delivery error' } } }
   expect(taskDisplay(cleaned)).toMatchObject({ status: 'output_deleted', label: '图片已清理' })
   expect(taskDisplay(failed)).toMatchObject({ status: 'failed', message: '生成结果未能交付到作品库。' })
   const html = renderToStaticMarkup(<TaskCard record={cleaned} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />)
   expect(html).toContain('图片已清理')
   expect(html).toContain('disabled="" aria-label="继续改图"')
 })
 it('keeps the failure reason when an undelivered archive has passed its deadline', () => {
   const task = record('failed', 'result_available')
   expect(taskDisplay({ ...task, job: { ...task.job, output_expires_at: 1 } })).toMatchObject({
     status: 'failed', label: '任务失败', message: 'provider unavailable', canOpenArchive: false,
   })
 })
})
