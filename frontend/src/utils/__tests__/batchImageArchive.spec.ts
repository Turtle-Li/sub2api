import { describe, expect, it, vi } from 'vitest'

import {
  buildBatchImageArchiveZip,
  loadBatchImageArchivePreview,
  parseBatchImageArchiveResultLine,
  scanBatchImageArchiveItems,
  type BatchImageArchiveFileCapability,
} from '../batchImageArchive'

const pngBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB'

function capability(size: number): BatchImageArchiveFileCapability {
  return {
    index: 0,
    name: 'result.jsonl',
    size,
    content_type: 'application/x-ndjson',
    url: 'https://image-1309919944.cos.ap-shanghai.myqcloud.com/result.jsonl?signature=secret',
  }
}

function archiveBody(lines: string[]) {
  return `${lines.join('\n')}\n`
}

function responseFor(body: string) {
  return new Response(body, {
    status: 200,
    headers: { 'content-type': 'application/x-ndjson' },
  })
}

describe('batch image private archive', () => {
  it('parses camelCase and snake_case image parts without decoding them', () => {
    const camel = parseBatchImageArchiveResultLine(JSON.stringify({
      key: 'cover',
      response: {
        candidates: [{
          content: { parts: [{ inlineData: { mimeType: 'image/png', data: pngBase64 } }] },
        }],
      },
    }))
    const snake = parseBatchImageArchiveResultLine(JSON.stringify({
      custom_id: 'detail',
      candidates: [{
        content: { parts: [{ inline_data: { mime_type: 'image/webp', data: 'AAAA' } }] },
      }],
    }))
    expect(camel.item).toMatchObject({ custom_id: 'cover', status: 'success', image_count: 1 })
    expect(snake.item).toMatchObject({ custom_id: 'detail', mime_type: 'image/webp' })
  })

  it('maps provider failures to safe item metadata', () => {
    const parsed = parseBatchImageArchiveResultLine(JSON.stringify({
      key: 'blocked',
      error: { status: 'FAILED_PRECONDITION', message: 'blocked by safety policy' },
    }))
    expect(parsed.item).toMatchObject({
      custom_id: 'blocked',
      status: 'failed',
      error: { code: 'SAFETY_BLOCKED', source: 'provider' },
    })
  })

  it('fetches COS without credentials or Authorization and scans locally', async () => {
    const body = archiveBody([
      JSON.stringify({ key: 'bad', error: { message: 'bad prompt' } }),
    ])
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(responseFor(body))
    const items = await scanBatchImageArchiveItems(async () => [capability(new TextEncoder().encode(body).byteLength)])
    expect(items).toHaveLength(1)
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.credentials).toBe('omit')
    expect(init.referrerPolicy).toBe('no-referrer')
    expect(new Headers(init.headers).has('Authorization')).toBe(false)
    fetchMock.mockRestore()
  })

  it('rejects signed capabilities for any other COS bucket', async () => {
    const file = capability(10)
    file.url = 'https://other-1309919944.cos.ap-shanghai.myqcloud.com/result.jsonl?signature=secret'
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    await expect(scanBatchImageArchiveItems(async () => [file]))
      .rejects.toMatchObject({ code: 'ARCHIVE_CAPABILITIES_INVALID' })
    expect(fetchMock).not.toHaveBeenCalled()
    fetchMock.mockRestore()
  })

  it('stops a preview scan after the requested line', async () => {
    const validPng = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2nT8AAAAASUVORK5CYII='
    const body = archiveBody([
      JSON.stringify({
        key: 'target',
        response: { candidates: [{ content: { parts: [{ inlineData: { mimeType: 'image/png', data: validPng } }] } }] },
      }),
      '{invalid trailing line',
    ])
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(responseFor(body))
    const preview = await loadBatchImageArchivePreview(
      async () => [capability(new TextEncoder().encode(body).byteLength)],
      'target',
    )
    expect(preview.status).toBe('success')
    expect(preview.image?.type).toBe('image/png')
    vi.restoreAllMocks()
  })

  it('builds a valid stored ZIP with manifest and errors', async () => {
    const validPng = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2nT8AAAAASUVORK5CYII='
    const body = archiveBody([
      JSON.stringify({
        key: 'ok',
        response: { candidates: [{ content: { parts: [{ inlineData: { mimeType: 'image/png', data: validPng } }] } }] },
      }),
      JSON.stringify({ key: 'bad', status: { code: 'INVALID_ARGUMENT', message: 'bad prompt' } }),
    ])
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(responseFor(body))
    const result = await buildBatchImageArchiveZip(
      async () => [capability(new TextEncoder().encode(body).byteLength)],
      {
        batchId: 'imgbatch_test',
        model: 'gemini-image',
        itemCount: 2,
        successCount: 1,
        failCount: 1,
        expectedCustomIds: ['ok', 'bad'],
      },
    )
    const bytes = new Uint8Array(await new Promise<ArrayBuffer>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(reader.result as ArrayBuffer)
      reader.onerror = () => reject(reader.error)
      reader.readAsArrayBuffer(result.blob)
    }))
    expect(new DataView(bytes.buffer).getUint32(0, true)).toBe(0x04034b50)
    expect(new TextDecoder().decode(bytes)).toContain('manifest.json')
    expect(new TextDecoder().decode(bytes)).toContain('errors.json')
    expect(result.fileCount).toBe(1)
    vi.restoreAllMocks()
  })
})
