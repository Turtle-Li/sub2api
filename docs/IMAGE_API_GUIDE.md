# 图片接口调用指南

本文面向使用中转服务的 API 用户，覆盖单次生图、异步生图和批量生图。所有示例都使用 OpenAI 兼容的 HTTP 接口。

## 公共约定

将 `https://relay.example.com` 替换为你的中转地址，将 `sk-...` 替换为 API Key：

```http
Authorization: Bearer sk-...
Content-Type: application/json
```

接口同时提供 `/v1/...` 和历史兼容的无前缀别名。下面统一使用 `/v1` 路径。API Key 必须属于已开启图片能力的分组；异步任务和批量任务查询时，使用提交任务的同一个 API Key。

## 1. 单次图片生成

### 生成图片

```http
POST /v1/images/generations
```

请求体沿用 OpenAI Images 格式，常用字段如下：

| 字段 | 说明 |
| --- | --- |
| `model` | 图片模型，例如 `gpt-image-1`、`gpt-image-2` 或已配置的兼容模型 |
| `prompt` | 图片描述 |
| `size` | 尺寸，例如 `1024x1024`、`1536x1024` |
| `n` | 输出数量；超过单次限制时请拆分请求 |
| `quality` | 模型支持时可用 `low`、`medium`、`high` |
| `background`、`output_format`、`output_compression` | 模型支持的原生选项 |

```bash
curl https://relay.example.com/v1/images/generations \
  -H 'Authorization: Bearer sk-...' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-image-1",
    "prompt": "一座冬季暴风雪中的灯塔",
    "size": "1536x1024",
    "quality": "high",
    "n": 1
  }'
```

需要上传参考图或蒙版时，使用 `multipart/form-data`，字段名保持 OpenAI 兼容格式：`model`、`prompt`、`image`（可重复）、`mask` 以及其他文本字段。

在当前中转配置中，JSON 请求的 `n > 1` 可能会自动转为异步任务并返回任务提交 envelope；如果客户端需要稳定的 `202 + task_id` 语义，请直接调用下面的显式异步接口。

## 2. 异步图片任务

异步接口先返回任务 ID，适合单次请求耗时较长、需要 `n > 1` 或不希望长时间占用 HTTP 连接的场景。

```http
POST /v1/images/generations/async
POST /v1/images/edits/async
GET  /v1/images/tasks/{task_id}
```

提交请求与同步接口使用相同的 JSON 或 multipart 格式，但不支持 `stream`。成功提交返回 `202 Accepted`：

```json
{
  "id": "imgtask_0123456789abcdef",
  "task_id": "imgtask_0123456789abcdef",
  "object": "image.generation.task",
  "status": "processing",
  "poll_url": "/v1/images/tasks/imgtask_0123456789abcdef"
}
```

使用同一个 API Key 轮询：

```bash
curl https://relay.example.com/v1/images/tasks/imgtask_0123456789abcdef \
  -H 'Authorization: Bearer sk-...'
```

任务终态为 `completed`、`failed` 或 `cancelled`。完成时 `result.data[].url` 指向对象存储中的图片；系统会移除大体积 `b64_json`，避免把图片正文长期放在任务缓存中。异步任务是原子结果：请求中的任一图片失败，任务整体会进入 `failed`，不会返回部分结果。需要逐项成功、逐项失败和失败重试时，应使用批量任务。

## 3. 批量图片任务

批量任务适合多条 prompt、不同参考图组合、部分失败后重试，以及统一下载结果。每次提交会创建一个独立任务，任务有自己的状态、明细、费用冻结、取消和下载生命周期。

### 路由

```http
GET    /v1/images/batches/models
POST   /v1/images/batches
GET    /v1/images/batches
GET    /v1/images/batches/{id}
GET    /v1/images/batches/{id}/items
GET    /v1/images/batches/{id}/items/{custom_id}/content
GET    /v1/images/batches/{id}/result-files
GET    /v1/images/batches/{id}/download
POST   /v1/images/batches/{id}/cancel
DELETE /v1/images/batches/{id}
DELETE /v1/images/batches/{id}/outputs
```

先查询当前 API Key 可用模型：

```bash
curl https://relay.example.com/v1/images/batches/models \
  -H 'Authorization: Bearer sk-...'
```

### 提交示例

```bash
curl https://relay.example.com/v1/images/batches \
  -H 'Authorization: Bearer sk-...' \
  -H 'Idempotency-Key: poster-20261004-001' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-image-2",
    "task_name": "产品海报",
    "image_size": "1K",
    "response_mime_type": "image/png",
    "items": [
      {
        "custom_id": "poster_001",
        "prompt": "白底产品海报，留出标题区域",
        "output_count": 1
      },
      {
        "custom_id": "poster_002",
        "prompt": "深色产品海报，带柔和轮廓光",
        "output_count": 2
      }
    ]
  }'
```

请求字段：

| 字段 | 说明 |
| --- | --- |
| `model` | 由 `/models` 返回并且对当前 API Key 可用的模型 |
| `task_name` | 可选，便于在任务列表中识别 |
| `image_size` | `1K`、`2K` 或 `4K`；2K/4K 会先生成 1K 源图，再进入公共超分流程 |
| `response_mime_type` | `image/png`、`image/jpeg` 或 `image/webp` |
| `items` | 至少一项；每项包含唯一 `custom_id` 和完整 `prompt` |
| `output_count` | 每项重复生成次数，默认 `1`，最多 `4` |
| `reference_images` | 每项的参考图，支持 PNG、JPEG、WebP 的 inline base64 数据 |
| `shared_reference_images` | 可选的公共参考图池；item 用 `shared_reference_id` 引用，适合大量复用同一张图 |

Gemini/Vertex 图片模型和 OpenAI/Image 模型都通过同一套批量接口提交。客户端不需要传内部通道名；以 `/models` 返回的 `model` 为准。

提交成功后使用 `GET /v1/images/batches/{id}` 轮询。需要列出历史任务时，`GET /v1/images/batches` 支持 `limit`（默认 20，最大 100）、`cursor`、`status`、`task_name`、`downloaded`、`from` 和 `to` 参数；明细接口支持 `status`、`limit`（默认 100，最大 500）和 `cursor`。

### 限制与结果下载

- 单个 item 最多生成 4 张；单个批量任务在 `1K`、`2K`、`4K` 下分别最多生成 50、15、10 张。超过上限请拆成多个任务。
- Gemini 2.5 Flash Image 每个 item 最多 3 张参考图；Gemini 3 系列最多 14 张；`gpt-image-*` 最多 16 张 inline 参考图。
- 一个任务展开后的参考图附件最多 1000 个，inline 参考图解码后总量最多 128 MB。
- `GET /v1/images/batches/{id}` 查看任务状态和汇总计数；`GET /v1/images/batches/{id}/items` 查看每条 prompt 的结果与错误。
- `GET /v1/images/batches/{id}/items/{custom_id}/content?image_index=0` 可读取单张结果；使用私有归档时，客户端应改用 `result-files`。
- 优先调用 `GET /v1/images/batches/{id}/result-files` 获取短时、只读的 JSONL 文件地址，在本地解码图片并生成 ZIP。不要把签名地址写入日志或长期保存。
- `result-files` 返回 `expires_at` 和 `data[].{index,name,size,content_type,url}`；地址过期后重新调用接口获取，不要修改签名参数。
- `GET /batches/{id}/download?status=succeeded&max_items=50` 是兼容旧客户端的服务端 ZIP 下载入口；新客户端应优先使用 `result-files`，避免大量图片正文经过中转服务器。ZIP 可能包含 `manifest.json` 和 `errors.json`。
- `DELETE /v1/images/batches/{id}/outputs` 删除输出文件；`DELETE /v1/images/batches/{id}` 仅删除已结束任务在列表中的记录，账务记录仍保留。
- 任务完成后可以只提交失败项重试；已成功的图片不会重复扣费。取消任务前请注意，已经确认成功的图片仍会按成功项结算。

## 4. 选择哪一种接口

| 场景 | 推荐接口 |
| --- | --- |
| 单张或少量图片，调用方希望立即拿到 JSON | `/v1/images/generations` 或 `/v1/images/edits` |
| 单次请求较慢，或者需要一个可轮询的任务 ID | `/v1/images/generations/async`、`/v1/images/edits/async` |
| 多条 prompt、不同参考图、逐项结果、失败重试、统一下载 | `/v1/images/batches` |

批量任务保留“一个提交对应一个任务”的设计是有意的：它能承载排队、费用冻结、逐项状态、部分成功和重试。页面继续使用任务列表和详情抽屉更适合这个生命周期；若未来要做类似 ChatGPT 的连续创作体验，可以在此基础上增加“新建任务”快捷入口和最近任务卡片，不需要把批量任务改造成聊天消息流。
