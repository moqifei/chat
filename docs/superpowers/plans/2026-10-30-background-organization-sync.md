# Background Organization Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Make organization-page synchronization a serialized background job that outlives the Electron client's 25-second request timeout.

**Architecture:** A small Chat RPC coordinator owns a single mutex. It runs requested AD and digital-employee phases in order, records phase errors independently, and is invoked by manual, startup, and cron triggers. The Electron page acknowledges task acceptance rather than falsely reporting completion.

**Tech Stack:** Go 1.22, gRPC, React, TypeScript, Axios, Ant Design.

## Global Constraints

- Preserve the AD and digital-employee storage schemas and external API payloads.
- AD failure must not prevent a requested digital-employee phase.
- Manual sync must use syncBackgroundCtx rather than request context.
- Do not add a task table, polling API, new dependency, or configuration.

---

### Task 1: Add and test the organization-sync coordinator

**Files:**

- Create: internal/rpc/chat/sync_organization.go
- Create: internal/rpc/chat/sync_organization_test.go
- Modify: internal/rpc/chat/start.go:108-124

**Interfaces:**

- Produces organizationSyncScope with AD and DigitalEmployee boolean fields.
- Produces organizationSyncResult with ADErr and DigitalEmployeeErr error fields.
- Produces executeOrganizationSync(ctx, mu, scope, runAD, runDigitalEmployee).
- Produces enqueueOrganizationSync(run), which starts run(syncBackgroundCtx()) in a goroutine.

- [ ] **Step 1: Write failing tests**

```go
func TestExecuteOrganizationSyncRunsDigitalEmployeeAfterADFailure(t *testing.T) {
	var mu sync.Mutex
	adErr := errors.New("LDAP unavailable")
	calls := make([]string, 0, 2)
	result := executeOrganizationSync(context.Background(), &mu,
		organizationSyncScope{AD: true, DigitalEmployee: true},
		func(context.Context) error { calls = append(calls, "ad"); return adErr },
		func(context.Context) error { calls = append(calls, "digital"); return nil },
	)
	if !errors.Is(result.ADErr, adErr) || result.DigitalEmployeeErr != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !reflect.DeepEqual(calls, []string{"ad", "digital"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestExecuteOrganizationSyncSerializesConcurrentRuns(t *testing.T) {
	var mu sync.Mutex
	firstEntered := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{}, 1)
	go executeOrganizationSync(context.Background(), &mu, organizationSyncScope{AD: true},
		func(context.Context) error { firstEntered <- struct{}{}; <-releaseFirst; return nil }, nil)
	<-firstEntered
	go executeOrganizationSync(context.Background(), &mu, organizationSyncScope{AD: true},
		func(context.Context) error { secondEntered <- struct{}{}; return nil }, nil)
	select { case <-secondEntered: t.Fatal("concurrent sync entered"); case <-time.After(30 * time.Millisecond): }
	close(releaseFirst)
	select { case <-secondEntered: case <-time.After(time.Second): t.Fatal("second sync did not run") }
}

func TestEnqueueOrganizationSyncUsesBackgroundContext(t *testing.T) {
	completed := make(chan context.Context, 1)
	enqueueOrganizationSync(func(ctx context.Context) { completed <- ctx })
	select {
	case ctx := <-completed:
		if ctx.Err() != nil { t.Fatal("background context is cancelled") }
	case <-time.After(time.Second):
		t.Fatal("queued sync did not start")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```powershell
docker run --rm -v "D:\openim\chat:/workspace" -w /workspace chat:latest go test ./internal/rpc/chat -run "Test(ExecuteOrganizationSync|EnqueueOrganizationSync)" -count=1
```

Expected: FAIL because the coordinator symbols do not yet exist.

- [ ] **Step 3: Implement minimal coordinator**

```go
type organizationSyncScope struct { AD, DigitalEmployee bool }
type organizationSyncResult struct { ADErr, DigitalEmployeeErr error }

func executeOrganizationSync(ctx context.Context, mu *sync.Mutex, scope organizationSyncScope, runAD func(context.Context) error, runDigitalEmployee func(context.Context) error) organizationSyncResult {
	mu.Lock()
	defer mu.Unlock()
	result := organizationSyncResult{}
	if scope.AD && runAD != nil { result.ADErr = runAD(ctx) }
	if scope.DigitalEmployee && runDigitalEmployee != nil { result.DigitalEmployeeErr = runDigitalEmployee(ctx) }
	return result
}

func enqueueOrganizationSync(run func(context.Context)) { go run(syncBackgroundCtx()) }
```

Add organizationSyncMu sync.Mutex to chatSvr. Do not add dependencies.

- [ ] **Step 4: Run tests and verify success**

Run the Step 2 command. Expected: PASS for all three tests.

- [ ] **Step 5: Commit Task 1**

```powershell
git add internal/rpc/chat/start.go internal/rpc/chat/sync_organization.go internal/rpc/chat/sync_organization_test.go
git commit -m "feat: serialize organization synchronization"
```

### Task 2: Route startup, cron, and manual triggers through the coordinator

**Files:**

- Modify: internal/rpc/chat/sync_ad.go:56-105 and :90-258
- Modify: internal/rpc/chat/sync_ad_api.go:176-204
- Modify: internal/rpc/chat/sync_digital_employee.go:179-208
- Modify: internal/rpc/chat/sync_organization.go
- Test: internal/rpc/chat/sync_organization_test.go

**Interfaces:**

- Consumes Task 1 coordinator.
- Produces queueOrganizationSync(scope organizationSyncScope).
- Changes runADSync(ctx context.Context) to return error.

- [ ] **Step 1: Preserve the AD-failure regression test**

Run:

```powershell
docker run --rm -v "D:\openim\chat:/workspace" -w /workspace chat:latest go test ./internal/rpc/chat -run TestExecuteOrganizationSyncRunsDigitalEmployeeAfterADFailure -count=1
```

Expected: PASS before wiring changes.

- [ ] **Step 2: Make AD errors observable and centralize phase policy**

Change every early exit in runADSync from return to return err. Preserve the log message at each error branch and append return nil after the completed log.

Add this wrapper:

```go
func (o *chatSvr) runOrganizationSync(ctx context.Context, scope organizationSyncScope) {
	result := executeOrganizationSync(ctx, &o.organizationSyncMu, scope,
		func(ctx context.Context) error {
			if o.ADClient == nil || !o.ADConfig.Sync.Enable { return nil }
			return o.runADSync(ctx)
		},
		func(ctx context.Context) error {
			if !o.DigitalEmployeeConfig.Enable { return nil }
			return o.runDigitalEmployeeSync(ctx)
		},
	)
	if result.ADErr != nil { log.ZError(ctx, "organization sync: AD phase failed", result.ADErr) }
	if result.DigitalEmployeeErr != nil { log.ZError(ctx, "organization sync: digital employee phase failed", result.DigitalEmployeeErr) }
}

func (o *chatSvr) queueOrganizationSync(scope organizationSyncScope) {
	enqueueOrganizationSync(func(ctx context.Context) { o.runOrganizationSync(ctx, scope) })
}
```

Replace AD scheduler calls with:

```go
o.queueOrganizationSync(organizationSyncScope{AD: true})
```

Replace digital scheduler calls with:

```go
o.queueOrganizationSync(organizationSyncScope{DigitalEmployee: true})
```

Remove digitalEmployeeSyncMu because every production caller now takes the shared lock. Replace the manual RPC body with:

```go
o.queueOrganizationSync(organizationSyncScope{AD: true, DigitalEmployee: true})
return &chat.SyncADOrganizationResp{SyncTime: time.Now().Unix()}, nil
```

- [ ] **Step 3: Run the full Chat RPC test package**

Run:

```powershell
docker run --rm -v "D:\openim\chat:/workspace" -w /workspace chat:latest go test ./internal/rpc/chat -count=1
```

Expected: PASS, including snapshot and coordinator tests.

- [ ] **Step 4: Commit Task 2**

```powershell
git add internal/rpc/chat/sync_ad.go internal/rpc/chat/sync_ad_api.go internal/rpc/chat/sync_digital_employee.go internal/rpc/chat/sync_organization.go internal/rpc/chat/sync_organization_test.go
git commit -m "fix: run manual organization sync in background"
```

### Task 3: Change client feedback to task acceptance

**Files:**

- Modify: src/pages/contact/organization/index.tsx:123-132

**Interfaces:**

- Consumes the unchanged syncADOrganization API; a successful response now means the background task was accepted.

- [ ] **Step 1: Replace the completion message**

Replace:

```ts
message.success("同步成功");
refreshDepts();
```

with:

```ts
message.info("同步任务已启动，请稍后刷新组织架构");
window.setTimeout(() => {
  refreshDepts();
}, 2000);
```

Keep the existing error message; it now means task submission failed.

- [ ] **Step 2: Run client verification**

Run:

```powershell
npx prettier --check src/pages/contact/organization/index.tsx
npm run build
```

Expected: formatting and Vite build succeed.

- [ ] **Step 3: Commit Task 3**

```powershell
git add src/pages/contact/organization/index.tsx
git commit -m "fix: report organization sync task start"
```

### Task 4: Verify against the local Mock Server

**Files:**

- No source changes expected.

- [ ] **Step 1: Build and recreate Chat**

```powershell
docker build -t chat:latest D:\openim\chat
docker compose -f D:\openim\openim-docker\docker-compose.yaml up -d --force-recreate openim-chat
```

Expected: Chat starts with digitalEmployee.enable true and the configured Mock Server base URL.

- [ ] **Step 2: Start the Mock Server**

```powershell
cd D:\openim
python -u .\mock_digital_employee_server.py
```

Expected: it listens on port 18080.

- [ ] **Step 3: Trigger and observe one manual synchronization**

Click the organization sync button. Expected: the client says the task started; Chat logs show independent AD and digital phase outcomes; Mock Server logs an employeelist request and, for new employees, an external-bind request.

- [ ] **Step 4: Check serialization**

Click sync twice quickly. Expected: phase bodies do not overlap; the second task starts only after the first task releases the coordinator mutex.

