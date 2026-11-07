# Segmented replies

The instance-level **设置 → Bot 行为与回复策略 → 允许多段回复**
switch (`ENABLE_MULTI_SEGMENT_REPLY`) defaults to off and applies without a
restart. It uses the existing settings store and backup format; no migration is
needed.

When enabled, the runtime injects `<frostagent_msg_guidance>` immediately before
the generated `<frostagent_skills>` system message, after the primary system
message. It refreshes every model iteration and appears in Prompt Inspector,
not persistent conversation history.

Ordinary final text is split on bare `<FROSTAGENT_SEGMENT_MARK>` and blank
paragraph separators (LF or CRLF double newlines). Single newlines stay intact.
Matched backtick spans and fences preserve their content, including literal
markers and blank lines. Empty segments are skipped. When disabled, bare
markers are removed but paragraph breaks and backtick-protected markers remain.
The existing structured tool message chain is not segmented; disabled-mode
filtering also covers its plain text components.

Both OneBot and AstrBot send segments in order, using existing session-turn and
epoch guards. Group quote/mention settings apply to each message. Billing
receipts attach only to the last segment and are not parsed as model output.
AstrBot marks all but the last segment as intermediate so its plugin continues
consuming the same reply stream.

OneBot requires each platform ACK before sending the next segment; AstrBot keeps
its existing transport-write confirmation boundary. A failure or interruption
stops the unsent suffix. History and group context retain only the confirmed
prefix; incomplete turns do not enqueue automatic memory extraction. Successful
replies are recorded once, with paragraph separators instead of bare markers.
A session reset prevents old-epoch delivery callbacks from restoring history.
