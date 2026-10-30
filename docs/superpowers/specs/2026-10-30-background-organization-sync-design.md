# Background Organization Sync Design

## Goal

Make the manual organization sync request independent of the Electron client's
25-second HTTP timeout. A manual sync must not leave organization data partially
updated because the originating HTTP request was cancelled.

## Scope

- Change the manual `POST /ad/sync` trigger to start a background sync task.
- Serialize AD and digital-employee organization writes across manual, startup,
  and scheduled triggers.
- Continue digital-employee synchronization when AD synchronization fails.
- Change the desktop notification to say that the sync task has started.

No persistent task model, status query API, data-schema change, or digital-
employee protocol change is included.

## Trigger Flow

`SyncADOrganization` starts a goroutine with `syncBackgroundCtx()` and returns
immediately. The goroutine acquires one organization-sync mutex, executes AD
sync, then executes digital-employee sync when its feature flag is enabled.

The AD and digital-employee outcomes are logged independently. An AD failure
does not stop the digital-employee phase. A digital-employee failure does not
roll back a completed AD phase.

All existing startup and scheduled triggers call the same serialized background
entry point. This removes races between an initial or scheduled trigger and a
user-triggered run.

## Client Behavior

The sync button only waits for the request that starts the background task. On
success, it displays `同步任务已启动，请稍后刷新组织架构` and refreshes the
department list after a short delay. The response confirms task acceptance, not
completion.

## Failure Handling

- The background task owns its context and is not cancelled when the caller's
  HTTP request times out or disconnects.
- AD failures are logged and the digital-employee phase continues.
- Digital-employee failures are logged after the AD phase has completed.
- The mutex is released with `defer` on every execution path.

## Verification

- A manual trigger returns before a deliberately blocked sync finishes.
- Cancelling the trigger request does not cancel the background task context.
- A failing AD phase still invokes the digital-employee phase.
- Concurrent triggers execute their write phases one at a time.
- The client reports that a task started rather than that synchronization
  completed.
