// Package onebot implements the OneBot v11 protocol adapter for FrostAgent.
//
// # Compatibility Baseline and Vendor Parity Principles
//
// FrostAgent integrates with QQ through OneBot v11 upstream implementations.
// While both NapCat and LuckyLillia (LLBot) provide OneBot v11 interfaces, their
// wire formats, message ID lifecycles, failure semantics, and media representations
// differ in practice. FrostAgent adheres to the following core compatibility principles:
//
// 1. NapCat is the primary compatibility baseline (Canonical Baseline).
// In any scenario where NapCat and another vendor exhibit conflicting behaviors that
// cannot be simultaneously unified, NapCat's existing behavior is preserved.
//
// 2. Adapter-boundary normalization.
// Vendor parity is achieved via incoming normalization and vendor shims at the OneBot
// adapter boundary. Vendor-specific branches must not leak into core, LLM, memory,
// or sticker management layers.
//
// 3. Inbound Segment Normalization:
//   - Sticker Subtype: NapCat sends image stickers with `data.sub_type = 1`.
//     LuckyLillia sends image stickers with camelCase `data.subType = 1`.
//     The adapter canonicalizes incoming segments so that both keys are populated,
//     ensuring consistent sticker observation, stealing, and vision handling.
//   - Market Face (QQ 商城表情): NapCat represents market faces as `image` segments
//     bearing `emoji_id` / `emoji_package_id` metadata. LuckyLillia represents them
//     with native `mface` segments. The adapter provides identical canonical semantics:
//     both produce the `[图片] ` placeholder in text extraction, both evaluate as containing
//     images for vision processing, both resolve to the official VIP QQ CDN URL,
//     and both are eligible for sticker collection.
//   - Audio / Voice: Wire type `record` is standard; aliases `audio` and `voice` are
//     safely recognized as `[语音] ` in text extraction.
//
// 4. Outbound Dual-Write:
// When sending sticker image segments, FrostAgent explicitly writes both `sub_type: 1`
// and `subType: 1` in `data`. Neither field may be removed, guaranteeing delivery across
// both NapCat and LuckyLillia.
//
// 5. Vendor-Local Message ID Handle & Connection Scoping:
// OneBot `message_id` values are vendor-local, opaque, connection-scoped handles.
// NapCat computes positive int32 hashes mapped in memory, while LuckyLillia uses
// signed int32 keys backed by a database. The same real QQ message will NOT share
// the same OneBot `message_id` across different upstreams or reconnects.
// FrostAgent does not treat OneBot `message_id` as a globally portable or stable message
// identity. In addition:
//   - Sticker observations are explicitly tagged with connection generations (ObservationScope),
//     ensuring that after an upstream switch or reconnect, stale `message_id` handles from an older
//     connection cannot be queried against the new connection or collide with short IDs; connection
//     teardown flushes its scoped cache.
//   - Outbound quote/reply validation at SendHook boundary: When `send_message` or final structured
//     output contains a `quote` component, the adapter validates that the referenced `message_id`
//     was observed or sent on the active connection generation for that exact session. Stale or
//     cross-generation IDs are rejected before wire serialization, preventing upstream vendor
//     divergence where NapCat vs LuckyLillia handle stale reply segments inconsistently.
// All lookups via `get_msg` (for quotes, replies, or historical stickers)
// strictly degrade to empty context on failure, stale IDs, deleted messages, or timeouts,
// ensuring upstream discrepancies never stall the core dialogue pipeline.
//
// 6. Canonical Send Failure Semantics:
//   - Pre-flight validation (O(1) memory): Outbound messages are validated locally before dispatch;
//     local paths for every media type (`image`, `record`, `video`, `file`) are validated for
//     existence, readability, and non-emptiness upfront (Fail-Fast) using file metadata inspection
//     (`os.Stat`) and 1-byte probing (`os.Open` + read) to avoid loading multi-megabyte/gigabyte
//     media into RAM. Full payload buffering into memory is strictly reserved for sticker base64 encoding.
//   - Cross-container filesystem boundary: Pre-flight validation confirms readability within FrostAgent's
//     filesystem context. Because non-sticker media is transmitted over OneBot v11 as `file://<local path>`,
//     separately-containerized OneBot upstreams require a shared filesystem mount (e.g. shared Docker volume)
//     to resolve local file paths. Remote or isolated-container path virtualization is out of scope.
//   - Upstream ACK semantics: Dispatched actions wait for upstream ACK (`SendActionAndWait`).
//     If upstream returns an error (retcode != 0, as LuckyLillia does on converter error,
//     or on mute/risk control), FrostAgent treats the message as undelivered, prevents
//     history commit, and logs delivery failure. If upstream returns status="ok" (NapCat's
//     partial-send converter model), FrostAgent treats the transmission as delivered.
//
// 7. Inbound Failure Semantics Observability:
// If an upstream encounters media download failure before dispatch, NapCat may drop the
// media segment while forwarding text, whereas LuckyLillia may fail the entire message event.
// FrostAgent logs diagnostics when media payloads lack readable URLs or fail to download,
// ensuring operational transparency without silent behavioral drift.
package onebot
