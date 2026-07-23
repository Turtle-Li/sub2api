export interface BatchImageArchiveFileCapability {
  index: number
  name: string
  size: number
  content_type: string
  url: string
}

export type BatchImageArchiveCapabilityProvider =
  () => Promise<BatchImageArchiveFileCapability[]>

export interface BatchImageArchiveItem {
  custom_id: string
  status: 'success' | 'failed'
  mime_type: string | null
  file_extension: string | null
  image_count: number
  error: {
    code: string
    message: string
    source: 'provider'
  } | null
}

export interface BatchImageArchivePreview extends BatchImageArchiveItem {
  image: Blob | null
}

export interface BatchImageArchiveZipOptions {
  batchId: string
  model: string
  itemCount: number
  successCount: number
  failCount: number
  expectedCustomIds?: Iterable<string>
}

export interface BatchImageArchiveZipResult {
  blob: Blob
  items: BatchImageArchiveItem[]
  fileCount: number
}

interface ParsedInlineImage {
  mimeType: string
  extension: string
  base64Data: string
}

interface ParsedResultLine {
  item: BatchImageArchiveItem
  images: ParsedInlineImage[]
}

type JsonRecord = Record<string, unknown>
type ResultLineVisitor = (parsed: ParsedResultLine) => boolean | void | Promise<boolean | void>

const MAX_ARCHIVE_FILES = 20
const MAX_JSONL_LINE_BYTES = 96 * 1024 * 1024
const MAX_IMAGE_BYTES = 64 * 1024 * 1024
const ARCHIVE_COS_HOST = 'image-1309919944.cos.ap-shanghai.myqcloud.com'
const ZIP32_MAX = 0xffffffff
const ZIP_MAX_ENTRIES = 0xffff
const UTF8_FLAG = 0x0800
const encoder = new TextEncoder()

export class BatchImageArchiveError extends Error {
  readonly code: string
  readonly status?: number

  constructor(code: string, message: string, status?: number) {
    super(message)
    this.name = 'BatchImageArchiveError'
    this.code = code
    this.status = status
  }
}

function asRecord(value: unknown): JsonRecord | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as JsonRecord
}

function stringValue(value: unknown): string {
  if (typeof value === 'string') return value.trim()
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return ''
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    const resolved = stringValue(value)
    if (resolved) return resolved
  }
  return ''
}

function nestedRecord(value: unknown, ...keys: string[]): JsonRecord | null {
  let current = asRecord(value)
  for (const key of keys) {
    if (!current) return null
    current = asRecord(current[key])
  }
  return current
}

function nestedValue(value: unknown, ...keys: string[]): unknown {
  let current: unknown = value
  for (const key of keys) {
    const record = asRecord(current)
    if (!record) return undefined
    current = record[key]
  }
  return current
}

function customIdFromResult(record: JsonRecord): string {
  const request = asRecord(record.request)
  const instance = asRecord(record.instance)
  return firstString(
    record.key,
    record.custom_id,
    record.customId,
    request?.key,
    request?.custom_id,
    request?.customId,
    instance?.key,
    instance?.custom_id,
    instance?.customId,
  )
}

function normalizeMimeType(value: unknown): string {
  const mime = stringValue(value).split(';', 1)[0].toLowerCase()
  return mime === 'image/jpg' ? 'image/jpeg' : mime
}

function extensionForMimeType(mimeType: string): string {
  switch (mimeType) {
    case 'image/png':
      return 'png'
    case 'image/jpeg':
      return 'jpg'
    case 'image/webp':
      return 'webp'
    case 'image/gif':
      return 'gif'
    default:
      return ''
  }
}

function extractImagesFromCandidates(raw: unknown): ParsedInlineImage[] {
  if (!Array.isArray(raw)) return []
  const images: ParsedInlineImage[] = []
  for (const candidateValue of raw) {
    const parts = nestedValue(candidateValue, 'content', 'parts')
    if (!Array.isArray(parts)) continue
    for (const partValue of parts) {
      const part = asRecord(partValue)
      const inline = asRecord(part?.inlineData) || asRecord(part?.inline_data)
      if (!inline) continue
      const base64Data = stringValue(inline.data)
      const mimeType = normalizeMimeType(inline.mimeType ?? inline.mime_type)
      const extension = extensionForMimeType(mimeType)
      if (!base64Data || !extension) continue
      images.push({ mimeType, extension, base64Data })
    }
  }
  return images
}

function providerFailure(record: JsonRecord): { code: string, message: string } | null {
  const failure =
    asRecord(record.status)
    || asRecord(record.error)
    || nestedRecord(record, 'response', 'error')
  if (!failure) return null
  const rawCode = firstString(failure.code, failure.status)
  const message = firstString(failure.message, failure.details) || 'provider returned an item error'
  const normalized = `${rawCode} ${message}`.toLowerCase()
  let code = 'PROVIDER_ITEM_FAILED'
  if (/(safety|policy|blocked|prohibited)/.test(normalized)) code = 'SAFETY_BLOCKED'
  else if (/(invalid_argument|invalid argument|bad request)/.test(normalized)) code = 'INVALID_ARGUMENT'
  else if (/(quota|rate|resource_exhausted|too many requests)/.test(normalized)) code = 'PROVIDER_RATE_LIMITED'
  return { code, message: message.slice(0, 500) }
}

export function parseBatchImageArchiveResultLine(line: string): ParsedResultLine {
  let record: JsonRecord
  try {
    record = asRecord(JSON.parse(line)) || {}
  } catch {
    throw new BatchImageArchiveError('ARCHIVE_JSONL_INVALID', 'The result archive contains invalid JSONL.')
  }
  const customId = customIdFromResult(record)
  if (!customId) {
    throw new BatchImageArchiveError('ARCHIVE_CUSTOM_ID_MISSING', 'A result line is missing its item ID.')
  }
  const images = [
    ...extractImagesFromCandidates(nestedValue(record, 'response', 'candidates')),
    ...extractImagesFromCandidates(record.candidates),
  ]
  if (images.length > 0) {
    return {
      item: {
        custom_id: customId,
        status: 'success',
        mime_type: images[0].mimeType,
        file_extension: images[0].extension,
        image_count: images.length,
        error: null,
      },
      images,
    }
  }

  const failure = providerFailure(record)
  const hasProviderResponse =
    Object.prototype.hasOwnProperty.call(record, 'response')
    || Object.prototype.hasOwnProperty.call(record, 'candidates')
  const code = failure?.code || (hasProviderResponse ? 'EMPTY_IMAGE_OUTPUT' : 'PROVIDER_ITEM_FAILED')
  const message =
    failure?.message
    || (hasProviderResponse
      ? 'provider response contained no image output'
      : 'provider result line contained no image output')
  return {
    item: {
      custom_id: customId,
      status: 'failed',
      mime_type: null,
      file_extension: null,
      image_count: 0,
      error: { code, message, source: 'provider' },
    },
    images: [],
  }
}

function validateCapabilities(files: BatchImageArchiveFileCapability[]): BatchImageArchiveFileCapability[] {
  if (!Array.isArray(files) || files.length === 0 || files.length > MAX_ARCHIVE_FILES) {
    throw new BatchImageArchiveError('ARCHIVE_CAPABILITIES_INVALID', 'The result archive file list is invalid.')
  }
  const sorted = [...files].sort((left, right) => left.index - right.index)
  for (let index = 0; index < sorted.length; index += 1) {
    const file = sorted[index]
    if (
      file.index !== index
      || !Number.isSafeInteger(file.size)
      || file.size <= 0
      || file.size > ZIP32_MAX
      || normalizeMimeType(file.content_type) !== 'application/x-ndjson'
    ) {
      throw new BatchImageArchiveError('ARCHIVE_CAPABILITIES_INVALID', 'The result archive file list is invalid.')
    }
    let url: URL
    try {
      url = new URL(file.url)
    } catch {
      throw new BatchImageArchiveError('ARCHIVE_CAPABILITIES_INVALID', 'The result archive URL is invalid.')
    }
    if (
      url.protocol !== 'https:'
      || url.hostname.toLowerCase() !== ARCHIVE_COS_HOST
      || url.username
      || url.password
      || url.hash
      || !url.search
    ) {
      throw new BatchImageArchiveError('ARCHIVE_CAPABILITIES_INVALID', 'The result archive URL is invalid.')
    }
  }
  return sorted
}

async function fetchArchiveFile(file: BatchImageArchiveFileCapability): Promise<Response> {
  let response: Response
  try {
    response = await fetch(file.url, {
      method: 'GET',
      headers: { Accept: 'application/x-ndjson, application/json' },
      credentials: 'omit',
      cache: 'no-store',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
    })
  } catch {
    throw new BatchImageArchiveError(
      'ARCHIVE_NETWORK_FAILED',
      'The browser could not read the private result archive. Check COS CORS and network access.',
    )
  }
  if (!response.ok || !response.body) {
    response.body?.cancel().catch(() => undefined)
    throw new BatchImageArchiveError(
      'ARCHIVE_DOWNLOAD_FAILED',
      'The private result archive could not be downloaded.',
      response.status,
    )
  }
  return response
}

async function visitResponseLines(
  response: Response,
  expectedSize: number,
  visitor: ResultLineVisitor,
): Promise<boolean> {
  if (!response.body) {
    throw new BatchImageArchiveError('ARCHIVE_DOWNLOAD_FAILED', 'The private result archive has no response body.')
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let receivedBytes = 0
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      receivedBytes += value.byteLength
      if (receivedBytes > expectedSize) {
        throw new BatchImageArchiveError('ARCHIVE_SIZE_MISMATCH', 'The result archive is larger than its signed metadata.')
      }
      buffer += decoder.decode(value, { stream: true })
      if (buffer.length > MAX_JSONL_LINE_BYTES && !buffer.includes('\n')) {
        throw new BatchImageArchiveError('ARCHIVE_LINE_TOO_LARGE', 'A result archive line is too large.')
      }
      let newline = buffer.indexOf('\n')
      while (newline >= 0) {
        const line = buffer.slice(0, newline).trim()
        buffer = buffer.slice(newline + 1)
        if (line && await visitor(parseBatchImageArchiveResultLine(line)) === true) {
          await reader.cancel()
          return true
        }
        newline = buffer.indexOf('\n')
      }
    }
    buffer += decoder.decode()
    const finalLine = buffer.trim()
    if (finalLine && await visitor(parseBatchImageArchiveResultLine(finalLine)) === true) {
      return true
    }
    if (receivedBytes !== expectedSize) {
      throw new BatchImageArchiveError('ARCHIVE_SIZE_MISMATCH', 'The result archive size does not match its signed metadata.')
    }
    return false
  } finally {
    reader.releaseLock()
  }
}

async function visitArchiveLines(
  capabilitiesProvider: BatchImageArchiveCapabilityProvider,
  visitor: ResultLineVisitor,
): Promise<boolean> {
  let files = validateCapabilities(await capabilitiesProvider())
  for (let position = 0; position < files.length; position += 1) {
    let file = files[position]
    let response: Response
    try {
      response = await fetchArchiveFile(file)
    } catch (error) {
      const status = error instanceof BatchImageArchiveError ? error.status : undefined
      if (status !== 401 && status !== 403) throw error
      files = validateCapabilities(await capabilitiesProvider())
      file = files[position]
      response = await fetchArchiveFile(file)
    }
    if (await visitResponseLines(response, file.size, visitor)) return true
  }
  return false
}

function decodeBase64(value: string): Uint8Array {
  const compact = value.replace(/\s+/g, '')
  if (!compact || compact.length > Math.ceil(MAX_IMAGE_BYTES / 3) * 4 + 4) {
    throw new BatchImageArchiveError('ARCHIVE_IMAGE_TOO_LARGE', 'A generated image is too large to decode safely.')
  }
  const estimated = Math.floor(compact.length * 3 / 4)
    - (compact.endsWith('==') ? 2 : compact.endsWith('=') ? 1 : 0)
  if (estimated <= 0 || estimated > MAX_IMAGE_BYTES) {
    throw new BatchImageArchiveError('ARCHIVE_IMAGE_TOO_LARGE', 'A generated image is too large to decode safely.')
  }
  const output = new Uint8Array(estimated)
  let outputOffset = 0
  try {
    for (let offset = 0; offset < compact.length; offset += 32 * 1024) {
      const chunk = atob(compact.slice(offset, Math.min(compact.length, offset + 32 * 1024)))
      for (let index = 0; index < chunk.length; index += 1) {
        output[outputOffset++] = chunk.charCodeAt(index)
      }
    }
  } catch {
    throw new BatchImageArchiveError('ARCHIVE_IMAGE_BASE64_INVALID', 'A generated image has invalid Base64 data.')
  }
  if (outputOffset !== output.length) {
    throw new BatchImageArchiveError('ARCHIVE_IMAGE_BASE64_INVALID', 'A generated image has invalid Base64 data.')
  }
  return output
}

function hasRasterSignature(bytes: Uint8Array, mimeType: string): boolean {
  switch (mimeType) {
    case 'image/png':
      return bytes.length >= 8
        && bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47
        && bytes[4] === 0x0d && bytes[5] === 0x0a && bytes[6] === 0x1a && bytes[7] === 0x0a
    case 'image/jpeg':
      return bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff
    case 'image/webp':
      return bytes.length >= 12
        && bytes[0] === 0x52 && bytes[1] === 0x49 && bytes[2] === 0x46 && bytes[3] === 0x46
        && bytes[8] === 0x57 && bytes[9] === 0x45 && bytes[10] === 0x42 && bytes[11] === 0x50
    case 'image/gif':
      return bytes.length >= 6
        && bytes[0] === 0x47 && bytes[1] === 0x49 && bytes[2] === 0x46
        && bytes[3] === 0x38 && (bytes[4] === 0x37 || bytes[4] === 0x39) && bytes[5] === 0x61
    default:
      return false
  }
}

function decodeImage(image: ParsedInlineImage): Uint8Array {
  const bytes = decodeBase64(image.base64Data)
  if (!hasRasterSignature(bytes, image.mimeType)) {
    throw new BatchImageArchiveError('ARCHIVE_IMAGE_SIGNATURE_INVALID', 'A generated image does not match its MIME type.')
  }
  return bytes
}

export async function loadBatchImageArchivePreview(
  capabilitiesProvider: BatchImageArchiveCapabilityProvider,
  customId: string,
): Promise<BatchImageArchivePreview> {
  let result: BatchImageArchivePreview | null = null
  await visitArchiveLines(capabilitiesProvider, (parsed) => {
    if (parsed.item.custom_id !== customId) return false
    const firstImage = parsed.images[0]
    result = {
      ...parsed.item,
      image: firstImage ? new Blob([decodeImage(firstImage)], { type: firstImage.mimeType }) : null,
    }
    return true
  })
  if (!result) {
    return {
      custom_id: customId,
      status: 'failed',
      mime_type: null,
      file_extension: null,
      image_count: 0,
      error: {
        code: 'RESULT_MISSING',
        message: 'provider result was not found for item',
        source: 'provider',
      },
      image: null,
    }
  }
  return result
}

export async function scanBatchImageArchiveItems(
  capabilitiesProvider: BatchImageArchiveCapabilityProvider,
): Promise<BatchImageArchiveItem[]> {
  const seen = new Set<string>()
  const items: BatchImageArchiveItem[] = []
  await visitArchiveLines(capabilitiesProvider, ({ item }) => {
    if (seen.has(item.custom_id)) {
      throw new BatchImageArchiveError('ARCHIVE_DUPLICATE_ITEM', 'The result archive contains a duplicate item ID.')
    }
    seen.add(item.custom_id)
    items.push(item)
  })
  return items
}

function safeFilenameBase(value: string): string {
  const withoutControls = [...value.normalize('NFKC')]
    .map(character => {
      const code = character.codePointAt(0) || 0
      return code <= 0x1f || code === 0x7f ? '_' : character
    })
    .join('')
  const cleaned = withoutControls
    .replace(/[/\\:*?"<>|]+/g, '_')
    .replace(/^\.+/, '_')
    .trim()
    .slice(0, 120)
  return cleaned || 'image'
}

function uniqueImageFilename(
  customId: string,
  imageIndex: number,
  extension: string,
  used: Set<string>,
): string {
  const base = `${safeFilenameBase(customId)}${imageIndex > 0 ? `_${imageIndex + 1}` : ''}`
  let candidate = `images/${base}.${extension}`
  let suffix = 2
  while (used.has(candidate)) {
    candidate = `images/${base}_${suffix}.${extension}`
    suffix += 1
  }
  used.add(candidate)
  return candidate
}

let crcTable: Uint32Array | null = null

function crc32(data: Uint8Array): number {
  if (!crcTable) {
    crcTable = new Uint32Array(256)
    for (let index = 0; index < 256; index += 1) {
      let value = index
      for (let bit = 0; bit < 8; bit += 1) {
        value = (value & 1) ? (0xedb88320 ^ (value >>> 1)) : (value >>> 1)
      }
      crcTable[index] = value >>> 0
    }
  }
  let checksum = 0xffffffff
  for (const byte of data) {
    checksum = crcTable[(checksum ^ byte) & 0xff] ^ (checksum >>> 8)
  }
  return (checksum ^ 0xffffffff) >>> 0
}

function dosDateTime(date = new Date()): { date: number, time: number } {
  const year = Math.min(2107, Math.max(1980, date.getFullYear()))
  return {
    date: ((year - 1980) << 9) | ((date.getMonth() + 1) << 5) | date.getDate(),
    time: (date.getHours() << 11) | (date.getMinutes() << 5) | Math.floor(date.getSeconds() / 2),
  }
}

class StoredZipBuilder {
  private readonly parts: BlobPart[] = []
  private readonly centralParts: Uint8Array[] = []
  private readonly timestamp = dosDateTime()
  private offset = 0
  private count = 0

  add(name: string, data: Uint8Array) {
    if (this.count >= ZIP_MAX_ENTRIES || data.byteLength > ZIP32_MAX) {
      throw new BatchImageArchiveError('ARCHIVE_ZIP_TOO_LARGE', 'The local ZIP exceeds the ZIP32 limit.')
    }
    const nameBytes = encoder.encode(name)
    if (nameBytes.byteLength > 0xffff) {
      throw new BatchImageArchiveError('ARCHIVE_FILENAME_TOO_LONG', 'A generated filename is too long.')
    }
    const nextOffset = this.offset + 30 + nameBytes.byteLength + data.byteLength
    if (nextOffset > ZIP32_MAX) {
      throw new BatchImageArchiveError('ARCHIVE_ZIP_TOO_LARGE', 'The local ZIP exceeds the ZIP32 limit.')
    }
    const checksum = crc32(data)
    const local = new Uint8Array(30 + nameBytes.byteLength)
    const localView = new DataView(local.buffer)
    localView.setUint32(0, 0x04034b50, true)
    localView.setUint16(4, 20, true)
    localView.setUint16(6, UTF8_FLAG, true)
    localView.setUint16(8, 0, true)
    localView.setUint16(10, this.timestamp.time, true)
    localView.setUint16(12, this.timestamp.date, true)
    localView.setUint32(14, checksum, true)
    localView.setUint32(18, data.byteLength, true)
    localView.setUint32(22, data.byteLength, true)
    localView.setUint16(26, nameBytes.byteLength, true)
    localView.setUint16(28, 0, true)
    local.set(nameBytes, 30)
    this.parts.push(local, data)

    const central = new Uint8Array(46 + nameBytes.byteLength)
    const centralView = new DataView(central.buffer)
    centralView.setUint32(0, 0x02014b50, true)
    centralView.setUint16(4, 20, true)
    centralView.setUint16(6, 20, true)
    centralView.setUint16(8, UTF8_FLAG, true)
    centralView.setUint16(10, 0, true)
    centralView.setUint16(12, this.timestamp.time, true)
    centralView.setUint16(14, this.timestamp.date, true)
    centralView.setUint32(16, checksum, true)
    centralView.setUint32(20, data.byteLength, true)
    centralView.setUint32(24, data.byteLength, true)
    centralView.setUint16(28, nameBytes.byteLength, true)
    centralView.setUint16(30, 0, true)
    centralView.setUint16(32, 0, true)
    centralView.setUint16(34, 0, true)
    centralView.setUint16(36, 0, true)
    centralView.setUint32(38, 0, true)
    centralView.setUint32(42, this.offset, true)
    central.set(nameBytes, 46)
    this.centralParts.push(central)
    this.offset = nextOffset
    this.count += 1
  }

  finish(): Blob {
    const centralOffset = this.offset
    const centralSize = this.centralParts.reduce((sum, part) => sum + part.byteLength, 0)
    if (centralOffset + centralSize + 22 > ZIP32_MAX) {
      throw new BatchImageArchiveError('ARCHIVE_ZIP_TOO_LARGE', 'The local ZIP exceeds the ZIP32 limit.')
    }
    const end = new Uint8Array(22)
    const view = new DataView(end.buffer)
    view.setUint32(0, 0x06054b50, true)
    view.setUint16(4, 0, true)
    view.setUint16(6, 0, true)
    view.setUint16(8, this.count, true)
    view.setUint16(10, this.count, true)
    view.setUint32(12, centralSize, true)
    view.setUint32(16, centralOffset, true)
    view.setUint16(20, 0, true)
    return new Blob([...this.parts, ...this.centralParts, end], { type: 'application/zip' })
  }
}

export async function buildBatchImageArchiveZip(
  capabilitiesProvider: BatchImageArchiveCapabilityProvider,
  options: BatchImageArchiveZipOptions,
): Promise<BatchImageArchiveZipResult> {
  const builder = new StoredZipBuilder()
  const expected = options.expectedCustomIds ? new Set(options.expectedCustomIds) : null
  if (expected && expected.size !== options.itemCount) {
    throw new BatchImageArchiveError('ARCHIVE_ITEM_COUNT_MISMATCH', 'The task item list is incomplete.')
  }
  const seen = new Set<string>()
  const usedNames = new Set<string>()
  const items: BatchImageArchiveItem[] = []
  const manifestFiles: Array<{
    custom_id: string
    filename: string
    mime_type: string
    image_index: number
  }> = []
  const errors: Array<{ custom_id: string, code: string, message: string }> = []

  await visitArchiveLines(capabilitiesProvider, (parsed) => {
    const { item } = parsed
    if (seen.has(item.custom_id)) {
      throw new BatchImageArchiveError('ARCHIVE_DUPLICATE_ITEM', 'The result archive contains a duplicate item ID.')
    }
    if (expected && !expected.has(item.custom_id)) {
      throw new BatchImageArchiveError('ARCHIVE_UNKNOWN_ITEM', 'The result archive contains an unexpected item ID.')
    }
    seen.add(item.custom_id)
    items.push(item)
    if (item.status === 'failed') {
      errors.push({
        custom_id: item.custom_id,
        code: item.error?.code || 'PROVIDER_ITEM_FAILED',
        message: item.error?.message || 'provider returned an item error',
      })
      return
    }
    parsed.images.forEach((image, imageIndex) => {
      const filename = uniqueImageFilename(item.custom_id, imageIndex, image.extension, usedNames)
      builder.add(filename, decodeImage(image))
      manifestFiles.push({
        custom_id: item.custom_id,
        filename,
        mime_type: image.mimeType,
        image_index: imageIndex,
      })
    })
  })

  if (expected) {
    for (const customId of expected) {
      if (seen.has(customId)) continue
      const missing: BatchImageArchiveItem = {
        custom_id: customId,
        status: 'failed',
        mime_type: null,
        file_extension: null,
        image_count: 0,
        error: {
          code: 'RESULT_MISSING',
          message: 'provider result was not found for item',
          source: 'provider',
        },
      }
      seen.add(customId)
      items.push(missing)
      errors.push({
        custom_id: customId,
        code: 'RESULT_MISSING',
        message: 'provider result was not found for item',
      })
    }
  }
  if (
    seen.size !== options.itemCount
  ) {
    throw new BatchImageArchiveError('ARCHIVE_ITEM_COUNT_MISMATCH', 'The result archive item count does not match the job.')
  }
  const successCount = items.filter(item => item.status === 'success').length
  const failCount = items.length - successCount
  if (successCount !== options.successCount || failCount !== options.failCount) {
    throw new BatchImageArchiveError('ARCHIVE_COMPLETION_MISMATCH', 'The result archive counts do not match Vertex completion statistics.')
  }

  builder.add('manifest.json', encoder.encode(`${JSON.stringify({
    batch_id: options.batchId,
    model: options.model,
    item_count: options.itemCount,
    success_count: successCount,
    fail_count: failCount,
    files: manifestFiles,
  }, null, 2)}\n`))
  builder.add('errors.json', encoder.encode(`${JSON.stringify(errors, null, 2)}\n`))
  return {
    blob: builder.finish(),
    items,
    fileCount: manifestFiles.length,
  }
}
