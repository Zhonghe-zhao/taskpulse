# MemoBridge SDK Migration Request

This is the remaining cross-repository integration work. The TaskPulse side
already provides:

- `pkg/taskpulse.Client` for task creation, claim, heartbeat, progress,
  complete, fail, cancellation, and event queries;
- `pkg/taskpulseworker.Runtime` for polling, workflow dispatch, lease
  heartbeat, token/version refresh, completion, failure classification, and
  graceful shutdown;
- idempotent task creation and idempotent complete/fail semantics on the
  server.

## Required MemoBridge changes

1. Add a local development dependency in `E:\CS\memobridge\go.mod`:

   ```go
   replace github.com/zhaozhonghe/taskpulse => ../TaskPulse
   ```

2. Replace the handwritten `internal/taskpulse` HTTP protocol and the manual
   `SemanticProfileWorkerLoop` with:

   ```go
   github.com/zhaozhonghe/taskpulse/pkg/taskpulse
   github.com/zhaozhonghe/taskpulse/pkg/taskpulseworker
   ```

3. Keep the existing SemanticProfile business executor. It must still own:

   - loading `SourceItem` from MemoBridge PostgreSQL;
   - recomputing and checking `content_hash`;
   - checking `prompt_version`;
   - calling the LLM;
   - validating the structured output;
   - idempotent SemanticProfile upsert.

4. Register only this workflow in the first integration:

   ```text
   memobridge.semantic_profile
   ```

5. Return only a small `ResultRef`, for example:

   ```json
   {
     "source_item_id": 11778,
     "content_hash": "sha256:...",
     "prompt_version": "source_semantic_profile:v1"
   }
   ```

   Do not send SourceItem content, the prompt, or the complete LLM output to
   TaskPulse.

6. Add a `Dockerfile.worker` whose entrypoint is:

   ```text
   ./cmd/memobridge-worker
   ```

   The process must run in the foreground and handle SIGTERM gracefully.

## Acceptance checks

- One real SemanticProfile task reaches `succeeded` and creates one profile.
- Repeating the same request with the same workflow and idempotency key returns
  the original task and does not call the LLM again.
- A retryable provider failure enters `retrying` and later succeeds.
- A permanent `source_changed` or `invalid_model_output` failure enters
  `failed` without retrying.
- Killing the Worker after Claim allows lease expiry and recovery by a new
  Worker.
- The TaskPulse task result contains only `result_ref` data.
- MemoBridge `go test ./...` passes after migration.

## Boundary

TaskPulse does not access MemoBridge PostgreSQL. MemoBridge does not reimplement
TaskPulse Claim, Lease, Heartbeat, Retry, or recovery logic.
