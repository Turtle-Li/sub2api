# 生图历史来源与缩略图冷读核对

状态：FIXED_LOCALLY，已实现持久化修复；未提交、推送或部署。
环境：本地 image-studio-gallery-optimization 工作树；未连接生产、未读真实
用户图片。原优化基线06b619a2，本记录针对其上未提交的本地实现。

Bug: STUDIO-THUMBNAIL-COLD-READ-20261108

以下为修复前证据，行号对应当时内存版实现。修复后契约与验证见
`../../features/IMAGE_STUDIO_PERSISTENT_THUMBNAILS_20261108.md`：生成时存云端
缩略图、历史批次一次补齐、冷启动直接读已存 JPEG、鉴权后 ETag/304、删除
写入围栏。保留原分析，避免把先前浏览器请求数误当作后端源读取证据。

现象：浏览器虽然只拿缩略图，服务器在缓存未命中时仍读取原图；淘汰、重启
或另一实例上会再次读取。上一轮“原图请求为零”只覆盖浏览器→本地合成 API，
没有覆盖服务器→供应商/对象存储，不能用来证明整条链路没有原图下载。

复现条件：请求一张没有内存缓存的成功图片的 thumbnail；服务重启、LRU淘汰
或请求落到另一实例时同样发生。根据源码建立等价证据，未做线上耗时测量。

调用链：TaskCard → thumbnail → ItemThumbnail → 内存缓存未命中 →
OpenItemContent → provider.OpenResult/放大图对象存储 → 获取原始图片字节 →
JPEG缩略图编码 → 写入进程内LRU。普通JSONL结果从头扫描至目标 custom_id；
一个批次多张未缓存卡片可能反复读取同一个结果文件的前缀，不一定读完整文件。

影响范围：缩略图首次展示、缓存淘汰后的滚动回访、应用重启、多实例请求；
网络读取、JSONL扫描和图片解码成本。浏览器获取的字节数减少，不等于后端
源读取成本消失。元数据列表分页与这条取图链路独立。

疑似根因：已直接确认。缩略图只保存到内存LRU，没有持久化派生对象，也没有
在结果入库时预生成；历史补齐没有可跨重启复用的产物。列表慢的线上贡献比例
尚未测量，不能把所有延迟归于此路径。

推荐修改方案：新结果在已有结果处理/入库流程拿到图片字节时生成并持久化
缩略图，列表直接读缩略图对象；已有作品后台或受控按需补齐一次后持久化。
内存缓存仅加速缩略图读取。应先核对项目对象存储、保存期限和删除契约，不能
盲目把供应商临时结果永久保存，也不能在每次列表请求时同步重建。

风险：保留原图/派生图同源身份、owner/key检查、校验值/版本、删除生命周期、
并发幂等、失败恢复和资源上限；不重跑生成、不改费用、不通过提高并发掩盖慢读。

## 历史数据到底来自哪里

| 数据 | 实际来源及证据 |
| --- | --- |
| 上轮截图的纸船、历史任务、模型示例价格 | `.omo/evidence/gallery-optimization/fixture-server.mjs`第16行起循环创建合成记录；图片为既有本地测试素材，无真实用户历史访问 |
| 正式列表的模型、提示词预览、时间与状态 | `backend/internal/repository/image_studio_gallery.go:14`联表查询 `batch_image_jobs`/`batch_image_items`，绑定用户及key，24条分页；不查询请求日志或使用记录 |
| 提示词最初怎么进表 | `BatchImagePublicService.createPendingItems`提交时保存一次 prompt_preview，随后结果处理器更新条目；读取列表时不重新解析历次请求 |
| 缩略图缓存命中 | `backend/internal/service/image_studio_thumbnail.go:88`返回内存字节，不打开源图 |
| 缩略图缓存未命中 | 同文件第102行调用OpenItemContent，第110行读取原图，第117行编码；只有第124行写内存，没有派生对象写入 |
| 普通结果读取 | `backend/internal/service/batch_image_download.go:201`打开供应商结果，第697行起逐行找 custom_id；OpenAI读取已存对象、Gemini下载结果文件、Vertex读取GCS结果对象 |

“我的作品”目前覆盖已记录的批次生图任务，并不代表自动恢复所有普通API请求
中生成过的图片。提交时写一次任务记录与打开列表时扫描请求日志是不同的成本。

## 固定参照与后续验收

VSO revision：4edef85ab3716fd43989b4a49561a148ba500d93，使用git show检查。
源码：`backend/src/main/java/com/vso/controlplane/service/ImageVariantStorageService.java`
的persistVariants写持久化对象；resolveVariant优先找派生对象，缺少时才读取
原图并补齐。ImagePreviewService只负责编码，不依靠请求时的本地缓存。

测试：`backend/src/test/java/com/vso/controlplane/service/ImageVariantStorageServiceTest.java`
的eagerlyStoresBothVariantsAndServesWithoutReadingOriginal、
historicalSourceIsBackfilledOnceAndThenReused使用会抛错的原图读取器，验证预存
和补齐后不再读原图。这是目前Sub2内存版尚未满足的契约；未复制源码，原有MIT
UI许可不变。既有20260930 OpenAI COS seek问题处理的是结果上传失败，不能把
它的修复当作缩略图持久化已实现。

后续必须验证：新任务列表不读原图；旧图补齐后第二次不读原图；重建服务实例
仍复用已存缩略图；多卡片不反复扫同一批次；越权、删除、损坏和失败恢复受控。
分别统计浏览器响应、后端源对象读取次数/字节与耗时，不能只统计前端/content
接口。此次仅核对与修正文档，没有修改运行代码、提交、推送或部署。
