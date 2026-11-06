import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { taskDisplay, taskPending, type StudioTask, type studioAPI } from '../api'
import TaskCard from '../upstream/components/TaskCard'
const record = (jobStatus: StudioTask['job']['status'], itemStatus: string): StudioTask => ({ id: 'job/one', job: { id: 'job', status: jobStatus, model: 'fixture', error: { code: 'BATCH_FAILED', message: 'provider unavailable' } }, item: { custom_id: 'one', status: itemStatus, image_count: 0 } } as StudioTask)
describe('terminal batch / unfinished item display projection', () => {
 it.each([['cancelled', '已取消'], ['failed', '任务失败'], ['output_deleted', '结果已清理']] as const)('projects %s without mutating pending server items or retaining a spinner', (status, label) => {
   const task = record(status, 'pending')
   const html = renderToStaticMarkup(<TaskCard record={task} api={{} as ReturnType<typeof studioAPI>} onOpen={vi.fn()} onEditOutputs={vi.fn()} />)
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
})
