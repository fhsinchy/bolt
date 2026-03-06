# internal/queue

FIFO queue manager with configurable max concurrent downloads.

## Design

- Uses `StartFunc` and `PauseFn` callbacks to avoid circular import with engine
- `notify` channel (buffered at 1) — "something changed" signal
- Queue order persisted via `queue_order` column in downloads table
- `GetNextQueued` from DB returns lowest queue_order download
- **DB is source of truth**: `evaluate()` queries `CountByStatus(active)` from DB instead of maintaining an in-memory counter — prevents drift from double-decrements or missed events

## Evaluation Loop

When signaled, `evaluate()` loops: query active count from DB, if `activeCount < maxConcurrent`, fetch next queued download and call `startFn`. If start fails, mark download as error and try next.

## Concurrency Enforcement

All resume/retry paths go through the queue (`EnqueueResume`, `EnqueueResumeAll`) — they set status to queued and signal, letting `evaluate()` decide whether to start based on DB state. No code path bypasses the queue to start downloads directly.

`SetMaxConcurrent` calls `pauseExcess()` which pauses the newest active downloads (highest queue_order) when the limit is reduced.

## Signals

Queue re-evaluates when:
- New download enqueued (`Enqueue`)
- Active download completes/fails/pauses (`OnDownloadComplete`)
- Download resumed via queue (`EnqueueResume`, `EnqueueResumeAll`)
- Max concurrent setting changes (`SetMaxConcurrent`)
