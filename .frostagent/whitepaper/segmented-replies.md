# Segmented replies

The instance-level **设置 → Bot 行为与回复策略 → 允许多段回复**
switch (`ENABLE_MULTI_SEGMENT_REPLY`) defaults to off and applies without a
restart. It uses the existing settings store and backup format; no migration is
needed.

When enabled, the runtime injects `<frostagent_msg_guidance>` immediately before
the generated `<frostagent_skills>` system message, after the primary system
message. It refreshes every model iteration and appears in Prompt Inspector,
not persistent conversation history.
The guidance starts with “为保证拟真的对话效果，你被允许多段回复用户消息。”;
it ends by telling the model that `send_message` accepts `forcefully_avoid_separation: true`
when the literal marker text must be sent without segmentation.

Ordinary final text is split on bare `<FROSTAGENT_SEGMENT_MARK>` and blank
paragraph separators (LF or CRLF double newlines). Single newlines stay intact.
Backticks have no special meaning: markers and blank lines inside backtick spans
or fences are split or removed like any other text. Empty segments are skipped.
When disabled, bare markers are removed but paragraph breaks remain.
In structured `send_message` chains and legacy structured final replies, plain
text components drop markers in either mode. The optional top-level
`send_message.forcefully_avoid_separation` (default false) keeps markers in plain
components as ordinary literal text when true; it is not part of the restricted
proactive specific-reply or awareness send schemas, which always drop markers.

The optional **分段消息上限** (`MULTI_SEGMENT_REPLY_MAX_SEGMENTS`, 0–20, default
0 = unlimited, applies only while segmentation is enabled) caps delivered
segments. When the model returns more segments than the cap, the runtime keeps a
random subset of the cap size in original order and discards the rest before
delivery, so dropped segments never reach history or the platform.

OneBot sends segments in order, using existing session-turn and
epoch guards. Group quote/mention settings apply only to the first segment of a
turn. Follow-up segments are plain messages and do not inherit the quote or @
decoration, which prevents a multi-segment reply from repeatedly @-ing the
trigger sender. Billing receipts attach only to the last segment and are not
parsed as model output.

OneBot requires each platform ACK before sending the next segment. A failure or interruption
stops the unsent suffix. History and group context retain only the confirmed
prefix; incomplete turns do not enqueue automatic memory extraction. Successful
replies are recorded once, with paragraph separators instead of bare markers.
A session reset prevents old-epoch delivery callbacks from restoring history.

Generated final text is excluded from the private execution transcript until
confirmed. Each confirmed segment updates one cumulative public-history entry
and the originating live turn's transcript atomically. The public entry is
marked as already represented in that transcript so an interrupted successor
inherits only the confirmed prefix, once and after the original input/tool
trajectory. Late confirmations update public history without rewriting a
successor's already-running private transcript. They are immediately associated
with their originating turn and queued in that lane. At the next preparation
boundary, the latest confirmed prefix is inserted after that turn's input/tool
trajectory (or replaces its earlier prefix), before shared-history filtering.
This also handles a late first segment and out-of-order confirmations from
multiple interrupted turns. The per-turn checkpoints are cleared when the task
ends; session resets continue to reject old-epoch callbacks.
